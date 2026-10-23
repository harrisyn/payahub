package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/onboarding"
	"github.com/harrisyn/payahub/internal/payment"
)

// Wallet plans pay from the organisation's wallet: a monthly fee at the
// start of each month (or when the plan is chosen) and a charge for each
// live claim a payer acknowledges. Both are ledger postings keyed so they
// happen once.

func orgPlan(ctx context.Context, q Querier, orgID string) (onboarding.Plan, error) {
	var p onboarding.Plan
	err := q.QueryRow(ctx, `SELECT plan FROM organizations WHERE id = $1`, orgID).Scan(&p)
	return p, err
}

// chargeClaim debits the claim's price from the organisation's wallet when a
// live claim first becomes billable. Called by SaveClaim in the same
// transaction as the state change, so a claim is charged exactly when, and
// only if, it is acknowledged.
func chargeClaim(ctx context.Context, tx pgx.Tx, c *claim.Claim) error {
	plan, err := orgPlan(ctx, tx, c.OrganizationID)
	if err != nil {
		return err
	}
	price, ok := onboarding.Prices[plan]
	if !ok || price.PerClaim == 0 {
		return nil // trial, enterprise, or flat (its fee covers claims, fair use)
	}
	cur := onboarding.BillingCurrency
	_, _, err = Post(ctx, tx, Posting{
		Key: "claim:" + c.ID, Kind: "charge", OrgID: c.OrganizationID,
		Memo:   claimMemo(c),
		Detail: map[string]any{"claim_id": c.ID, "payer_id": c.PayerID, "plan": plan},
		Legs: []Leg{
			{Account: WalletAccount(c.OrganizationID, cur), Amount: -price.PerClaim},
			{Account: PlatformAccount(AcctRevenue, "", cur), Amount: price.PerClaim},
		},
	})
	return err
}

// claimMemo says which claim a charge is for in the clinic's terms: the
// payer and the member.
func claimMemo(c *claim.Claim) string {
	payer := c.PayerID
	if p, ok := catalog.LookupPayer(c.PayerID); ok {
		payer = p.Name
	}
	return payer + ", member " + c.Member.MemberNumber
}

// ChargeMonthlyFee charges this month's fee for the organisation's plan, less
// any fee already charged this month (moving from flat + volume to flat
// costs the difference). It may leave the wallet below zero; live claims then
// wait until it is funded.
func ChargeMonthlyFee(ctx context.Context, tx pgx.Tx, orgID string, now time.Time) (int64, error) {
	plan, err := orgPlan(ctx, tx, orgID)
	if err != nil {
		return 0, err
	}
	price := onboarding.Prices[plan]
	if price.MonthlyFee == 0 {
		return 0, nil
	}
	month := now.UTC().Format("2006-01")
	var paid int64
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(sum(-e.amount), 0) FROM ledger_transactions t JOIN ledger_entries e ON e.transaction_id = t.id
		WHERE t.organization_id = $1 AND t.kind = 'fee' AND t.detail->>'month' = $2 AND e.account_id = $3`,
		orgID, month, WalletAccount(orgID, onboarding.BillingCurrency).ID).Scan(&paid); err != nil {
		return 0, err
	}
	due := price.MonthlyFee - paid
	if due <= 0 {
		return 0, nil
	}
	cur := onboarding.BillingCurrency
	_, posted, err := Post(ctx, tx, Posting{
		Key: fmt.Sprintf("fee:%s:%s:%s", orgID, month, plan), Kind: "fee", OrgID: orgID,
		Memo:   fmt.Sprintf("%s plan, %s", plan, now.UTC().Format("January 2006")),
		Detail: map[string]any{"month": month, "plan": plan},
		Legs: []Leg{
			{Account: WalletAccount(orgID, cur), Amount: -due},
			{Account: PlatformAccount(AcctRevenue, "", cur), Amount: due},
		},
	})
	if !posted {
		due = 0
	}
	return due, err
}

// OrgsOwingFee lists organisations on a plan with a monthly fee that have not
// been charged it this month.
func (s *Store) OrgsOwingFee(ctx context.Context, now time.Time) ([]string, error) {
	var plans []string
	for p, price := range onboarding.Prices {
		if price.MonthlyFee > 0 {
			plans = append(plans, string(p))
		}
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT o.id FROM organizations o
		WHERE o.plan = ANY($1) AND NOT EXISTS (
			SELECT 1 FROM ledger_transactions t
			WHERE t.organization_id = o.id AND t.kind = 'fee' AND t.detail->>'month' = $2 AND t.detail->>'plan' = o.plan)`,
		plans, now.UTC().Format("2006-01"))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// reserved is what the organisation's in-flight live claims will cost once
