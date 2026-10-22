// Package queue runs claim submission on River, a job queue stored in the
// same Postgres database as the claims. Jobs are inserted in the same
// transaction as the claim change that needs them, so an accepted request is
// never lost: either both commit or neither does. Workers retry with backoff,
// and a job whose worker dies is picked up again after a timeout.
package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/sandbox"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

const (
	SubmissionsQueue = "submissions"
	maxAttempts      = 20
	payerTimeout     = 60 * time.Second
)

type SubmitArgs struct {
	ClaimID        string `json:"claim_id"`
	OrganizationID string `json:"organization_id"`
}

func (SubmitArgs) Kind() string { return "submit_claim" }

func (SubmitArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: SubmissionsQueue, MaxAttempts: maxAttempts}
}

type Client = river.Client[pgx.Tx]

// NewClient builds a River client. With workers > 0 it also works jobs;
// with 0 it only inserts (the API process).
func NewClient(st *store.Store, v *vault.Vault, workers int, logger *slog.Logger) (*Client, error) {
	ws := river.NewWorkers()
	river.AddWorker(ws, &SubmitWorker{Store: st, Vault: v})
	cfg := &river.Config{Workers: ws, Logger: logger}
	if workers > 0 {
		cfg.Queues = map[string]river.QueueConfig{SubmissionsQueue: {MaxWorkers: workers}}
	}
	return river.NewClient(riverpgxv5.New(st.Pool), cfg)
}

var (
	// ErrNotSubmittable is returned by Enqueue when validation leaves the claim short of ready.
	ErrNotSubmittable = errors.New("claim is not ready to submit")
	ErrUnknownPayer   = errors.New("unknown payer")
	// ErrTrialLimit is returned for a live claim once the trial has ended or used its live claims.
	ErrTrialLimit = errors.New("the trial has no live claims left")
)

// Enqueue validates a claim, moves it to ready and inserts its submit job, all
// in one transaction. Calling it again while a job owns the claim is a no-op.
func Enqueue(ctx context.Context, st *store.Store, rc *Client, orgID, claimID string) (*claim.Claim, engine.Validation, error) {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	defer tx.Rollback(ctx)

	c, queued, err := store.LockClaim(ctx, tx, orgID, claimID)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	if queued || c.State == claim.Submitted || c.State.Billable() {
		return c, engine.Validation{Issues: []engine.Issue{}, State: c.State}, nil
	}
	m, ok := manifestFor(c)
	if !ok {
		return nil, engine.Validation{}, fmt.Errorf("%w %q", ErrUnknownPayer, c.PayerID)
	}
	if c.Environment == "live" {
		allowed, err := store.TrialAllowsLive(ctx, tx, c.OrganizationID)
		if err != nil {
			return nil, engine.Validation{}, err
		}
		if !allowed {
			return c, engine.Validation{}, ErrTrialLimit
		}
	}
	overrides, err := store.PayerOverrides(ctx, tx, c.OrganizationID, c.PayerID, c.Environment)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	v := engine.Validate(c, m, overrides)

	from := c.State
	if v.State != claim.Ready {
		if c.State == claim.Rejected {
			c.State = claim.Draft
		}
		if claim.CanTransition(c.State, v.State) {
			c.State = v.State
		}
		if err := store.SaveClaim(ctx, tx, c, from, false, "validation_failed", v.Issues); err != nil {
			return nil, v, err
		}
		return c, v, errors.Join(ErrNotSubmittable, tx.Commit(ctx))
	}

	if c.State == claim.Rejected {
		c.State = claim.Draft
	}
	c.State = claim.AdvanceToReady(c.State)
	if err := store.SaveClaim(ctx, tx, c, from, true, "submission_queued", map[string]any{"plan": v.Plan}); err != nil {
		return nil, v, err
	}
	if _, err := rc.InsertTx(ctx, tx, SubmitArgs{ClaimID: c.ID, OrganizationID: c.OrganizationID}, nil); err != nil {
		return nil, v, err
	}
	return c, v, tx.Commit(ctx)
}

// manifestFor gives the rules for the claim's payer. Sandbox claims are sent
// to the sandbox payer but checked against the named payer's rules, so trials
// see realistic validation.
func manifestFor(c *claim.Claim) (connector.Manifest, bool) {
	m, _, ok := connector.Lookup(c.PayerID)
	return m, ok
}

// connectorFor builds the claim's connector: the sandbox payer for sandbox
// claims, otherwise the payer's connector with the organisation's credentials
// opened from the vault.
func (w *SubmitWorker) connectorFor(ctx context.Context, c *claim.Claim) (connector.Connector, error) {
	if c.Environment == "sandbox" {
		return sandbox.New(nil)
	}
	creds := map[string]string{}
	sealed, err := store.SealedCredentials(ctx, w.Store.Pool, c.OrganizationID, c.PayerID, c.Environment)
	if err != nil {
		return nil, err
	}
	if sealed != nil {
		if creds, err = w.Vault.Open(sealed, vault.Owner(c.OrganizationID, c.PayerID, c.Environment)); err != nil {
			return nil, err
		}
	}
	return connector.New(c.PayerID, creds)
}

