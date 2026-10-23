package api

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/harrisyn/payahub/internal/onboarding"
	"github.com/harrisyn/payahub/internal/payment"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

// Wallet routes. Every organisation pays for its plan from a wallet it funds
// through a payment provider and can withdraw from. Routes that change where
// money goes (payout accounts, withdrawals) need a console session.
func (s *Server) walletRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/payment-providers", s.auth(s.listProviders))
	mux.Handle("GET /v1/payment-providers/{id}/banks", s.auth(s.listBanks))
	mux.Handle("GET /v1/wallet", s.auth(s.getWallet))
	mux.Handle("GET /v1/wallet/transactions", s.auth(s.walletTransactions))
	mux.Handle("POST /v1/wallet/fundings", s.auth(s.createFunding))
	mux.Handle("GET /v1/wallet/fundings", s.auth(s.listFundings))
	mux.Handle("GET /v1/wallet/fundings/{id}", s.auth(s.getFunding))
	mux.Handle("GET /v1/wallet/payout-accounts", s.auth(s.listPayoutAccounts))
	mux.Handle("POST /v1/wallet/payout-accounts", s.session(s.addPayoutAccount))
	mux.Handle("DELETE /v1/wallet/payout-accounts/{id}", s.session(s.removePayoutAccount))
	mux.Handle("GET /v1/wallet/withdrawals", s.auth(s.listWithdrawals))
	mux.Handle("POST /v1/wallet/withdrawals", s.session(s.requestWithdrawal))
	mux.HandleFunc("POST /v1/webhooks/payments/{provider}", s.paymentWebhook)

	mux.Handle("GET /v1/operator/withdrawals", s.operator(s.opListWithdrawals))
	mux.Handle("POST /v1/operator/withdrawals/{id}/approve", s.operator(s.opApproveWithdrawal))
	mux.Handle("POST /v1/operator/withdrawals/{id}/reject", s.operator(s.opRejectWithdrawal))
	mux.Handle("POST /v1/operator/wallets/{org}/adjustments", s.operator(s.opAdjust))
	mux.Handle("GET /v1/operator/ledger", s.operator(s.opLedger))
}

const (
	minFunding    = 100        // GHS 1.00
	maxFunding    = 10_000_000 // GHS 100,000.00 per checkout
	minWithdrawal = 100
)

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": payment.All()})
}

