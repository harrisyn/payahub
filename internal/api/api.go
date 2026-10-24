// Package api is Payahub's public REST API under /v1. Everything (the
// dashboard, the document channel, Serenity) uses these routes; there is no
// private back door. Built on net/http with no framework.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/deid"
	"github.com/harrisyn/payahub/internal/icd10"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

const maxBody = 1 << 20 // 1 MiB per claim request

type Server struct {
	Store  *store.Store
	Queue  *queue.Client
	Vault  *vault.Vault
	Logger *slog.Logger
	// OCR reads forms on this server to black out the patient's identity
	// before a model sees them; nil uses the installed Tesseract.
	OCR deid.OCR
	// ConsoleURL is the web app's address, for links in email.
	ConsoleURL string
	// AllowPrivateWebhooks lets webhook endpoints be http or internal
	// addresses (development only).
	AllowPrivateWebhooks bool
}

func (s *Server) Handler() http.Handler {
	// Callers that wait for the payer are woken by claim_saved; listen
	// before the first one arrives.
	s.Store.ListenForClaims()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.Handle("POST /v1/claims", s.auth(s.createClaim))
	mux.Handle("POST /v1/claims/import", s.auth(s.importClaims))
	mux.Handle("POST /v1/claims/validate", s.auth(s.validateClaim))
	mux.Handle("GET /v1/claims/{id}", s.auth(s.getClaim))
	mux.Handle("GET /v1/claims/{id}/events", s.auth(s.claimEvents))
	mux.Handle("POST /v1/claims/{id}/submit", s.auth(s.submitClaim))
	mux.Handle("GET /v1/claims/{id}/checks", s.auth(s.claimChecks))
	mux.Handle("POST /v1/claims/{id}/review", s.auth(s.reviewClaim))
	s.catalogRoutes(mux)
	s.consoleRoutes(mux)
	s.operatorRoutes(mux)
	s.leadRoutes(mux)
	s.priceRoutes(mux)
	s.walletRoutes(mux)
	s.aiRoutes(mux)
	s.assistRoutes(mux)
	s.webhookRoutes(mux)
	s.teamRoutes(mux)
	return mux
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string, details any) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg, Details: details}})
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on our side.", nil)
}

type ctxKey struct{}

func orgFrom(ctx context.Context) store.Org { return ctx.Value(ctxKey{}).(store.Org) }

func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || strings.TrimSpace(key) == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "Send your API key as 'Authorization: Bearer <key>'.", nil)
			return
		}
		org, err := s.Store.Authenticate(r.Context(), strings.TrimSpace(key))
		if errors.Is(err, store.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "That API key is not recognised.", nil)
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if !permitted(org, r) {
			forbidden(w, r)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, org)))
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "degraded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readInput decodes a claim request strictly (unknown fields rejected) and
// returns the raw bytes' hash for idempotency checks.
func (s *Server) readInput(w http.ResponseWriter, r *http.Request) (*claim.Input, []byte, bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Request body is over 1 MiB.", nil)
		return nil, nil, false
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var in claim.Input
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return nil, nil, false
	}
	in.Normalize(func() string { return store.NewID("ln") })
	if err := s.completeInput(r.Context(), orgFrom(r.Context()).ID, &in, pricesGiven(raw, len(in.Lines))); err != nil {
		s.internal(w, r, err)
		return nil, nil, false
	}
	if errs := in.Check(); len(errs) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_claim", "The claim has missing or invalid fields.", errs)
		return nil, nil, false
	}
	if p, ok := catalog.LookupPayer(in.PayerID); !ok || p.Inactive {
		writeError(w, http.StatusUnprocessableEntity, "unknown_payer", "No payer '"+in.PayerID+"' in the catalogue. See GET /v1/payers.", nil)
		return nil, nil, false
	}
	sum := sha256.Sum256(raw)
	return &in, sum[:], true
}

