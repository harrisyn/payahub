package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/claim"
)

// Waiting for a claim to settle (or a member check to answer): a waiter
// writes a claim_waits row, SaveClaim and FinishMemberCheck send a
// claim_saved notification with the id on commit when such a row exists, one
// listening connection per process passes it to whoever is waiting on that
// id, and a slow poll covers a lost notification, a dropped connection, or a
// save that looked for waiters just before one arrived. Nothing holds a
// transaction while it waits.
//
// Notifying only when someone waits matters at volume: Postgres takes one
// database-wide lock for every committing transaction that notified, so a
// notification on every save put all saves in a single line.

const claimSavedChannel = "claim_saved"

// notifyWaiters is run inside a saving transaction with the id as $1.
const notifyWaiters = `SELECT pg_notify('` + claimSavedChannel + `', $1)
	WHERE EXISTS (SELECT 1 FROM claim_waits WHERE id = $1 AND until > now())`

type waiters struct {
	once sync.Once
	// stop ends the listener, so Close does not wait on its connection.
	stop context.CancelFunc
	mu   sync.Mutex
	byID map[string]map[chan struct{}]struct{}
}

// watch registers a waiter on id, in this process and in claim_waits, until
// release is called. The row is written before the caller first looks, so a
// save that commits after that look notifies.
func (s *Store) watch(ctx context.Context, id string) (chan struct{}, func(), error) {
	s.ListenForClaims()
	ch := s.wait.add(id)
	waiter := NewID("w")
	until := time.Now().Add(5 * time.Minute)
	if d, ok := ctx.Deadline(); ok {
		until = d.Add(time.Second)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO claim_waits (id, waiter, until) VALUES ($1, $2, $3)`, id, waiter, until); err != nil {
		s.wait.remove(id, ch)
		return nil, nil, err
	}
	return ch, func() {
		s.wait.remove(id, ch)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.Pool.Exec(ctx, `DELETE FROM claim_waits WHERE id = $1 AND waiter = $2`, id, waiter)
	}, nil
}

func (w *waiters) add(id string) chan struct{} {
	ch := make(chan struct{}, 1)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.byID[id] == nil {
		w.byID[id] = map[chan struct{}]struct{}{}
	}
	w.byID[id][ch] = struct{}{}
	return ch
}

func (w *waiters) remove(id string, ch chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.byID[id], ch)
	if len(w.byID[id]) == 0 {
		delete(w.byID, id)
	}
}

// wake tells the waiters on id (or everyone, for "") to look again.
func (w *waiters) wake(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for wid, chans := range w.byID {
		if id != "" && wid != id {
			continue
		}
		for ch := range chans {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (s *Store) listen(ctx context.Context) {
	for ctx.Err() == nil {
		err := func() error {
			conn, err := s.Pool.Acquire(ctx)
			if err != nil {
				return err
			}
			defer conn.Release()
			if _, err := conn.Exec(ctx, "LISTEN "+claimSavedChannel); err != nil {
				return err
			}
			// Anything saved while we were not listening: look again.
			s.wait.wake("")
			for {
				n, err := conn.Conn().WaitForNotification(ctx)
				if err != nil {
					return err
				}
				s.wait.wake(n.Payload)
			}
		}()
		if err != nil && ctx.Err() == nil {
			select {
			case <-time.After(time.Second):
			case <-ctx.Done():
			}
		}
	}
}

// ListenForClaims starts the process's claim_saved listener, once. The API
// calls it at start so the first caller to wait does not pay for it.
func (s *Store) ListenForClaims() {
	s.wait.once.Do(func() {
		s.wait.byID = map[string]map[chan struct{}]struct{}{}
		var ctx context.Context
		ctx, s.wait.stop = context.WithCancel(context.Background())
		go s.listen(ctx)
		// Rows left by a process that stopped while waiting.
		go func() {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_, _ = s.Pool.Exec(ctx, `DELETE FROM claim_waits WHERE until < now()`)
		}()
	})
}

// Settled is a claim whose submission is no longer in progress, with the
// event that settled it.
type Settled struct {
	Claim *claim.Claim
	Event *Event
}

// WaitSettled waits until no submission job owns the claim (it was
// acknowledged, rejected, sent and awaiting the payer, or returned to ready)
// or ctx ends. It returns ctx's error on timeout; the claim carries on.
func (s *Store) WaitSettled(ctx context.Context, orgID, id string) (Settled, error) {
	ch, release, err := s.watch(ctx, id)
	if err != nil {
		return Settled{}, err
	}
	defer release()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		var body []byte
		var queued bool
		err := s.Pool.QueryRow(ctx, `SELECT body, submission_queued FROM claims WHERE id = $1 AND organization_id = $2`,
			id, orgID).Scan(&body, &queued)
		if errors.Is(err, pgx.ErrNoRows) {
			return Settled{}, ErrNotFound
		}
		if err != nil {
			return Settled{}, err
		}
		if !queued {
			var c claim.Claim
			if err := json.Unmarshal(body, &c); err != nil {
				return Settled{}, err
			}
			out := Settled{Claim: &c}
			var e Event
			err := s.Pool.QueryRow(ctx, `
				SELECT id, kind, COALESCE(from_state, ''), to_state, detail, created_at FROM claim_events
				WHERE claim_id = $1 ORDER BY id DESC LIMIT 1`, id).Scan(&e.ID, &e.Kind, &e.FromState, &e.ToState, &e.Detail, &e.CreatedAt)
			if err == nil {
				out.Event = &e
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return Settled{}, err
			}
			return out, nil
		}
		select {
		case <-ctx.Done():
			return Settled{}, ctx.Err()
		case <-ch:
		case <-poll.C:
		}
	}
}
