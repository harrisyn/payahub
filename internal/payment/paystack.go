package payment

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Paystack collects by card and mobile money and pays out to banks and
// mobile money wallets (Ghana, Nigeria, Kenya, South Africa). One platform
// account takes every organisation's funding; the ledger says whose it is.
type Paystack struct {
	SecretKey string
	BaseURL   string // https://api.paystack.co unless testing
	Client    *http.Client
}

func NewPaystack(secretKey string) *Paystack {
	return &Paystack{SecretKey: secretKey, BaseURL: "https://api.paystack.co", Client: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 25 * time.Second,
		},
	}}
}

func (p *Paystack) Info() Info {
	return Info{ID: "paystack", Name: "Paystack", Currencies: []string{"GHS", "NGN", "KES", "ZAR", "USD"},
		Channels: []string{"card", "mobile_money", "bank_transfer"}, Payouts: true,
		Sandbox: strings.HasPrefix(p.SecretKey, "sk_test_")}
}

// Paystack references allow letters, digits, '-', '.' and '='. Payahub ids
// are prefix_uuid, so the one underscore becomes a dash and back.
func paystackRef(id string) string { return strings.Replace(id, "_", "-", 1) }
func payahubRef(ref string) string { return strings.Replace(ref, "-", "_", 1) }

type paystackReply struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// paystackError is a reply Paystack refused; Status is the HTTP status.
type paystackError struct {
	Status  int
	Message string
}

func (e *paystackError) Error() string {
	return fmt.Sprintf("paystack: HTTP %d: %s", e.Status, e.Message)
}

func (p *Paystack) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.Client.Do(req)
	if err != nil {
		return fmt.Errorf("paystack: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("paystack: reading reply: %w", err)
	}
	var r paystackReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return &paystackError{Status: res.StatusCode, Message: strings.TrimSpace(string(raw[:min(len(raw), 200)]))}
	}
	if res.StatusCode/100 != 2 || !r.Status {
		return &paystackError{Status: res.StatusCode, Message: r.Message}
	}
	if out != nil {
		return json.Unmarshal(r.Data, out)
	}
	return nil
}

func notFound(err error) bool {
	pe, ok := err.(*paystackError)
	return ok && (pe.Status == http.StatusNotFound ||
		(pe.Status == http.StatusBadRequest && strings.Contains(strings.ToLower(pe.Message), "not found")))
}

func (p *Paystack) StartCheckout(ctx context.Context, c Checkout) (CheckoutSession, error) {
	var d struct {
		AuthorizationURL string `json:"authorization_url"`
		Reference        string `json:"reference"`
	}
	err := p.call(ctx, http.MethodPost, "/transaction/initialize", map[string]any{
		"email": c.Email, "amount": c.Amount, "currency": c.Currency,
		"reference": paystackRef(c.Reference), "callback_url": c.CallbackURL,
		"metadata": map[string]string{"payahub_funding": c.Reference},
	}, &d)
	if err != nil {
		return CheckoutSession{}, err
	}
	return CheckoutSession{URL: d.AuthorizationURL, ProviderReference: d.Reference}, nil
}

func (p *Paystack) VerifyCollection(ctx context.Context, ref string) (Collection, error) {
	var d struct {
		ID              int64  `json:"id"`
		Status          string `json:"status"`
		Amount          int64  `json:"amount"`
		Fees            int64  `json:"fees"`
		Currency        string `json:"currency"`
		PaidAt          string `json:"paid_at"`
		GatewayResponse string `json:"gateway_response"`
	}
	err := p.call(ctx, http.MethodGet, "/transaction/verify/"+url.PathEscape(paystackRef(ref)), nil, &d)
	if notFound(err) {
		return Collection{}, ErrNotFound
	}
	if err != nil {
		return Collection{}, err
	}
	c := Collection{Amount: d.Amount, Fee: d.Fees, Currency: strings.ToUpper(d.Currency),
		ProviderReference: fmt.Sprint(d.ID), Message: d.GatewayResponse}
	switch d.Status {
	case "success":
		c.Status = Succeeded
		c.PaidAt, _ = time.Parse(time.RFC3339, d.PaidAt)
	case "failed", "reversed":
		c.Status = Failed
	case "abandoned":
		// Paystack marks a checkout abandoned until it is paid, and it can
		// still be paid; the ledger decides when to give up on it.
		c.Status = Pending
	default: // ongoing, pending, processing, queued
		c.Status = Pending
	}
	return c, nil
}

