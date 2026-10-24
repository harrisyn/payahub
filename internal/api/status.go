package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

// POST /v1/claims/{id}/status-check asks the payer now where an
// acknowledged claim stands, rather than at its next scheduled check, and
// waits for the answer like a real-time submit (20 seconds by default).
func (s *Server) statusCheck(w http.ResponseWriter, r *http.Request) {
	org := orgFrom(r.Context())
	c, err := s.Store.GetClaim(r.Context(), org.ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "claim_not_found", "No claim "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if c.State != claim.Acknowledged && c.State != claim.Approved {
		writeError(w, http.StatusConflict, "not_with_payer",
			"Only a claim the payer acknowledged and has not yet paid or rejected can be asked about; this one is "+string(c.State)+".", nil)
		return
	}
	since := time.Now()
	if err := queue.CheckStatusNow(r.Context(), s.Queue, c); err != nil {
		s.internal(w, r, err)
		return
	}
	wait := memberCheckWait
	if asked := r.URL.Query().Get("wait") + r.Header.Get("Prefer"); asked != "" {
		wait = waitFor(r)
	}
	if wait > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(wait)*time.Second)
		got, err := s.Store.WaitStatusChecked(ctx, org.ID, c.ID, since)
		cancel()
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, got)
			return
		case !errors.Is(err, context.DeadlineExceeded) && r.Context().Err() == nil:
			s.internal(w, r, err)
			return
		}
	}
	w.Header().Set("Location", "/v1/claims/"+c.ID)
	writeJSON(w, http.StatusAccepted, c)
}