func (s *Server) listBanks(w http.ResponseWriter, r *http.Request) {
	p, err := payment.Lookup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "No payment provider "+r.PathValue("id")+".", nil)
		return
	}
	cur := strings.ToUpper(r.URL.Query().Get("currency"))
	if cur == "" {
		cur = onboarding.BillingCurrency
	}
	banks, err := p.Banks(r.Context(), cur)
	if err != nil {
		writeError(w, http.StatusBadGateway, "provider_unavailable", "The payment provider did not answer; try again.", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": banks})
}

func (s *Server) getWallet(w http.ResponseWriter, r *http.Request) {
	wl, err := s.Store.Wallet(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, wl)
}

func (s *Server) walletTransactions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	var before *time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "before must be an RFC 3339 time from next_before.", nil)
			return
		}
		before = &t
	}
	entries, err := s.Store.WalletStatement(r.Context(), orgFrom(r.Context()).ID, before, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{"data": entries}
	if len(entries) == limit {
		out["next_before"] = entries[len(entries)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createFunding(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount      int64  `json:"amount"`
		Provider    string `json:"provider"`
		CallbackURL string `json:"callback_url"`
	}
	if !decode(w, r, &in) {
		return
	}
	org := orgFrom(r.Context())
	problems := map[string]string{}
	if in.Amount < minFunding || in.Amount > maxFunding {
		problems["amount"] = "must be between GHS 1.00 and GHS 100,000.00, in pesewas"
	}
	p, err := payment.Lookup(in.Provider)
	if err != nil {
		problems["provider"] = "is not a configured payment provider"
	} else if !slices.Contains(p.Info().Currencies, onboarding.BillingCurrency) {
		problems["provider"] = "does not take " + onboarding.BillingCurrency
	}
	if u, err := url.Parse(in.CallbackURL); in.CallbackURL != "" && (err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "") {
		problems["callback_url"] = "must be an http(s) URL"
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_funding", "Check the highlighted fields.", problems)
		return
	}
	f, created, err := s.Store.CreateFunding(r.Context(), org.ID, in.Provider, in.Amount, onboarding.BillingCurrency,
		r.Header.Get("Idempotency-Key"), org.UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !created {
		writeJSON(w, http.StatusOK, f)
		return
	}
	email, err := s.Store.UserEmail(r.Context(), org.ID, org.UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cs, err := p.StartCheckout(r.Context(), payment.Checkout{Reference: f.ID, Amount: f.Amount, Currency: f.Currency,
		Email: email, CallbackURL: in.CallbackURL})
	if err != nil {
		s.Logger.WarnContext(r.Context(), "checkout failed", "funding", f.ID, "err", err)
		f, _ = s.Store.SettleFunding(r.Context(), f.ID, payment.Collection{Status: payment.Failed, Message: "the provider did not start the checkout"})
		writeError(w, http.StatusBadGateway, "provider_unavailable", "The payment provider did not start the checkout; try again.", nil)
		return
	}
	if err := s.Store.SetCheckout(r.Context(), f.ID, cs.URL, cs.ProviderReference); err != nil {
		s.internal(w, r, err)
		return
	}
	f.CheckoutURL = &cs.URL
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) listFundings(w http.ResponseWriter, r *http.Request) {
	fs, err := s.Store.ListFundings(r.Context(), orgFrom(r.Context()).ID, 50)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": fs})
}

// getFunding returns a funding, first asking the provider about it while it
// is pending: the page a checkout returns to settles it even before the
// webhook arrives.
func (s *Server) getFunding(w http.ResponseWriter, r *http.Request) {
	f, err := s.Store.GetFunding(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "funding_not_found", "No funding "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if g, err := queue.ReconcileFunding(r.Context(), s.Store, f); err != nil {
		s.Logger.WarnContext(r.Context(), "funding check", "funding", f.ID, "err", err)
	} else {
		f = g
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) listPayoutAccounts(w http.ResponseWriter, r *http.Request) {
	as, err := s.Store.ListPayoutAccounts(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": as})
}

func digits(s string) bool {
	return s != "" && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsDigit(r) }) < 0
}

func (s *Server) addPayoutAccount(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Provider      string `json:"provider"`
		Type          string `json:"type"`
		Name          string `json:"name"`
		BankCode      string `json:"bank_code"`
		AccountNumber string `json:"account_number"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name, in.BankCode = strings.TrimSpace(in.Name), strings.TrimSpace(in.BankCode)
	in.AccountNumber = strings.ReplaceAll(strings.TrimSpace(in.AccountNumber), " ", "")
	problems := map[string]string{}
	p, err := payment.Lookup(in.Provider)
	if err != nil || !p.Info().Payouts {
		problems["provider"] = "is not a configured provider that pays out"
	}
	if in.Type != "bank" && in.Type != "mobile_money" {
		problems["type"] = "must be bank or mobile_money"
	}
	if in.Name == "" || len(in.Name) > 120 {
		problems["name"] = "is the account holder's name"
	}
	if in.BankCode == "" {
		problems["bank_code"] = "choose the bank or mobile network"
	}
	if !digits(in.AccountNumber) || len(in.AccountNumber) < 6 || len(in.AccountNumber) > 20 {
		problems["account_number"] = "must be 6 to 20 digits"
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_payout_account", "Check the highlighted fields.", problems)
		return
	}
	bankName := ""
	if banks, err := p.Banks(r.Context(), onboarding.BillingCurrency); err == nil {
		i := slices.IndexFunc(banks, func(b payment.Bank) bool { return b.Code == in.BankCode })
		if i < 0 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_payout_account", "Check the highlighted fields.",
				map[string]string{"bank_code": "is not a bank or network this provider pays to"})
			return
		}
		bankName = banks[i].Name
	}
	code, err := p.AddRecipient(r.Context(), payment.Destination{Type: in.Type, Name: in.Name, BankCode: in.BankCode,
		AccountNumber: in.AccountNumber, Currency: onboarding.BillingCurrency})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "payout_account_refused",
			"The payment provider did not accept this account: "+err.Error(), nil)
		return
	}
	org := orgFrom(r.Context())
	a, err := s.Store.AddPayoutAccount(r.Context(), store.PayoutAccount{OrganizationID: org.ID, Provider: in.Provider, Type: in.Type,
		Name: in.Name, BankCode: in.BankCode, BankName: bankName, AccountNumber: in.AccountNumber,
		Currency: onboarding.BillingCurrency, RecipientCode: code}, org.UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) removePayoutAccount(w http.ResponseWriter, r *http.Request) {
	err := s.Store.RemovePayoutAccount(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "payout_account_not_found", "No payout account "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listWithdrawals(w http.ResponseWriter, r *http.Request) {
	ws, err := s.Store.ListWithdrawals(r.Context(), orgFrom(r.Context()).ID, nil, 50)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ws})
}

func (s *Server) requestWithdrawal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount          int64  `json:"amount"`
		PayoutAccountID string `json:"payout_account_id"`
		Reason          string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Amount < minWithdrawal {
		writeError(w, http.StatusUnprocessableEntity, "invalid_withdrawal", "Check the highlighted fields.",
			map[string]string{"amount": "must be at least GHS 1.00, in pesewas"})
		return
	}
	org := orgFrom(r.Context())
	wd, created, err := s.Store.RequestWithdrawal(r.Context(), org.ID, in.PayoutAccountID, in.Amount,
		strings.TrimSpace(in.Reason), r.Header.Get("Idempotency-Key"), org.UserID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusUnprocessableEntity, "invalid_withdrawal", "Check the highlighted fields.",
			map[string]string{"payout_account_id": "is not one of your payout accounts"})
	case errors.Is(err, store.ErrInsufficientFunds):
		writeError(w, http.StatusUnprocessableEntity, "insufficient_funds",
			"The wallet does not have that much available. Money held for live claims in flight cannot be withdrawn.", nil)
	case err != nil:
		s.internal(w, r, err)
	case created:
		writeJSON(w, http.StatusCreated, wd)
	default:
		writeJSON(w, http.StatusOK, wd)
	}
}

// paymentWebhook takes a provider's notice. It is only a prompt: the funding
// or payout is then confirmed with the provider before money moves, so a
// forged or replayed notice moves nothing.
func (s *Server) paymentWebhook(w http.ResponseWriter, r *http.Request) {
	p, err := payment.Lookup(r.PathValue("provider"))
	if err != nil {
		writeError(w, http.StatusNotFound, "provider_not_found", "No payment provider "+r.PathValue("provider")+".", nil)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Webhook body is over 1 MiB.", nil)
		return
	}
	e, ok, err := p.ParseWebhook(r.Header, body)
	if errors.Is(err, payment.ErrBadSignature) {
		writeError(w, http.StatusUnauthorized, "bad_signature", "The webhook signature does not match.", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_webhook", err.Error(), nil)
		return
	}
	if ok {
		switch e.Kind {
		case "collection":
			f, err := s.Store.GetFunding(r.Context(), "", e.Reference)
			if err == nil && f.Provider == p.Info().ID {
				_, err = queue.ReconcileFunding(r.Context(), s.Store, f)
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				s.internal(w, r, err) // the provider retries
				return
			}
		case "payout":
			if _, err := queue.ReconcileWithdrawal(r.Context(), s.Store, e.Reference, false); err != nil && !errors.Is(err, store.ErrNotFound) {
				s.internal(w, r, err)
				return
			}
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) opListWithdrawals(w http.ResponseWriter, r *http.Request) {
	var statuses []string
	if st := r.URL.Query().Get("status"); st != "" {
		statuses = strings.Split(st, ",")
	}
	ws, err := s.Store.ListWithdrawals(r.Context(), "", statuses, 200)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ws})
}

func (s *Server) opApproveWithdrawal(w http.ResponseWriter, r *http.Request) {
	wd, err := queue.ApproveWithdrawal(r.Context(), s.Store, s.Queue, r.PathValue("id"), orgFrom(r.Context()).UserID)
	s.withdrawalDecided(w, r, wd, err)
}

func (s *Server) opRejectWithdrawal(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		writeError(w, http.StatusUnprocessableEntity, "reason_required", "Say why, for the organisation.",
			map[string]string{"reason": "is required"})
		return
	}
	ctx := r.Context()
	tx, err := s.Store.Pool.Begin(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer tx.Rollback(ctx)
	wd, err := store.LockWithdrawal(ctx, tx, r.PathValue("id"))
	if err == nil && wd.Status != "requested" {
		err = store.ErrWrongState
	}
	if err == nil {
		err = store.ReturnWithdrawal(ctx, tx, wd, "rejected", strings.TrimSpace(in.Reason), orgFrom(ctx).UserID)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		wd, err = s.Store.GetWithdrawal(ctx, "", wd.ID)
	}
	s.withdrawalDecided(w, r, wd, err)
}

func (s *Server) withdrawalDecided(w http.ResponseWriter, r *http.Request, wd store.Withdrawal, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "withdrawal_not_found", "No withdrawal "+r.PathValue("id")+".", nil)
	case errors.Is(err, store.ErrWrongState):
		writeError(w, http.StatusConflict, "withdrawal_decided", "This withdrawal was already "+wd.Status+".", nil)
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, wd)
	}
}

func (s *Server) opAdjust(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Amount int64  `json:"amount"`
		Memo   string `json:"memo"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := r.Header.Get("Idempotency-Key")
	problems := map[string]string{}
	if in.Amount == 0 {
		problems["amount"] = "must be a credit (positive) or a debit (negative), in pesewas"
	}
	if strings.TrimSpace(in.Memo) == "" {
		problems["memo"] = "say why; the organisation sees it on its statement"
	}
	if key == "" {
		problems["Idempotency-Key"] = "is required, so a repeat does not credit twice"
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_adjustment", "Check the highlighted fields.", problems)
		return
	}
	org := r.PathValue("org")
	posted, err := s.Store.Adjust(r.Context(), org, in.Amount, strings.TrimSpace(in.Memo), org+":"+key, orgFrom(r.Context()).UserID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "organization_not_found", "No organisation "+org+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	wl, err := s.Store.Wallet(r.Context(), org)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	status := http.StatusCreated
	if !posted {
		status = http.StatusOK
	}
	writeJSON(w, status, wl)
}

func (s *Server) opLedger(w http.ResponseWriter, r *http.Request) {
	accts, err := s.Store.PlatformBalances(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	owed, err := s.Store.WalletTotals(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	balanced, err := s.Store.LedgerBalanced(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accts, "wallets": owed, "balanced": balanced})
}