// acknowledged: queued or sent, not yet charged.
func reserved(ctx context.Context, q Querier, orgID string, perClaim int64) (int64, error) {
	if perClaim == 0 {
		return 0, nil
	}
	var n int64
	err := q.QueryRow(ctx, `
		SELECT count(*) FROM claims
		WHERE organization_id = $1 AND environment = 'live'
		  AND (submission_queued OR state = 'submitted')`, orgID).Scan(&n)
	return n * perClaim, err
}

// Live-sending refusals.
var (
	ErrTrialLimit  = errors.New("the trial has no live claims left")
	ErrWalletEmpty = errors.New("the wallet does not cover another live claim")
)

// MayGoLive reports whether an organisation may send one more live claim:
// a trial until it ends or uses its live claims; a wallet plan while the
// wallet, less what claims in flight will cost, covers one more; enterprise
// always. It returns nil, ErrTrialLimit or ErrWalletEmpty.
func MayGoLive(ctx context.Context, q Querier, orgID string) error {
	var plan onboarding.Plan
	var ends *time.Time
	var sent int
	err := q.QueryRow(ctx, `
		SELECT o.plan, o.trial_ends_at,
		       (SELECT count(*) FROM claims c WHERE c.organization_id = o.id AND c.environment = 'live'
		        AND c.state NOT IN ('draft', 'mapped', 'validated', 'needs_review', 'ready'))
		FROM organizations o WHERE o.id = $1`, orgID).Scan(&plan, &ends, &sent)
	if err != nil {
		return err
	}
	switch {
	case plan == onboarding.PlanEnterprise:
		return nil
	case plan.WalletFunded():
		price := onboarding.Prices[plan]
		bal, err := WalletBalance(ctx, q, orgID, onboarding.BillingCurrency)
		if err != nil {
			return err
		}
		held, err := reserved(ctx, q, orgID, price.PerClaim)
		if err != nil {
			return err
		}
		if bal-held < price.PerClaim || bal < 0 {
			return ErrWalletEmpty
		}
		return nil
	default: // trial, and enterprise until the contract is signed
		if ends != nil && time.Now().Before(*ends) && sent < onboarding.TrialLiveClaims {
			return nil
		}
		return ErrTrialLimit
	}
}

// Wallet is an organisation's wallet as the console shows it.
type Wallet struct {
	Currency          string            `json:"currency"`
	Balance           int64             `json:"balance"`
	Reserved          int64             `json:"reserved"`  // for live claims in flight
	Available         int64             `json:"available"` // balance less reserved and never below zero
	PendingWithdrawal int64             `json:"pending_withdrawals"`
	Plan              onboarding.Plan   `json:"plan"`
	Price             *onboarding.Price `json:"price,omitempty"`
	ChargedThisMonth  int64             `json:"charged_this_month"`
	CanSendLive       bool              `json:"can_send_live"`
}

