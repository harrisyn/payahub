// Package payment moves money between the outside world and Payahub's
// wallets. A provider (Paystack, a mobile money aggregator, a bank) only
// collects funds into a wallet and pays withdrawals out of one; what the
// money is for is the ledger's business, never the provider's. Providers
// register here the way connectors do, and the API offers the ones that are
// configured.
package payment

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Status is where a payment stands at the provider.
type Status string

const (
	Pending   Status = "pending"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	// Abandoned is a checkout the payer never finished.
	Abandoned Status = "abandoned"
)

// Final reports whether the status can no longer change.
func (s Status) Final() bool { return s == Succeeded || s == Failed || s == Abandoned }

// Checkout asks the provider to collect Amount (minor units) from the
// organisation. Reference is Payahub's id for the funding; providers that
// take a merchant reference use it, so a repeat is recognised.
type Checkout struct {
	Reference   string
	Amount      int64
	Currency    string
	Email       string // the payer's receipt address, where the provider needs one
	CallbackURL string // where the provider sends the browser afterwards
}

type CheckoutSession struct {
	URL               string // the provider's hosted page
	ProviderReference string
}

// Collection is what the provider says a checkout collected. Amount is what
// was actually paid; Fee is the provider's charge on it, when known.
type Collection struct {
	Status            Status
	Amount            int64
	Fee               int64
	Currency          string
	ProviderReference string
	PaidAt            time.Time
	Message           string
}

// Destination is where a withdrawal goes: a bank account or a mobile money
// wallet.
type Destination struct {
	Type          string `json:"type"` // bank | mobile_money
	Name          string `json:"name"`
	BankCode      string `json:"bank_code"` // the bank, or the mobile network
	AccountNumber string `json:"account_number"`
	Currency      string `json:"currency"`
}

// Payout pays Amount from the platform's provider balance to a destination.
// Reference is Payahub's withdrawal id and must make a repeat a no-op.
type Payout struct {
	Reference string
	Amount    int64
	Currency  string
	Recipient string // the provider's code for the destination, from AddRecipient
	Reason    string
}

type PayoutResult struct {
	Status            Status
	ProviderReference string
	Message           string
}

// Event is a verified webhook. Kind is "collection" or "payout"; the
// reference is Payahub's. Webhooks only say something happened: callers
// confirm it with Verify before they move money.
type Event struct {
	Kind      string
	Reference string
}

// Bank is a bank or mobile network a provider can pay to.
type Bank struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Type string `json:"type"` // bank | mobile_money
}

// Provider is one payment integration.
type Provider interface {
	Info() Info
	StartCheckout(ctx context.Context, c Checkout) (CheckoutSession, error)
	// VerifyCollection asks the provider what happened to a checkout. It is
	// the only source the ledger posts a funding from.
	VerifyCollection(ctx context.Context, reference string) (Collection, error)
	// AddRecipient registers a destination and returns the provider's code for it.
	AddRecipient(ctx context.Context, d Destination) (string, error)
	SendPayout(ctx context.Context, p Payout) (PayoutResult, error)
	// VerifyPayout asks what happened to a payout; ErrNotFound when the
	// provider never received it, so it is safe to send.
	VerifyPayout(ctx context.Context, reference string) (PayoutResult, error)
	Banks(ctx context.Context, currency string) ([]Bank, error)
	// ParseWebhook checks the request's signature and reads its event.
	// ok is false for events that need no action.
	ParseWebhook(h http.Header, body []byte) (e Event, ok bool, err error)
}

type Info struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Currencies []string `json:"currencies"`
	Channels   []string `json:"channels"` // card, mobile_money, bank_transfer
	Payouts    bool     `json:"payouts"`
	Sandbox    bool     `json:"sandbox"`
}

var (
	ErrNotFound     = errors.New("payment: not found at the provider")
	ErrBadSignature = errors.New("payment: webhook signature does not match")
	ErrUnknown      = errors.New("payment: no such provider")
)

var (
	mu        sync.RWMutex
	providers = map[string]Provider{}
)

// Register makes a configured provider available. Configure them at start-up.
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	providers[p.Info().ID] = p
}

func Lookup(id string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[id]
	if !ok {
		return nil, ErrUnknown
	}
	return p, nil
}

// All lists the configured providers, real ones first.
func All() []Info {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Info, 0, len(providers))
	for _, p := range providers {
		out = append(out, p.Info())
	}
	slices.SortFunc(out, func(a, b Info) int {
		if a.Sandbox != b.Sandbox {
			if a.Sandbox {
				return 1
			}
			return -1
		}
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return out
}

// Reset removes every provider; for tests.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	providers = map[string]Provider{}
}
