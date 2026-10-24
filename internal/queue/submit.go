// Package queue runs claim submission on River, a job queue stored in the
// same Postgres database as the claims. Jobs are inserted in the same
// transaction as the claim change that needs them, so an accepted request is
// never lost: either both commit or neither does. Workers retry with backoff,
// and a job whose worker dies is picked up again after a timeout.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/sandbox"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/mail"
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
func NewClient(st *store.Store, v *vault.Vault, workers int, logger *slog.Logger, mailer mail.Sender) (*Client, error) {
	if mailer == nil {
		mailer = mail.Log{Logger: logger}
	}
	ws := river.NewWorkers()
	river.AddWorker(ws, &EmailWorker{Sender: mailer})
	river.AddWorker(ws, &SubmitWorker{Store: st, Vault: v})
	river.AddWorker(ws, &MemberCheckWorker{Store: st, Vault: v})
	river.AddWorker(ws, &PayoutWorker{Store: st})
	river.AddWorker(ws, &BillingWorker{Store: st, Logger: logger})
	// A caller may be waiting on the payer's answer (submit and wait), so a
	// new job is picked up within a fraction of a second even when the insert
	// notification is missed.
	cfg := &river.Config{Workers: ws, Logger: logger, FetchCooldown: 20 * time.Millisecond, FetchPollInterval: 250 * time.Millisecond}
	if workers > 0 {
		cfg.Queues = map[string]river.QueueConfig{
			SubmissionsQueue: {MaxWorkers: workers},
			MoneyQueue:       {MaxWorkers: 10},
			MailQueue:        {MaxWorkers: 5},
		}
		cfg.PeriodicJobs = []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(billingEvery),
			func() (river.JobArgs, *river.InsertOpts) { return BillingArgs{}, nil },
			&river.PeriodicJobOpts{ID: "billing", RunOnStart: true})}
	}
	return river.NewClient(riverpgxv5.New(st.Pool), cfg)
}

var (
	// ErrNotSubmittable is returned by Enqueue when validation leaves the claim short of ready.
	ErrNotSubmittable = errors.New("claim is not ready to submit")
	ErrUnknownPayer   = errors.New("unknown payer")
	// ErrTrialLimit is returned for a live claim once the trial has ended or used its live claims.
	ErrTrialLimit = store.ErrTrialLimit
	// ErrWalletEmpty is returned for a live claim on a wallet plan the wallet no longer covers.
	ErrWalletEmpty = store.ErrWalletEmpty
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
	m, err := Prepare(ctx, tx, c)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	if c.Environment == "live" {
		if err := store.MayGoLive(ctx, tx, c.OrganizationID); err != nil {
			return c, engine.Validation{}, err
		}
	}
	v, err := validate(ctx, tx, c, m)
	if err != nil {
		return nil, engine.Validation{}, err
	}

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

// Prepare gives the claim's lines their standard items and the payer's codes
// (from the organisation's confirmed item mappings) and returns the rules of
// the connector that reaches the payer. Sandbox claims are sent to the
// sandbox payer but checked against the named payer's rules, so trials see
// realistic validation.
// Check resolves the claim's lines and runs the edit rules with what the
// database knows (the payer's settings for the organisation, other claims
// that may be the same one). It changes nothing.
func Check(ctx context.Context, q store.Querier, c *claim.Claim) (engine.Validation, error) {
	m, err := Prepare(ctx, q, c)
	if err != nil {
		return engine.Validation{}, err
	}
	return validate(ctx, q, c, m)
}

func validate(ctx context.Context, q store.Querier, c *claim.Claim, m connector.Manifest) (engine.Validation, error) {
	overrides, err := store.PayerOverrides(ctx, q, c.OrganizationID, c.PayerID, c.Environment)
	if err != nil {
		return engine.Validation{}, err
	}
	dups, err := store.FindDuplicates(ctx, q, c)
	if err != nil {
		return engine.Validation{}, err
	}
	return engine.Validate(c, m, overrides, engine.Facts{Today: time.Now(), Duplicates: dups}), nil
}

func Prepare(ctx context.Context, q store.Querier, c *claim.Claim) (connector.Manifest, error) {
	m, ok := manifestFor(c)
	if !ok {
		return m, fmt.Errorf("%w %q", ErrUnknownPayer, c.PayerID)
	}
	mapped, err := store.MappedItems(ctx, q, c.OrganizationID, catalog.LocalCodes(c))
	if err != nil {
		return m, err
	}
	catalog.Resolve(c, mapped)
	return m, nil
}

func manifestFor(c *claim.Claim) (connector.Manifest, bool) {
	p, ok := catalog.LookupPayer(c.PayerID)
	if !ok {
		return connector.Manifest{}, false
	}
	m, _, ok := connector.Lookup(p.Connector)
	return m, ok
}

// connectorFor builds the claim's connector: the sandbox payer for sandbox
// claims, otherwise the connector that reaches the payer, with the
// organisation's account on it opened from the vault and the payer's
// catalogue fields.
func (w *SubmitWorker) connectorFor(ctx context.Context, c *claim.Claim) (connector.Connector, error) {
	if c.Environment == "sandbox" {
		return sandbox.New(connector.Account{})
	}
	p, ok := catalog.LookupPayer(c.PayerID)
	if !ok {
		return nil, connector.Errorf(connector.Validation, "unknown payer %q", c.PayerID)
	}
	return OpenAccount(ctx, w.Store.Pool, w.Vault, c.OrganizationID, p, c.Environment)
}

// OpenAccount builds the connector that reaches payer p for an organisation,
// with its sealed account credentials opened.
func OpenAccount(ctx context.Context, q store.Querier, v *vault.Vault, orgID string, p catalog.Payer, env string) (connector.Connector, error) {
	creds := map[string]string{}
	sealed, err := store.SealedAccount(ctx, q, orgID, p.Connector, env)
	if err != nil {
		return nil, err
	}
	if sealed != nil {
		if creds, err = v.Open(sealed, vault.Owner(orgID, p.Connector, env)); err != nil {
			return nil, err
		}
	}
	return connector.New(p.Connector, connector.Account{Credentials: creds, PayerID: p.ID, PayerFields: p.Fields})
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

	overrides, err := store.PayerOverrides(ctx, w.Store.Pool, c.OrganizationID, c.PayerID, c.Environment)
	if err != nil {
		return connector.Receipt{}, err
	}
	// Sandbox claims are checked against the named payer's rules but sent the
	// way the sandbox payer takes them.
	cfg := engine.ResolveConfig(m, overrides)
	if c.Environment == "sandbox" {
		m = conn.Manifest()
	}
	plan := engine.ChoosePlan(m, cfg)
	sub := engine.Build(c, cfg)

	if plan.WholeClaim() && retry {
		found, err := conn.FindClaim(ctx, sub)
		if err == nil && found != nil {
			return *found, nil
		}
		if err != nil && connector.AsError(err).Code != connector.NotSupported {
			return connector.Receipt{}, err
		}
	}
	var progress engine.Progress
	raw, err := store.LoadProgress(ctx, w.Store.Pool, c.ID)
	if err != nil {
		return connector.Receipt{}, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &progress); err != nil {
			return connector.Receipt{}, fmt.Errorf("claim %s progress: %w", c.ID, err)
		}
	}
	journal := func(ctx context.Context, p *engine.Progress) error {
		b, err := json.Marshal(p)
		if err != nil {
			return err
		}
		return store.SaveProgress(ctx, w.Store.Pool, c.ID, b)
	}
	return engine.Send(ctx, conn, sub, plan, &progress, journal)
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