func (s *Store) Wallet(ctx context.Context, orgID string) (Wallet, error) {
	cur := onboarding.BillingCurrency
	w := Wallet{Currency: cur}
	var err error
	if w.Plan, err = orgPlan(ctx, s.Pool, orgID); err != nil {
		return w, err
	}
	if p, ok := onboarding.Prices[w.Plan]; ok {
		w.Price = &p
	}
	if w.Balance, err = WalletBalance(ctx, s.Pool, orgID, cur); err != nil {
		return w, err
	}
	if w.Price != nil {
		if w.Reserved, err = reserved(ctx, s.Pool, orgID, w.Price.PerClaim); err != nil {
			return w, err
		}
	}
	w.Available = max(0, w.Balance-w.Reserved)
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT sum(amount) FROM wallet_withdrawals WHERE organization_id = $1 AND status IN ('requested', 'approved', 'paying')), 0),
		       COALESCE((SELECT sum(-e.amount) FROM ledger_entries e JOIN ledger_transactions t ON t.id = e.transaction_id
		                 WHERE e.account_id = $2 AND t.kind IN ('charge', 'fee') AND e.created_at >= $3), 0)`,
		orgID, WalletAccount(orgID, cur).ID, start).Scan(&w.PendingWithdrawal, &w.ChargedThisMonth); err != nil {
		return w, err
	}
	w.CanSendLive = MayGoLive(ctx, s.Pool, orgID) == nil
	return w, nil
}

// WalletEntry is one line of a wallet statement.
type WalletEntry struct {
	Seq           int64     `json:"seq"`
	TransactionID string    `json:"transaction_id"`
	Kind          string    `json:"kind"`
	Memo          string    `json:"memo"`
	Amount        int64     `json:"amount"`
	BalanceAfter  int64     `json:"balance_after"`
	Reference     string    `json:"reference,omitempty"` // the claim, funding or withdrawal it is for
	CreatedAt     time.Time `json:"created_at"`
}

// WalletStatement lists the wallet's entries, newest first, after the
// cursor (an entry's seq, exclusive) when one is given.
func (s *Store) WalletStatement(ctx context.Context, orgID string, before int64, limit int) ([]WalletEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT e.seq, t.id, t.kind, t.memo, e.amount, e.balance_after,
		       COALESCE(t.detail->>'claim_id', t.detail->>'funding_id', t.detail->>'withdrawal_id', ''), e.created_at
		FROM ledger_entries e JOIN ledger_transactions t ON t.id = e.transaction_id
		WHERE e.account_id = $1 AND ($2 = 0 OR e.seq < $2)
		ORDER BY e.seq DESC LIMIT $3`,
		WalletAccount(orgID, onboarding.BillingCurrency).ID, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (WalletEntry, error) {
		var e WalletEntry
		return e, r.Scan(&e.Seq, &e.TransactionID, &e.Kind, &e.Memo, &e.Amount, &e.BalanceAfter, &e.Reference, &e.CreatedAt)
	})
}

// Funding is one payment into a wallet.
type Funding struct {
	ID                string         `json:"id"`
	OrganizationID    string         `json:"-"`
	Provider          string         `json:"provider"`
	Amount            int64          `json:"amount"`
	Currency          string         `json:"currency"`
	Status            payment.Status `json:"status"`
	PaidAmount        *int64         `json:"paid_amount,omitempty"`
	Fee               *int64         `json:"fee,omitempty"`
	ProviderReference *string        `json:"provider_reference,omitempty"`
	CheckoutURL       *string        `json:"checkout_url,omitempty"`
	Message           *string        `json:"message,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	CompletedAt       *time.Time     `json:"completed_at,omitempty"`
}

const fundingCols = `id, organization_id, provider, amount, currency, status, paid_amount, fee, provider_reference,
	checkout_url, message, created_at, completed_at`

func scanFunding(r pgx.Row) (Funding, error) {
	var f Funding
	err := r.Scan(&f.ID, &f.OrganizationID, &f.Provider, &f.Amount, &f.Currency, &f.Status, &f.PaidAmount, &f.Fee,
		&f.ProviderReference, &f.CheckoutURL, &f.Message, &f.CreatedAt, &f.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	return f, err
}

// CreateFunding records a checkout before the provider is called. With an
// idempotency key, a repeat returns the first funding and false.
func (s *Store) CreateFunding(ctx context.Context, orgID, provider string, amount int64, currency, idemKey, by string) (Funding, bool, error) {
	id := NewID("fnd")
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO wallet_fundings (id, organization_id, provider, amount, currency, idempotency_key, created_by)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''))
		ON CONFLICT (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		id, orgID, provider, amount, currency, idemKey, by)
	if err != nil {
		return Funding{}, false, err
	}
	if tag.RowsAffected() == 0 {
		f, err := scanFunding(s.Pool.QueryRow(ctx, `SELECT `+fundingCols+` FROM wallet_fundings
			WHERE organization_id = $1 AND idempotency_key = $2`, orgID, idemKey))
		return f, false, err
	}
	f, err := s.GetFunding(ctx, orgID, id)
	return f, true, err
}

func (s *Store) SetCheckout(ctx context.Context, id, url, providerRef string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE wallet_fundings SET checkout_url = $2, provider_reference = NULLIF($3, ''), updated_at = now()
		WHERE id = $1`, id, url, providerRef)
	return err
}

