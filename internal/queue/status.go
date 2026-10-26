package queue

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

// Following a claim after acknowledgement: a periodic sweep queues a status
// job for each claim whose next check is due, and the job asks the payer
// (FetchClaim, outside any transaction) and records what changed.

const statusSweepEvery = 30 * time.Second

type StatusSweepArgs struct{}

func (StatusSweepArgs) Kind() string { return "status_sweep" }

func (StatusSweepArgs) InsertOpts() river.InsertOpts {
	// Only the elected leader inserts periodic jobs, so no uniqueness is
	// needed; overlapping sweeps would take different claims anyway.
	return river.InsertOpts{Queue: FollowUpQueue, MaxAttempts: 1}
}

type StatusSweepWorker struct {
	river.WorkerDefaults[StatusSweepArgs]
	Store *store.Store
}

func (w *StatusSweepWorker) Work(ctx context.Context, _ *river.Job[StatusSweepArgs]) error {
	for {
		n, err := w.sweep(ctx, 500)
		if err != nil || n < 500 {
			return err
		}
	}
}

func (w *StatusSweepWorker) sweep(ctx context.Context, n int) (int, error) {
	tx, err := w.Store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	due, err := store.LeaseStatusChecks(ctx, tx, n)
	if err != nil || len(due) == 0 {
		return 0, err
	}
	client, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return 0, err
	}
	params := make([]river.InsertManyParams, len(due))
	for i, d := range due {
		params[i] = river.InsertManyParams{Args: StatusArgs{ClaimID: d.ClaimID, OrganizationID: d.OrgID}}
	}
	if _, err := client.InsertManyTx(ctx, tx, params); err != nil {
		return 0, err
	}
	return len(due), tx.Commit(ctx)
}

type StatusArgs struct {
	ClaimID        string `json:"claim_id"`
	OrganizationID string `json:"organization_id"`
}

func (StatusArgs) Kind() string { return "claim_status" }

// One try: the claim's next check is already scheduled.
func (StatusArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: FollowUpQueue, MaxAttempts: 1}
}

// CheckStatusNow queues a status check for a claim the payer has, for a
// caller who wants the answer now.
func CheckStatusNow(ctx context.Context, rc *Client, c *claim.Claim) error {
	_, err := rc.Insert(ctx, StatusArgs{ClaimID: c.ID, OrganizationID: c.OrganizationID}, nil)
	return err
}

type StatusWorker struct {
	river.WorkerDefaults[StatusArgs]
	Store *store.Store
	Vault *vault.Vault
}

func (w *StatusWorker) Timeout(*river.Job[StatusArgs]) time.Duration {
	return payerTimeout + 10*time.Second
}

func (w *StatusWorker) Work(ctx context.Context, job *river.Job[StatusArgs]) error {
	c, err := w.Store.GetClaim(ctx, job.Args.OrganizationID, job.Args.ClaimID)
	if errors.Is(err, store.ErrNotFound) {
		return river.JobCancel(err)
	}
	if err != nil {
		return err
	}
	if (c.State != claim.Acknowledged && c.State != claim.Approved) || c.PayerClaimNumber == "" {
		return nil
	}
	conn, err := (&SubmitWorker{Store: w.Store, Vault: w.Vault}).connectorFor(ctx, c)
	var pc *connector.PayerClaim
	if err == nil {
		pctx, cancel := context.WithTimeout(ctx, payerTimeout)
		pc, err = conn.FetchClaim(pctx, c.PayerClaimNumber)
		cancel()
	}
	return w.record(ctx, job.Args, pc, err)
}

// record applies the payer's answer under the claim's lock. A failed
// question changes nothing but the time of the next one.
func (w *StatusWorker) record(ctx context.Context, a StatusArgs, pc *connector.PayerClaim, askErr error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	tx, err := w.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, _, err := store.LockClaim(ctx, tx, a.OrganizationID, a.ClaimID)
	if err != nil {
		return err
	}
	ackAt, err := store.AcknowledgedAt(ctx, tx, c)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	c.StatusCheckedAt = &now
	if askErr == nil && pc != nil && (c.State == claim.Acknowledged || c.State == claim.Approved) {
		if err := applyPayerStatus(ctx, tx, c, pc); err != nil {
			return err
		}
	}
	if err := store.StatusAsked(ctx, tx, c, ackAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applyPayerStatus moves the claim to where the payer says it is, through
// each state in between, with an event for each step, and records a
// payer's question or new words as an event too.
func applyPayerStatus(ctx context.Context, tx pgx.Tx, c *claim.Claim, pc *connector.PayerClaim) error {
	detail := map[string]any{"payer_status": pc.Status}
	if pc.Message != "" {
		detail["payer_message"] = pc.Message
	}
	said := string(pc.Status) != c.PayerStatus || pc.Message != c.PayerMessage
	if pc.Status != connector.PayerUnknown {
		c.PayerStatus, c.PayerMessage = string(pc.Status), pc.Message
	}
	var path []claim.State
	switch pc.Status {
	case connector.PayerApproved:
		path = []claim.State{claim.Approved}
	case connector.PayerPaid:
		path = []claim.State{claim.Approved, claim.Paid}
	case connector.PayerRejected:
		path = []claim.State{claim.Rejected}
	}
	moved := false
	for _, to := range path {
		if c.State == to || !claim.CanTransition(c.State, to) {
			continue
		}
		from := c.State
		c.State = to
		if err := store.SaveClaim(ctx, tx, c, from, false, string(to), detail); err != nil {
			return err
		}
		moved = true
	}
	if !moved && said && pc.Status == connector.PayerQueried {
		return store.SaveClaim(ctx, tx, c, c.State, false, "queried", detail)
	}
	return nil
}