func newClaim(org store.Org, in *claim.Input, sha []byte) *claim.Claim {
	now := time.Now().UTC()
	c := &claim.Claim{
		ID:             store.NewID("clm"),
		OrganizationID: org.ID,
		Environment:    org.Environment,
		State:          claim.Draft,
		Provenance:     claim.Provenance{Source: claim.SourceAPI, ReceivedAt: now, PayloadSHA256: hexString(sha)},
		CreatedAt:      now,
		UpdatedAt:      now,
		Input:          *in,
	}
	c.ComputeTotal()
	return c
}

func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2], out[i*2+1] = digits[v>>4], digits[v&0x0f]
	}
	return string(out)
}

func (s *Server) createClaim(w http.ResponseWriter, r *http.Request) {
	in, sha, ok := s.readInput(w, r)
	if !ok {
		return
	}
	org := orgFrom(r.Context())
	c := newClaim(org, in, sha)
	// Resolve lines now so the stored claim shows its standard items and
	// payer codes; lines still unresolved are tried again at submit.
	if _, err := queue.Prepare(r.Context(), s.Store.Pool, c); err != nil {
		s.internal(w, r, err)
		return
	}
	c, replayed, err := s.Store.CreateClaim(r.Context(), c, r.Header.Get("Idempotency-Key"), sha)
	switch {
	case errors.Is(err, store.ErrIdempotencyConflict):
		writeError(w, http.StatusUnprocessableEntity, "idempotency_conflict",
			"This Idempotency-Key was already used with a different request body.", nil)
	case err != nil:
		s.internal(w, r, err)
	case r.URL.Query().Get("submit") == "true":
		// Create and send in one call; a replay sends nothing twice, as
		// submitting a claim already on its way is a no-op.
		if replayed {
			w.Header().Set("Idempotent-Replayed", "true")
		}
		s.submit(w, r, org.ID, c.ID, http.StatusAccepted, c)
	case replayed:
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusOK, c)
	default:
		writeJSON(w, http.StatusCreated, c)
	}
}

func (s *Server) validateClaim(w http.ResponseWriter, r *http.Request) {
	in, sha, ok := s.readInput(w, r)
	if !ok {
		return
	}
	org := orgFrom(r.Context())
	c := newClaim(org, in, sha)
	v, err := queue.Check(r.Context(), s.Store.Pool, c)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) getClaim(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetClaim(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) claimEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.ListEvents(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": events})
}

// submitClaim queues the claim for delivery and answers 202 at once; the
// outcome arrives on the claim (and, later, by webhook).
func (s *Server) submitClaim(w http.ResponseWriter, r *http.Request) {
	s.submit(w, r, orgFrom(r.Context()).ID, r.PathValue("id"), http.StatusAccepted, nil)
}

// submit queues the claim and answers: at once (status, the claim and its
// issues), or, when the caller asked to wait, with the payer's answer.
func (s *Server) submit(w http.ResponseWriter, r *http.Request, orgID, id string, status int, created *claim.Claim) {
	c, v, err := queue.Enqueue(r.Context(), s.Store, s.Queue, orgID, id)
	notSent := func(code, msg string, details any) {
		if created == nil {
			writeError(w, http.StatusPaymentRequired, code, msg, details)
			return
		}
		// The claim was saved; say so, with why it was not sent.
		writeJSON(w, http.StatusCreated, map[string]any{"claim": created, "submitted": false,
			"error": apiError{Code: code, Message: msg, Details: details}})
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+id+".", nil)
	case errors.Is(err, queue.ErrTrialLimit):
		notSent("trial_limit", "Your trial has no live claims left. Choose a plan to keep sending live claims.", nil)
	case errors.Is(err, queue.ErrWalletEmpty):
		notSent("wallet_empty", "Your wallet does not cover another live claim. Add funds to keep sending live claims.", nil)
	case errors.Is(err, queue.ErrNotSubmittable):
		if created != nil {
			created.State = c.State
			writeJSON(w, http.StatusCreated, map[string]any{"claim": created, "submitted": false,
				"error": apiError{Code: "not_submittable", Message: "Saved, but not sent: the claim did not pass validation; it is now " + string(c.State) + ".", Details: v.Issues}})
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "not_submittable",
			"The claim did not pass validation; it is now "+string(c.State)+".", v.Issues)
	case err != nil:
		s.internal(w, r, err)
	default:
		if wait := waitFor(r); wait > 0 {
			s.settle(w, r, c, v.Issues, wait)
			return
		}
		writeJSON(w, status, map[string]any{"claim": c, "issues": v.Issues})
	}
}