// GetFunding reads one funding; orgID "" reads any (webhooks, operators).
func (s *Store) GetFunding(ctx context.Context, orgID, id string) (Funding, error) {
	return scanFunding(s.Pool.QueryRow(ctx, `SELECT `+fundingCols+` FROM wallet_fundings
		WHERE id = $1 AND ($2 = '' OR organization_id = $2)`, id, orgID))
}

func (s *Store) ListFundings(ctx context.Context, orgID string, limit int) ([]Funding, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+fundingCols+` FROM wallet_fundings WHERE organization_id = $1
		ORDER BY created_at DESC LIMIT $2`, orgID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Funding, error) { return scanFunding(r) })
}

// PendingFundings lists checkouts still open after `older`, oldest first, for
// the reconciler to ask their providers about.
func (s *Store) PendingFundings(ctx context.Context, older time.Duration, limit int) ([]Funding, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+fundingCols+` FROM wallet_fundings
		WHERE status = 'pending' AND created_at < now() - $1::interval ORDER BY created_at LIMIT $2`,
		fmt.Sprintf("%d seconds", int(older.Seconds())), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Funding, error) { return scanFunding(r) })
}

// AbandonAfter is how long an unpaid checkout stays open.
const AbandonAfter = 24 * time.Hour

// SettleFunding applies what the provider said about a funding. A success
// credits the wallet with what was paid (and books the provider's fee) once;
// a failure closes it; a checkout unpaid after AbandonAfter is abandoned.
// It returns the funding as it now stands.
func (s *Store) SettleFunding(ctx context.Context, id string, c payment.Collection) (Funding, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Funding{}, err
	}
	defer tx.Rollback(ctx)
	f, err := scanFunding(tx.QueryRow(ctx, `SELECT `+fundingCols+` FROM wallet_fundings WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return f, err
	}
	if f.Status.Final() {
		return f, nil
	}
	status := c.Status
	switch {
	case status == payment.Succeeded && c.Currency != f.Currency:
		status, c.Message = payment.Failed, fmt.Sprintf("paid in %s, expected %s; refer to an operator", c.Currency, f.Currency)
	case status == payment.Succeeded:
		cur := f.Currency
		legs := []Leg{
			{Account: WalletAccount(f.OrganizationID, cur), Amount: c.Amount},
			{Account: PlatformAccount(AcctCash, f.Provider, cur), Amount: -(c.Amount - c.Fee)},
		}
		if c.Fee > 0 {
			legs = append(legs, Leg{Account: PlatformAccount(AcctFees, f.Provider, cur), Amount: -c.Fee})
		}
		if c.Amount <= 0 || c.Fee < 0 || c.Fee >= c.Amount {
			return f, fmt.Errorf("funding %s: provider reported amount %d fee %d", id, c.Amount, c.Fee)
		}
		if _, _, err := Post(ctx, tx, Posting{
			Key: "funding:" + f.ID, Kind: "funding", OrgID: f.OrganizationID,
			Memo:   "Wallet funded through " + f.Provider,
			Detail: map[string]any{"funding_id": f.ID, "provider_reference": c.ProviderReference, "fee": c.Fee},
			Legs:   legs,
		}); err != nil {
			return f, err
		}
	case status == payment.Pending && time.Since(f.CreatedAt) > AbandonAfter:
		status = payment.Abandoned
	}
	if status == payment.Pending {
		return f, nil
	}
	_, err = tx.Exec(ctx, `
		UPDATE wallet_fundings SET status = $2, paid_amount = $3, fee = $4,
		       provider_reference = COALESCE(NULLIF($5, ''), provider_reference), message = NULLIF($6, ''),
		       updated_at = now(), completed_at = now()
		WHERE id = $1`, id, status, nilIfZero(c.Amount, status == payment.Succeeded), nilIfZero(c.Fee, status == payment.Succeeded),
		c.ProviderReference, c.Message)
	if err != nil {
		return f, err
	}
	if err := tx.Commit(ctx); err != nil {
		return f, err
	}
	return s.GetFunding(ctx, "", id)
}

func nilIfZero(n int64, keep bool) *int64 {
	if !keep {
		return nil
	}
	return &n
}

// PayoutAccount is a destination for withdrawals.
type PayoutAccount struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"-"`
	Provider       string    `json:"provider"`
	Type           string    `json:"type"`
	Name           string    `json:"name"`
	BankCode       string    `json:"bank_code"`
	BankName       string    `json:"bank_name"`
	AccountNumber  string    `json:"account_number"`
	Currency       string    `json:"currency"`
	RecipientCode  string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

const payoutCols = `id, organization_id, provider, type, name, bank_code, bank_name, account_number, currency, recipient_code, created_at`

func scanPayout(r pgx.Row) (PayoutAccount, error) {
	var a PayoutAccount
	err := r.Scan(&a.ID, &a.OrganizationID, &a.Provider, &a.Type, &a.Name, &a.BankCode, &a.BankName, &a.AccountNumber,
		&a.Currency, &a.RecipientCode, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) AddPayoutAccount(ctx context.Context, a PayoutAccount, by string) (PayoutAccount, error) {
	a.ID = NewID("pac")
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO payout_accounts (id, organization_id, provider, type, name, bank_code, bank_name, account_number,
		                             currency, recipient_code, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''))`,
		a.ID, a.OrganizationID, a.Provider, a.Type, a.Name, a.BankCode, a.BankName, a.AccountNumber, a.Currency, a.RecipientCode, by)
	if err != nil {
		return a, err
	}
	return s.GetPayoutAccount(ctx, a.OrganizationID, a.ID)
}

func (s *Store) GetPayoutAccount(ctx context.Context, orgID, id string) (PayoutAccount, error) {
	return scanPayout(s.Pool.QueryRow(ctx, `SELECT `+payoutCols+` FROM payout_accounts
		WHERE id = $1 AND organization_id = $2 AND removed_at IS NULL`, id, orgID))
}

func (s *Store) ListPayoutAccounts(ctx context.Context, orgID string) ([]PayoutAccount, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+payoutCols+` FROM payout_accounts
		WHERE organization_id = $1 AND removed_at IS NULL ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (PayoutAccount, error) { return scanPayout(r) })
}

func (s *Store) RemovePayoutAccount(ctx context.Context, orgID, id string) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE payout_accounts SET removed_at = now()
		WHERE id = $1 AND organization_id = $2 AND removed_at IS NULL`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Withdrawal is money leaving a wallet for a payout account.
type Withdrawal struct {
	ID                string     `json:"id"`
	OrganizationID    string     `json:"organization_id"`
	OrganizationName  string     `json:"organization_name,omitempty"`
	PayoutAccountID   string     `json:"payout_account_id"`
	Destination       string     `json:"destination"` // bank or network and the account, for people
	Provider          string     `json:"provider"`
	Amount            int64      `json:"amount"`
	Currency          string     `json:"currency"`
	Status            string     `json:"status"` // requested | approved | paying | paid | failed | rejected
	Reason            string     `json:"reason"`
	ProviderReference *string    `json:"provider_reference,omitempty"`
	Message           *string    `json:"message,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	DecidedAt         *time.Time `json:"decided_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	recipient         string
}

