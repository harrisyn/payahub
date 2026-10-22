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
	"github.com/harrisyn/payahub/internal/engine"
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
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.Handle("POST /v1/claims", s.auth(s.createClaim))
	mux.Handle("POST /v1/claims/validate", s.auth(s.validateClaim))
	mux.Handle("GET /v1/claims/{id}", s.auth(s.getClaim))
	mux.Handle("GET /v1/claims/{id}/events", s.auth(s.claimEvents))
	mux.Handle("POST /v1/claims/{id}/submit", s.auth(s.submitClaim))
	s.catalogRoutes(mux)
	s.consoleRoutes(mux)
	s.operatorRoutes(mux)
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
func readInput(w http.ResponseWriter, r *http.Request) (*claim.Input, []byte, bool) {
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
	in, sha, ok := readInput(w, r)
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
	case replayed:
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusOK, c)
	default:
		writeJSON(w, http.StatusCreated, c)
	}
}

func (s *Server) validateClaim(w http.ResponseWriter, r *http.Request) {
	in, sha, ok := readInput(w, r)
	if !ok {
		return
	}
	org := orgFrom(r.Context())
	c := newClaim(org, in, sha)
	m, err := queue.Prepare(r.Context(), s.Store.Pool, c)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	overrides, err := store.PayerOverrides(r.Context(), s.Store.Pool, org.ID, c.PayerID, org.Environment)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, engine.Validate(c, m, overrides))
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
	c, v, err := queue.Enqueue(r.Context(), s.Store, s.Queue, orgFrom(r.Context()).ID, r.PathValue("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+r.PathValue("id")+".", nil)
	case errors.Is(err, queue.ErrTrialLimit):
		writeError(w, http.StatusPaymentRequired, "trial_limit",
			"Your trial has no live claims left. Choose a plan to keep sending live claims.", nil)
	case errors.Is(err, queue.ErrNotSubmittable):
		writeError(w, http.StatusUnprocessableEntity, "not_submittable",
			"The claim did not pass validation; it is now "+string(c.State)+".", v.Issues)
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{"claim": c, "issues": v.Issues})
	}
}
