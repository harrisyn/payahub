package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/riverqueue/river"

	"github.com/harrisyn/payahub/internal/payment"
	"github.com/harrisyn/payahub/internal/store"
)

// Money jobs: paying out approved withdrawals, and the periodic billing
// round (monthly fees, and checkouts and payouts whose webhook never came).
// Like claims, every one reconciles with the provider before it acts, and
// no transaction is held open across a provider call.

const (
	MoneyQueue   = "money"
	billingEvery = 15 * time.Minute
	// payoutRecheck is how long a payout the provider is still processing
	// waits before it is asked about again.
	payoutRecheck = 10 * time.Minute
)

type PayoutArgs struct {
	WithdrawalID string `json:"withdrawal_id"`
}

func (PayoutArgs) Kind() string { return "pay_withdrawal" }

func (PayoutArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: MoneyQueue, MaxAttempts: 25}
}

type PayoutWorker struct {
	river.WorkerDefaults[PayoutArgs]
	Store *store.Store
}

func (w *PayoutWorker) Work(ctx context.Context, job *river.Job[PayoutArgs]) error {
	done, err := ReconcileWithdrawal(ctx, w.Store, job.Args.WithdrawalID, true)
	if err != nil {
		return err
	}
	if !done {
		return river.JobSnooze(payoutRecheck)
	}
	return nil
}

// ApproveWithdrawal approves a requested withdrawal and queues its payout in
// one transaction.
func ApproveWithdrawal(ctx context.Context, st *store.Store, rc *Client, id, by string) (store.Withdrawal, error) {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return store.Withdrawal{}, err
	}
	defer tx.Rollback(ctx)
	wd, err := store.ApproveWithdrawal(ctx, tx, id, by)
	if err != nil {
		return wd, err
	}
	if _, err := rc.InsertTx(ctx, tx, PayoutArgs{WithdrawalID: id}, nil); err != nil {
		return wd, err
	}
	return wd, tx.Commit(ctx)
}

// ReconcileWithdrawal asks the provider about an approved or paying
// withdrawal and books what it says. With send, a payout the provider has
// never seen is sent (the withdrawal id is its reference, so a repeat is a
// no-op there). It reports whether the withdrawal is closed.
func ReconcileWithdrawal(ctx context.Context, st *store.Store, id string, send bool) (bool, error) {
	wd, err := st.GetWithdrawal(ctx, "", id)
	if err != nil {
		return false, err
	}
	if wd.Status != "approved" && wd.Status != "paying" {
		return true, nil
	}
	p, err := payment.Lookup(wd.Provider)
	if err != nil {
		return false, fmt.Errorf("withdrawal %s: provider %s is not configured", id, wd.Provider)
	}
	res, err := p.VerifyPayout(ctx, wd.ID)
	if errors.Is(err, payment.ErrNotFound) {
		if !send {
			return false, nil
		}
		res, err = p.SendPayout(ctx, payment.Payout{Reference: wd.ID, Amount: wd.Amount, Currency: wd.Currency,
			Recipient: wd.Recipient(), Reason: "Payahub wallet withdrawal"})
	}
	if err != nil {
		return false, err
	}
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	wd, err = store.LockWithdrawal(ctx, tx, id)
	if err != nil {
		return false, err
	}
	if wd.Status != "approved" && wd.Status != "paying" {
		return true, nil // settled meanwhile, by a webhook or another worker
	}
	switch res.Status {
	case payment.Succeeded:
		err = store.SettleWithdrawal(ctx, tx, wd, res.ProviderReference)
	case payment.Failed, payment.Abandoned:
		err = store.ReturnWithdrawal(ctx, tx, wd, "failed", res.Message, "")
	default:
		err = store.MarkPaying(ctx, tx, id, res.ProviderReference, res.Message)
	}
	if err != nil {
		return false, err
	}
	return res.Status.Final(), tx.Commit(ctx)
}

// ReconcileFunding asks the provider about a pending funding and books the
// answer; the ledger is only ever credited from the provider's own record.
func ReconcileFunding(ctx context.Context, st *store.Store, f store.Funding) (store.Funding, error) {
	if f.Status.Final() {
		return f, nil
	}
	p, err := payment.Lookup(f.Provider)
	if err != nil {
		return f, fmt.Errorf("funding %s: provider %s is not configured", f.ID, f.Provider)
	}
	c, err := p.VerifyCollection(ctx, f.ID)
	if errors.Is(err, payment.ErrNotFound) {
		c, err = payment.Collection{Status: payment.Pending}, nil
	}
	if err != nil {
		return f, err
	}
	return st.SettleFunding(ctx, f.ID, c)
}

type BillingArgs struct{}

func (BillingArgs) Kind() string { return "billing_round" }

func (BillingArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: MoneyQueue, MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{ByPeriod: billingEvery}}
}

type BillingWorker struct {
	river.WorkerDefaults[BillingArgs]
	Store  *store.Store
	Logger *slog.Logger
}

// Work charges monthly fees that are due, then settles checkouts and payouts
// whose webhook never arrived. One organisation's failure is logged and the
// round goes on; the next round retries it.
func (w *BillingWorker) Work(ctx context.Context, _ *river.Job[BillingArgs]) error {
	now := time.Now()
	orgs, err := w.Store.OrgsOwingFee(ctx, now)
	if err != nil {
		return err
	}
	for _, org := range orgs {
		if err := ChargeFee(ctx, w.Store, org, now); err != nil {
			w.Logger.ErrorContext(ctx, "monthly fee", "org", org, "err", err)
		}
	}
	pending, err := w.Store.PendingFundings(ctx, 10*time.Minute, 500)
	if err != nil {
		return err
	}
	for _, f := range pending {
		if _, err := ReconcileFunding(ctx, w.Store, f); err != nil {
			w.Logger.WarnContext(ctx, "funding check", "funding", f.ID, "err", err)
		}
	}
	return nil
}

// ChargeFee charges an organisation's monthly fee for now's month, once.
func ChargeFee(ctx context.Context, st *store.Store, orgID string, now time.Time) error {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := store.ChargeMonthlyFee(ctx, tx, orgID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