// Recipient is the provider's code for the destination; for the payout worker.
func (w Withdrawal) Recipient() string { return w.recipient }

const withdrawalSelect = `
	SELECT w.id, w.organization_id, o.name, w.payout_account_id,
	       COALESCE(NULLIF(p.bank_name, ''), p.bank_code) || ' ' || p.account_number || ' (' || p.name || ')',
	       w.provider, w.amount, w.currency, w.status, w.reason, w.provider_reference, w.message,
	       w.created_at, w.decided_at, w.completed_at, p.recipient_code
	FROM wallet_withdrawals w JOIN organizations o ON o.id = w.organization_id
	JOIN payout_accounts p ON p.id = w.payout_account_id`

func scanWithdrawal(r pgx.Row) (Withdrawal, error) {
	var w Withdrawal
	err := r.Scan(&w.ID, &w.OrganizationID, &w.OrganizationName, &w.PayoutAccountID, &w.Destination, &w.Provider, &w.Amount,
		&w.Currency, &w.Status, &w.Reason, &w.ProviderReference, &w.Message, &w.CreatedAt, &w.DecidedAt, &w.CompletedAt, &w.recipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// RequestWithdrawal holds the amount in the wallet and records the request
// for an operator. The wallet keeps what its live claims in flight will cost.
func (s *Store) RequestWithdrawal(ctx context.Context, orgID, payoutAccountID string, amount int64, reason, idemKey, by string) (Withdrawal, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Withdrawal{}, false, err
	}
	defer tx.Rollback(ctx)
	if idemKey != "" {
		var id string
		err := tx.QueryRow(ctx, `SELECT id FROM wallet_withdrawals WHERE organization_id = $1 AND idempotency_key = $2`, orgID, idemKey).Scan(&id)
		if err == nil {
			w, err := s.GetWithdrawal(ctx, orgID, id)
			return w, false, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Withdrawal{}, false, err
		}
	}
	acct, err := scanPayout(tx.QueryRow(ctx, `SELECT `+payoutCols+` FROM payout_accounts
		WHERE id = $1 AND organization_id = $2 AND removed_at IS NULL`, payoutAccountID, orgID))
	if err != nil {
		return Withdrawal{}, false, err
	}
	plan, err := orgPlan(ctx, tx, orgID)
	if err != nil {
		return Withdrawal{}, false, err
	}
	floor, err := reserved(ctx, tx, orgID, onboarding.Prices[plan].PerClaim)
	if err != nil {
		return Withdrawal{}, false, err
	}
	id := NewID("wdr")
	if _, err := tx.Exec(ctx, `
		INSERT INTO wallet_withdrawals (id, organization_id, payout_account_id, provider, amount, currency, reason,
		                                idempotency_key, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''))`,
		id, orgID, acct.ID, acct.Provider, amount, acct.Currency, reason, idemKey, by); err != nil {
		return Withdrawal{}, false, err
	}
	if _, _, err := Post(ctx, tx, Posting{
		Key: "withdrawal:" + id, Kind: "withdrawal", OrgID: orgID, By: by,
		Memo:   "Withdrawal to " + acct.BankName + " " + acct.AccountNumber,
		Detail: map[string]any{"withdrawal_id": id},
		Legs: []Leg{
			{Account: WalletAccount(orgID, acct.Currency), Amount: -amount, Floor: &floor},
			{Account: PlatformAccount(AcctPayoutsPending, acct.Provider, acct.Currency), Amount: amount},
		},
	}); err != nil {
		return Withdrawal{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Withdrawal{}, false, err
	}
	w, err := s.GetWithdrawal(ctx, orgID, id)
	return w, true, err
}

// GetWithdrawal reads one; orgID "" reads any.
func (s *Store) GetWithdrawal(ctx context.Context, orgID, id string) (Withdrawal, error) {
	return scanWithdrawal(s.Pool.QueryRow(ctx, withdrawalSelect+` WHERE w.id = $1 AND ($2 = '' OR w.organization_id = $2)`, id, orgID))
}

// ListWithdrawals lists an organisation's withdrawals (orgID set) or, for
// operators, every withdrawal in the given statuses.
func (s *Store) ListWithdrawals(ctx context.Context, orgID string, statuses []string, limit int) ([]Withdrawal, error) {
	rows, err := s.Pool.Query(ctx, withdrawalSelect+`
		WHERE ($1 = '' OR w.organization_id = $1) AND (COALESCE(cardinality($2::text[]), 0) = 0 OR w.status = ANY($2))
		ORDER BY w.created_at DESC LIMIT $3`, orgID, statuses, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Withdrawal, error) { return scanWithdrawal(r) })
}

// ErrWrongState is returned when a withdrawal is not in a state the action applies to.
var ErrWrongState = errors.New("the withdrawal is not waiting for this")

// LockWithdrawal reads a withdrawal for update in tx.
func LockWithdrawal(ctx context.Context, tx pgx.Tx, id string) (Withdrawal, error) {
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM wallet_withdrawals WHERE id = $1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Withdrawal{}, ErrNotFound
		}
		return Withdrawal{}, err
	}
	return scanWithdrawal(tx.QueryRow(ctx, withdrawalSelect+` WHERE w.id = $1`, id))
}