func (p *Paystack) AddRecipient(ctx context.Context, d Destination) (string, error) {
	typ := d.Type
	switch {
	case typ == "mobile_money":
	case d.Currency == "GHS":
		typ = "ghipss"
	case d.Currency == "NGN":
		typ = "nuban"
	case d.Currency == "ZAR":
		typ = "basa"
	default:
		typ = "authorization"
	}
	var out struct {
		RecipientCode string `json:"recipient_code"`
	}
	err := p.call(ctx, http.MethodPost, "/transferrecipient", map[string]any{
		"type": typ, "name": d.Name, "account_number": d.AccountNumber, "bank_code": d.BankCode, "currency": d.Currency,
	}, &out)
	return out.RecipientCode, err
}

func transferStatus(s, reason string) PayoutResult {
	switch s {
	case "success":
		return PayoutResult{Status: Succeeded}
	case "failed", "reversed", "abandoned", "rejected", "blocked":
		return PayoutResult{Status: Failed, Message: reason}
	case "otp":
		return PayoutResult{Status: Pending, Message: "Paystack is asking for an OTP; turn off OTP for transfers on the Paystack dashboard."}
	default: // pending, processing, queued, received
		return PayoutResult{Status: Pending}
	}
}

func (p *Paystack) SendPayout(ctx context.Context, po Payout) (PayoutResult, error) {
	var d struct {
		Status       string `json:"status"`
		TransferCode string `json:"transfer_code"`
		Reason       string `json:"reason"`
	}
	err := p.call(ctx, http.MethodPost, "/transfer", map[string]any{
		"source": "balance", "amount": po.Amount, "currency": po.Currency, "recipient": po.Recipient,
		"reference": paystackRef(po.Reference), "reason": po.Reason,
	}, &d)
	if err != nil {
		return PayoutResult{}, err
	}
	r := transferStatus(d.Status, d.Reason)
	r.ProviderReference = d.TransferCode
	return r, nil
}

func (p *Paystack) VerifyPayout(ctx context.Context, ref string) (PayoutResult, error) {
	var d struct {
		Status       string `json:"status"`
		TransferCode string `json:"transfer_code"`
		Reason       string `json:"reason"`
	}
	err := p.call(ctx, http.MethodGet, "/transfer/verify/"+url.PathEscape(paystackRef(ref)), nil, &d)
	if notFound(err) {
		return PayoutResult{}, ErrNotFound
	}
	if err != nil {
		return PayoutResult{}, err
	}
	r := transferStatus(d.Status, d.Reason)
	r.ProviderReference = d.TransferCode
	return r, nil
}

func (p *Paystack) Banks(ctx context.Context, currency string) ([]Bank, error) {
	var d []struct {
		Name string `json:"name"`
		Code string `json:"code"`
		Type string `json:"type"`
	}
	q := url.Values{"currency": {currency}, "perPage": {"500"}}
	if err := p.call(ctx, http.MethodGet, "/bank?"+q.Encode(), nil, &d); err != nil {
		return nil, err
	}
	out := make([]Bank, 0, len(d))
	for _, b := range d {
		t := "bank"
		if b.Type == "mobile_money" {
			t = "mobile_money"
		}
		out = append(out, Bank{Code: b.Code, Name: b.Name, Type: t})
	}
	return out, nil
}

// ParseWebhook checks x-paystack-signature, the HMAC-SHA512 of the body
// under the secret key.
func (p *Paystack) ParseWebhook(h http.Header, body []byte) (Event, bool, error) {
	got, err := hex.DecodeString(h.Get("X-Paystack-Signature"))
	if err != nil || len(got) == 0 {
		return Event{}, false, ErrBadSignature
	}
	mac := hmac.New(sha512.New, []byte(p.SecretKey))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return Event{}, false, ErrBadSignature
	}
	var e struct {
		Event string `json:"event"`
		Data  struct {
			Reference string `json:"reference"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return Event{}, false, err
	}
	ref := payahubRef(e.Data.Reference)
	switch {
	case e.Event == "charge.success":
		return Event{Kind: "collection", Reference: ref}, ref != "", nil
	case strings.HasPrefix(e.Event, "transfer."):
		return Event{Kind: "payout", Reference: ref}, ref != "", nil
	}
	return Event{}, false, nil
}
