package payment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Sandbox is a provider that collects and pays out at once and moves no
// real money. It is registered only in development and tests. It keeps no
// state of its own (the API and the worker may be different processes): it
// reads a funding's or withdrawal's amount through Amount, and amounts
// ending in 13 (GHS x.13) fail, so the failure path can be seen.
type Sandbox struct {
	// Amount returns what Payahub recorded for a funding ("collection") or
	// withdrawal ("payout"), or ErrNotFound.
	Amount func(ctx context.Context, kind, reference string) (int64, string, error)
}

func (s *Sandbox) Info() Info {
	return Info{ID: "sandbox", Name: "Sandbox payments", Currencies: []string{"GHS", "NGN", "KES", "USD"},
		Channels: []string{"card", "mobile_money"}, Payouts: true, Sandbox: true}
}

func fails(amount int64) bool { return amount%100 == 13 }

func (s *Sandbox) StartCheckout(_ context.Context, c Checkout) (CheckoutSession, error) {
	u := c.CallbackURL
	if u != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "reference=" + url.QueryEscape(c.Reference)
	}
	return CheckoutSession{URL: u, ProviderReference: "sbx_" + c.Reference}, nil
}

func (s *Sandbox) VerifyCollection(ctx context.Context, ref string) (Collection, error) {
	amount, cur, err := s.Amount(ctx, "collection", ref)
	if err != nil {
		return Collection{}, err
	}
	if fails(amount) {
		return Collection{Status: Failed, Currency: cur, ProviderReference: "sbx_" + ref,
			Message: "Declined by the sandbox (amounts ending in .13 fail)."}, nil
	}
	return Collection{Status: Succeeded, Amount: amount, Currency: cur, ProviderReference: "sbx_" + ref,
		PaidAt: time.Now().UTC()}, nil
}

func (s *Sandbox) AddRecipient(_ context.Context, d Destination) (string, error) {
	return "sbx_rcp_" + d.BankCode + "_" + d.AccountNumber, nil
}

func (s *Sandbox) SendPayout(ctx context.Context, p Payout) (PayoutResult, error) {
	return s.VerifyPayout(ctx, p.Reference)
}

func (s *Sandbox) VerifyPayout(ctx context.Context, ref string) (PayoutResult, error) {
	amount, _, err := s.Amount(ctx, "payout", ref)
	if err != nil {
		return PayoutResult{}, err
	}
	if fails(amount) {
		return PayoutResult{Status: Failed, ProviderReference: "sbx_trf_" + ref,
			Message: "Refused by the sandbox (amounts ending in .13 fail)."}, nil
	}
	return PayoutResult{Status: Succeeded, ProviderReference: "sbx_trf_" + ref}, nil
}

func (s *Sandbox) Banks(context.Context, string) ([]Bank, error) {
	return []Bank{
		{Code: "SBX-BANK", Name: "Sandbox Bank", Type: "bank"},
		{Code: "MTN", Name: "MTN Mobile Money", Type: "mobile_money"},
		{Code: "VOD", Name: "Telecel Cash", Type: "mobile_money"},
		{Code: "ATL", Name: "AirtelTigo Money", Type: "mobile_money"},
	}, nil
}

// ParseWebhook accepts {"kind":"collection","reference":"..."} unsigned; it
// only prompts a check with VerifyCollection, which reads Payahub's own record.
func (s *Sandbox) ParseWebhook(_ http.Header, body []byte) (Event, bool, error) {
	var e struct{ Kind, Reference string }
	if err := json.Unmarshal(body, &e); err != nil {
		return Event{}, false, err
	}
	return Event{Kind: e.Kind, Reference: e.Reference}, e.Reference != "", nil
}