// ApproveWithdrawal marks a requested withdrawal approved in tx; the caller
// inserts the payout job in the same transaction.
func ApproveWithdrawal(ctx context.Context, tx pgx.Tx, id, by string) (Withdrawal, error) {
	w, err := LockWithdrawal(ctx, tx, id)
	if err != nil {
		return w, err
	}
	if w.Status != "requested" {
		return w, ErrWrongState
	}
	_, err = tx.Exec(ctx, `UPDATE wallet_withdrawals SET status = 'approved', decided_by = $2, decided_at = now(), updated_at = now()
		WHERE id = $1`, id, by)
	w.Status = "approved"
	return w, err
}

// ReturnWithdrawal puts a withdrawal's amount back in the wallet and closes
// it as rejected (by an operator) or failed (by the provider).
func ReturnWithdrawal(ctx context.Context, tx pgx.Tx, w Withdrawal, status, message, by string) error {
	if _, _, err := Post(ctx, tx, Posting{
		Key: "withdrawal_returned:" + w.ID, Kind: "withdrawal_returned", OrgID: w.OrganizationID, By: by,
		Memo:   "Withdrawal " + status + ", returned to the wallet",
		Detail: map[string]any{"withdrawal_id": w.ID, "message": message},
		Legs: []Leg{
			{Account: PlatformAccount(AcctPayoutsPending, w.Provider, w.Currency), Amount: -w.Amount},
			{Account: WalletAccount(w.OrganizationID, w.Currency), Amount: w.Amount},
		},
	}); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE wallet_withdrawals SET status = $2, message = NULLIF($3, ''), decided_by = COALESCE(decided_by, NULLIF($4, '')),
		decided_at = COALESCE(decided_at, now()), updated_at = now(), completed_at = now() WHERE id = $1`, w.ID, status, message, by)
	return err
}

// MarkPaying records that the payout was sent to the provider.
func MarkPaying(ctx context.Context, tx pgx.Tx, id, providerRef, message string) error {
	_, err := tx.Exec(ctx, `UPDATE wallet_withdrawals SET status = 'paying', provider_reference = COALESCE(NULLIF($2, ''), provider_reference),
		message = NULLIF($3, ''), updated_at = now() WHERE id = $1`, id, providerRef, message)
	return err
}

// SettleWithdrawal books a paid withdrawal: the held amount leaves the
// provider's cash.
func SettleWithdrawal(ctx context.Context, tx pgx.Tx, w Withdrawal, providerRef string) error {
	if _, _, err := Post(ctx, tx, Posting{
		Key: "withdrawal_paid:" + w.ID, Kind: "withdrawal_paid", OrgID: w.OrganizationID,
		Memo:   "Withdrawal paid",
		Detail: map[string]any{"withdrawal_id": w.ID, "provider_reference": providerRef},
		Legs: []Leg{
			{Account: PlatformAccount(AcctPayoutsPending, w.Provider, w.Currency), Amount: -w.Amount},
			{Account: PlatformAccount(AcctCash, w.Provider, w.Currency), Amount: w.Amount},
		},
	}); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE wallet_withdrawals SET status = 'paid', provider_reference = COALESCE(NULLIF($2, ''), provider_reference),
		message = NULL, updated_at = now(), completed_at = now() WHERE id = $1`, w.ID, providerRef)
	return err
}

