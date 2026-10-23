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

// Waiting for a claim to settle: SaveClaim sends a claim_saved notification
// on commit, one listening connection per process passes it to whoever is
// waiting on that claim, and a slow poll covers a lost notification or a
// dropped connection. Nothing holds a transaction while it waits.

const claimSavedChannel = "claim_saved"

type waiters struct {
	once sync.Once
	mu   sync.Mutex
	byID map[string]map[chan struct{}]struct{}
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

func (s *Store) listen() {
	for {
		err := func() error {
			conn, err := s.Pool.Acquire(context.Background())
			if err != nil {
				return err
			}
			defer conn.Release()
			if _, err := conn.Exec(context.Background(), "LISTEN "+claimSavedChannel); err != nil {
				return err
			}
			// Anything saved while we were not listening: look again.
			s.wait.wake("")
			for {
				n, err := conn.Conn().WaitForNotification(context.Background())
				if err != nil {
					return err
				}
				s.wait.wake(n.Payload)
			}
		}()
		if err != nil {
			time.Sleep(time.Second)
		}
	}
}

// ListenForClaims starts the process's claim_saved listener, once. The API
// calls it at start so the first caller to wait does not pay for it.
func (s *Store) ListenForClaims() {
	s.wait.once.Do(func() {
		s.wait.byID = map[string]map[chan struct{}]struct{}{}
		go s.listen()
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
	s.ListenForClaims()
	ch := s.wait.add(id)
	defer s.wait.remove(id, ch)
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
