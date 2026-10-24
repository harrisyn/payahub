package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

// A member check is asked while the member waits at the desk, so the API
// waits for the payer's answer by default (20 seconds, or the caller's
// Prefer: wait). The payer call itself runs on a worker, which alone opens
// the organisation's payer credentials.
const memberCheckWait = 20

func (s *Server) memberCheckRoutes(mux *http.ServeMux) {
	mux.Handle("POST /v1/member-checks", s.auth(s.createMemberCheck))
	mux.Handle("GET /v1/member-checks/{id}", s.auth(s.getMemberCheck))
}

func (s *Server) createMemberCheck(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Request body is over 16 KiB.", nil)
		return
	}
	var in struct {
		PayerID      string `json:"payer_id"`
		MemberNumber string `json:"member_number"`
		OTP          string `json:"otp"`
		Action       string `json:"action"`
		Environment  string `json:"environment"`
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	in.PayerID, in.MemberNumber, in.OTP = strings.TrimSpace(in.PayerID), strings.TrimSpace(in.MemberNumber), strings.TrimSpace(in.OTP)
	if in.Action == "" {
		in.Action = "verify"
	}
	problems := map[string]string{}
	if _, ok := catalog.LookupPayer(in.PayerID); !ok {
		problems["payer_id"] = "Choose an insurer from the catalogue."
	}
	if in.MemberNumber == "" || len(in.MemberNumber) > 64 {
		problems["member_number"] = "Enter the member's number from their card."
	}
	if len(in.OTP) > 16 {
		problems["otp"] = "Enter the code the member received."
	}
	if in.Action != "verify" && in.Action != "send_otp" {
		problems["action"] = "verify or send_otp."
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_fields", "Check the highlighted fields.", problems)
		return
	}
	org := orgFrom(r.Context())
	sha := sha256.Sum256(raw)
	m, err := queue.CheckMember(r.Context(), s.Store, s.Queue, s.Vault, store.MemberCheck{
		ID: store.NewID("mck"), OrganizationID: org.ID, Environment: envOf(org, in.Environment),
		PayerID: in.PayerID, MemberNumber: in.MemberNumber, Action: in.Action, CreatedBy: org.UserID,
	}, in.OTP, r.Header.Get("Idempotency-Key"), sha[:])
	if errors.Is(err, store.ErrIdempotencyConflict) {
		writeError(w, http.StatusUnprocessableEntity, "idempotency_conflict",
			"This Idempotency-Key was already used with a different request body.", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.answerMemberCheck(w, r, m)
}

func (s *Server) getMemberCheck(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.GetMemberCheck(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No member check "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.answerMemberCheck(w, r, m)
}

// answerMemberCheck waits for a pending check, then answers 200 with the
// result or 202 with where to look later.
func (s *Server) answerMemberCheck(w http.ResponseWriter, r *http.Request, m store.MemberCheck) {
	wait := memberCheckWait
	if asked := r.URL.Query().Get("wait") + r.Header.Get("Prefer"); asked != "" {
		wait = waitFor(r)
	}
	if m.Status == "pending" && wait > 0 {
		extendWrite(w, time.Duration(wait)*time.Second+10*time.Second)
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(wait)*time.Second)
		got, err := s.Store.WaitMemberCheck(ctx, m.OrganizationID, m.ID)
		cancel()
		switch {
		case err == nil:
			m = got
		case !errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil:
			s.internal(w, r, err)
			return
		}
	}
	if m.Status == "pending" {
		w.Header().Set("Location", "/v1/member-checks/"+m.ID)
		writeJSON(w, http.StatusAccepted, m)
		return
	}
	writeJSON(w, http.StatusOK, m)
}