// Adjust credits (positive) or debits an organisation's wallet by an
// operator's decision, against the platform's adjustments account.
func (s *Store) Adjust(ctx context.Context, orgID string, amount int64, memo, key, by string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id = $1)`, orgID).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, ErrNotFound
	}
	cur := onboarding.BillingCurrency
	_, posted, err := Post(ctx, tx, Posting{
		Key: "adjustment:" + key, Kind: "adjustment", OrgID: orgID, Memo: memo, By: by,
		Legs: []Leg{
			{Account: WalletAccount(orgID, cur), Amount: amount},
			{Account: PlatformAccount(AcctAdjustments, "", cur), Amount: -amount},
		},
	})
	if err != nil {
		return false, err
	}
	return posted, tx.Commit(ctx)
}

// UserEmail is the address a provider's receipt goes to: the signed-in user's,
// or the organisation's first user's for API keys.
func (s *Store) UserEmail(ctx context.Context, orgID, userID string) (string, error) {
	var email string
	err := s.Pool.QueryRow(ctx, `SELECT email FROM users WHERE organization_id = $1
		ORDER BY (id = $2) DESC, created_at LIMIT 1`, orgID, userID).Scan(&email)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return email, err
}

// SandboxAmount gives the sandbox payment provider the amount Payahub
// recorded for a funding or a withdrawal.
func (s *Store) SandboxAmount(ctx context.Context, kind, ref string) (int64, string, error) {
	table := "wallet_fundings"
	if kind == "payout" {
		table = "wallet_withdrawals"
	}
	var amount int64
	var cur string
	err := s.Pool.QueryRow(ctx, `SELECT amount, currency FROM `+table+` WHERE id = $1`, ref).Scan(&amount, &cur)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", payment.ErrNotFound
	}
	return amount, cur, err
}