// claimChecks runs the edit rules on a saved claim, as submitting would,
// without changing it: what stands between the claim and the payer.
func (s *Server) claimChecks(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetClaim(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	v, err := queue.Check(r.Context(), s.Store.Pool, c)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// reviewClaim records that a person confirmed issues awaiting review (an AI
// value below its threshold, a possible duplicate). Errors cannot be
// confirmed away; they are fixed on the claim.
func (s *Server) reviewClaim(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirm []queue.Confirm `json:"confirm"`
		Note    string          `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Confirm) == 0 || len(in.Confirm) > 100 || len(in.Note) > 500 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_review", "Confirm between 1 and 100 issues, with a note of up to 500 characters.", nil)
		return
	}
	org := orgFrom(r.Context())
	by := "API key"
	if org.Session {
		if email, err := s.Store.UserEmail(r.Context(), org.ID, org.UserID); err == nil && email != "" {
			by = email
		}
	}
	c, v, err := queue.Review(r.Context(), s.Store, org.ID, r.PathValue("id"), by, in.Note, in.Confirm)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+r.PathValue("id")+".", nil)
	case errors.Is(err, queue.ErrClaimMoving):
		writeError(w, http.StatusConflict, "claim_moving", "The claim is already being sent; there is nothing to review.", nil)
	case errors.Is(err, queue.ErrNotReviewable):
		writeError(w, http.StatusUnprocessableEntity, "not_reviewable", "Only issues awaiting review can be confirmed. These are the claim's issues now.", v.Issues)
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"claim": c, "validation": v})
	}
}

// pricesGiven reports, for each line of a raw claim request, whether it
// carries unit_price at all (an explicit 0 counts).
func pricesGiven(raw []byte, n int) []bool {
	var given struct {
		Lines []struct {
			UnitPrice json.RawMessage `json:"unit_price"`
		} `json:"lines"`
	}
	out := make([]bool, n)
	if json.Unmarshal(raw, &given) != nil {
		for i := range out {
			out[i] = true
		}
		return out
	}
	for i := range out {
		out[i] = i >= len(given.Lines) || given.Lines[i].UnitPrice != nil
	}
	return out
}

// completeInput fills what the organisation's own data says about a claim:
// lines naming one of its price list codes get the price when they give
// none (an explicit 0 stays 0) and the description when it is empty, never
// a price in another currency than the claim's; ICD-10 diagnoses get their
// title.
func (s *Server) completeInput(ctx context.Context, orgID string, in *claim.Input, priceGiven []bool) error {
	for i := range in.Diagnoses {
		d := &in.Diagnoses[i]
		if d.Code.Display == "" && icd10.Is(d.Code.System) {
			if c, st := icd10.Check(d.Code.Value); st == icd10.Listed {
				d.Code.Display = c.Title
			}
		}
	}
	var codes []string
	for i := range in.Lines {
		in.Lines[i].PriceFrom = ""
		if l := in.Lines[i]; l.Code != nil && l.Code.System != catalog.System && l.Code.Value != "" {
			codes = append(codes, l.Code.Value)
		}
	}
	if len(codes) == 0 {
		return nil
	}
	prices, err := store.PricesFor(ctx, s.Store.Pool, orgID, codes)
	if err != nil || len(prices) == 0 {
		return err
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		if l.Code == nil || l.Code.System == catalog.System {
			continue
		}
		p, ok := prices[l.Code.Value]
		if !ok {
			continue
		}
		if strings.TrimSpace(l.Description) == "" {
			l.Description = p.Name
		}
		if i < len(priceGiven) && !priceGiven[i] && p.Currency == in.Currency {
			l.UnitPrice = p.UnitPrice
			l.PriceFrom = "price_list"
		}
	}
	return nil
}
