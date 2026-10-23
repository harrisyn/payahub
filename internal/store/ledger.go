package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
)

// The ledger is double-entry: every movement of money is one transaction
// whose entries sum to zero, and a transaction's key makes it happen at most
// once (claim:<id> charges a claim once, funding:<id> credits a payment
// once). Amounts are minor units; an entry is a credit when positive.

var (
	ErrInsufficientFunds = errors.New("the wallet does not have enough money for this")
	ErrUnbalanced        = errors.New("ledger: entries do not sum to zero")
)

// Account kinds.
const (
	AcctWallet         = "wallet"          // an organisation's money with us
	AcctRevenue        = "revenue"         // the platform's wallet: what plans earn
	AcctCash           = "cash"            // collected and held at a payment provider
	AcctFees           = "fees"            // what providers charged us
	AcctPayoutsPending = "payouts_pending" // withdrawals held until they are paid
	AcctAdjustments    = "adjustments"     // operators' credits and corrections
)

type LedgerAccount struct {
	ID           string
	Owner        string // "platform" or an organisation id
	Kind         string
	Currency     string
	KeepsBalance bool
}

func WalletAccount(orgID, currency string) LedgerAccount {
	return LedgerAccount{ID: "org:" + orgID + ":wallet:" + currency, Owner: orgID, Kind: AcctWallet, Currency: currency, KeepsBalance: true}
}

// PlatformAccount names one of the platform's accounts; provider is set for
// cash, fees and payouts_pending, which are kept per payment provider.
func PlatformAccount(kind, provider, currency string) LedgerAccount {
	id := "platform:" + kind
	if provider != "" {
		id += ":" + provider
	}
	return LedgerAccount{ID: id + ":" + currency, Owner: "platform", Kind: kind, Currency: currency}
}

// Leg is one entry: Amount is added to the account's balance.
type Leg struct {
	Account LedgerAccount
	Amount  int64
	// Floor, when set on an account that keeps a balance, is the lowest the
	// balance may fall to; below it the posting fails with ErrInsufficientFunds.
	Floor *int64
}

type Posting struct {
	Key    string
	Kind   string
	OrgID  string
	Memo   string
	Detail any
	By     string
	Legs   []Leg
}

// Post writes p in tx. It returns false, and changes nothing, when a
// transaction with p.Key already exists.
func Post(ctx context.Context, tx pgx.Tx, p Posting) (string, bool, error) {
	var sum int64
	seen := map[string]bool{}
	for _, l := range p.Legs {
		if l.Amount == 0 || l.Account.Currency != p.Legs[0].Account.Currency || seen[l.Account.ID] {
			return "", false, fmt.Errorf("ledger: bad leg %s %d", l.Account.ID, l.Amount)
		}
		seen[l.Account.ID] = true
		sum += l.Amount
	}
	if len(p.Legs) < 2 || sum != 0 {
		return "", false, ErrUnbalanced
	}
	var detail []byte
	if p.Detail != nil {
		var err error
		if detail, err = json.Marshal(p.Detail); err != nil {
			return "", false, err
		}
	}
	id := NewID("ltx")
	now := time.Now().UTC()
	tag, err := tx.Exec(ctx, `
		INSERT INTO ledger_transactions (id, key, kind, organization_id, memo, detail, created_by, created_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8)
		ON CONFLICT (key) DO NOTHING`, id, p.Key, p.Kind, p.OrgID, p.Memo, detail, p.By, now)
	if err != nil {
		return "", false, err
	}
	if tag.RowsAffected() == 0 {
		err := tx.QueryRow(ctx, `SELECT id FROM ledger_transactions WHERE key = $1`, p.Key).Scan(&id)
		return id, false, err
	}
	legs := slices.Clone(p.Legs)
	// A fixed order, so two postings never wait on each other's rows.
	slices.SortFunc(legs, func(a, b Leg) int {
		if a.Account.ID < b.Account.ID {
			return -1
		}
		return 1
	})
	for _, l := range legs {
		a := l.Account
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_accounts (id, owner, kind, currency, keeps_balance) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO NOTHING`, a.ID, a.Owner, a.Kind, a.Currency, a.KeepsBalance); err != nil {
			return "", false, err
		}
		var after *int64
		if a.KeepsBalance {
			var b int64
			if err := tx.QueryRow(ctx, `UPDATE ledger_accounts SET balance = balance + $2 WHERE id = $1 RETURNING balance`,
				a.ID, l.Amount).Scan(&b); err != nil {
				return "", false, err
			}
			if l.Floor != nil && b < *l.Floor {
				return "", false, ErrInsufficientFunds
			}
			after = &b
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (transaction_id, account_id, amount, balance_after, created_at)
			VALUES ($1, $2, $3, $4, $5)`, id, a.ID, l.Amount, after, now); err != nil {
			return "", false, err
		}
	}
	return id, true, nil
}

// WalletBalance is the organisation's wallet balance; zero before its first entry.
func WalletBalance(ctx context.Context, q Querier, orgID, currency string) (int64, error) {
	var b int64
	err := q.QueryRow(ctx, `SELECT balance FROM ledger_accounts WHERE id = $1`, WalletAccount(orgID, currency).ID).Scan(&b)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return b, err
}

// AccountBalance is one platform account's balance, summed from its entries.
type AccountBalance struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Currency string `json:"currency"`
	Balance  int64  `json:"balance"`
}

func (s *Store) PlatformBalances(ctx context.Context) ([]AccountBalance, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT a.id, a.kind, a.currency, COALESCE((SELECT sum(e.amount) FROM ledger_entries e WHERE e.account_id = a.id), 0)
		FROM ledger_accounts a WHERE a.owner = 'platform' ORDER BY a.kind, a.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AccountBalance, error) {
		var b AccountBalance
		return b, r.Scan(&b.ID, &b.Kind, &b.Currency, &b.Balance)
	})
}

// WalletTotals sums every organisation's wallet by currency: what the
// platform owes its customers.
func (s *Store) WalletTotals(ctx context.Context) (map[string]int64, error) {
	rows, err := s.Pool.Query(ctx, `SELECT currency, sum(balance) FROM ledger_accounts WHERE kind = 'wallet' GROUP BY currency`)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	defer rows.Close()
	for rows.Next() {
		var c string
		var n int64
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// LedgerBalanced reports whether every transaction sums to zero and every
// kept balance equals its entries: the check an operator or a test runs.
func (s *Store) LedgerBalanced(ctx context.Context) (bool, error) {
	var bad int
	err := s.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM (SELECT transaction_id FROM ledger_entries GROUP BY transaction_id HAVING sum(amount) <> 0) t)
		     + (SELECT count(*) FROM ledger_accounts a WHERE a.keeps_balance AND a.balance <>
		          COALESCE((SELECT sum(amount) FROM ledger_entries e WHERE e.account_id = a.id), 0))`).Scan(&bad)
	return bad == 0, err
}