// SubmitWorker sends one claim. It never holds a database transaction open
// across the payer call:
//
//  1. lock, check the claim is still owned by this job, mark it submitted, commit;
//  2. on a retry, ask the payer whether it already has the claim (FindClaim),
//     otherwise send it;
//  3. lock, record the outcome and complete the job in the same transaction.
//
// A crash between 1 and 3 leaves the claim submitted with its job running;
// River re-runs the job and step 2 reconciles instead of double-submitting.
type SubmitWorker struct {
	river.WorkerDefaults[SubmitArgs]
	Store *store.Store
	Vault *vault.Vault
}

func (w *SubmitWorker) Timeout(*river.Job[SubmitArgs]) time.Duration { return 2 * payerTimeout }

func (w *SubmitWorker) Work(ctx context.Context, job *river.Job[SubmitArgs]) error {
	c, proceed, err := w.begin(ctx, job)
	if err != nil || !proceed {
		return err
	}

	receipt, sendErr := w.send(ctx, c, job.Attempt > 1)
	outcome := engine.Decide(receipt, sendErr)
	return w.finish(ctx, job, outcome)
}

func (w *SubmitWorker) begin(ctx context.Context, job *river.Job[SubmitArgs]) (*claim.Claim, bool, error) {
	tx, err := w.Store.Pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	c, queued, err := store.LockClaim(ctx, tx, job.Args.OrganizationID, job.Args.ClaimID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, river.JobCancel(err)
	}
	if err != nil {
		return nil, false, err
	}
	if !queued || (c.State != claim.Ready && c.State != claim.Submitted) {
		// Already settled by an earlier run of this job: nothing to do.
		if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
			return nil, false, err
		}
		return nil, false, tx.Commit(ctx)
	}
	if c.State == claim.Ready {
		c.State = claim.Submitted
		if err := store.SaveClaim(ctx, tx, c, claim.Ready, true, "submission_started",
			map[string]any{"attempt": job.Attempt}); err != nil {
			return nil, false, err
		}
	}
	return c, true, tx.Commit(ctx)
}

func (w *SubmitWorker) send(ctx context.Context, c *claim.Claim, retry bool) (connector.Receipt, error) {
	conn, err := w.connectorFor(ctx, c)
	if err != nil {
		return connector.Receipt{}, err
	}
	m, ok := manifestFor(c)
	if !ok {
		return connector.Receipt{}, connector.Errorf(connector.Validation, "unknown payer %q", c.PayerID)
	}
	ctx, cancel := context.WithTimeout(ctx, payerTimeout)
	defer cancel()

	if retry {
		found, err := conn.FindClaim(ctx, c)
		if err == nil && found != nil {
			return *found, nil
		}
		if err != nil && connector.AsError(err).Code != connector.NotSupported {
			return connector.Receipt{}, err
		}
	}
	overrides, err := store.PayerOverrides(ctx, w.Store.Pool, c.OrganizationID, c.PayerID, c.Environment)
	if err != nil {
		return connector.Receipt{}, err
	}
	// Sandbox claims are checked against the named payer's rules but sent the
	// way the sandbox payer takes them.
	if c.Environment == "sandbox" {
		m = conn.Manifest()
	}
	return engine.Send(ctx, conn, c, engine.ChoosePlan(m, engine.ResolveConfig(m, overrides)))
}

func (w *SubmitWorker) finish(ctx context.Context, job *river.Job[SubmitArgs], o engine.Outcome) error {
	tx, err := w.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	c, _, err := store.LockClaim(ctx, tx, job.Args.OrganizationID, job.Args.ClaimID)
	if err != nil {
		return err
	}
	detail := map[string]any{"attempt": job.Attempt}
	if o.Err != nil {
		detail["error"] = map[string]string{
			"code": string(o.Err.Code), "message": o.Err.UserMessage, "payer_message": o.Err.PayerMessage,
		}
	}
	if !o.Final {
		// Record the failed attempt, keep the job, and let River retry with backoff.
		if err := store.SaveClaim(ctx, tx, c, c.State, true, "attempt_failed", detail); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return o.Err
	}

	from := c.State
	if o.Receipt != nil {
		c.PayerClaimNumber = o.Receipt.PayerClaimNumber
		detail["receipt"] = o.Receipt
	}
	c.State = o.State
	kind := map[claim.State]string{
		claim.Acknowledged: "acknowledged", claim.Submitted: "submitted",
		claim.Rejected: "rejected", claim.Ready: "returned_to_ready",
	}[o.State]
	if err := store.SaveClaim(ctx, tx, c, from, false, kind, detail); err != nil {
		return err
	}
	if _, err := river.JobCompleteTx[*riverpgxv5.Driver](ctx, tx, job); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
