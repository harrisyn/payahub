package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/store"
)

// A caller can wait for the payer's answer in the same response: with
// "Prefer: wait=N" (RFC 7240) or ?wait=N, up to MaxWait seconds. The claim
// is saved and its submission queued in one transaction before the wait
// starts, so a timeout or a dropped connection loses nothing: the claim
// carries on and its webhook and status say how it ended.

const MaxWait = 60

// waitFor reads the caller's wait, in seconds; 0 means do not wait.
func waitFor(r *http.Request) int {
	raw := r.URL.Query().Get("wait")
	for _, pref := range strings.Split(r.Header.Get("Prefer"), ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(pref), "="); ok && strings.EqualFold(k, "wait") {
			raw = v
		}
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0
	}
	return min(n, MaxWait)
}

// Outcome is how the payer answered, as far as it has.
type Outcome struct {
	State            claim.State `json:"state"`
	Final            bool        `json:"final"` // the payer has decided (acknowledged or rejected)
	PayerClaimNumber string      `json:"payer_claim_number,omitempty"`
	Message          string      `json:"message"`
	Error            *struct {
		Code         string `json:"code"`
		Message      string `json:"message"`
		PayerMessage string `json:"payer_message,omitempty"`
	} `json:"error,omitempty"`
}

func outcomeOf(st store.Settled) Outcome {
	c := st.Claim
	o := Outcome{State: c.State, PayerClaimNumber: c.PayerClaimNumber}
	switch c.State {
	case claim.Acknowledged, claim.Approved, claim.Paid:
		o.Final, o.Message = true, "The payer accepted the claim."
	case claim.Rejected:
		o.Final, o.Message = true, "The payer rejected the claim."
	case claim.Submitted:
		o.Message = "Sent; the payer will answer later. Its answer comes by webhook and on the claim."
	case claim.Ready:
		o.Message = "Not sent: something a person must fix first (see error)."
	default:
		o.Message = "The claim is " + string(c.State) + "."
	}
	if st.Event != nil && len(st.Event.Detail) > 0 {
		var d struct {
			Error *struct {
				Code         string `json:"code"`
				Message      string `json:"message"`
				PayerMessage string `json:"payer_message,omitempty"`
			} `json:"error"`
		}
		if json.Unmarshal(st.Event.Detail, &d) == nil && d.Error != nil {
			o.Error = d.Error
		}
	}
	return o
}

// settle waits up to seconds for the claim to settle and writes the answer:
// 200 with the outcome, or 202 when the payer has not answered in time.
func (s *Server) settle(w http.ResponseWriter, r *http.Request, c *claim.Claim, issues []engine.Issue, seconds int) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(seconds)*time.Second)
	defer cancel()
	st, err := s.Store.WaitSettled(ctx, c.OrganizationID, c.ID)
	w.Header().Set("Preference-Applied", "wait="+strconv.Itoa(seconds))
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		w.Header().Set("Location", "/v1/claims/"+c.ID)
		writeJSON(w, http.StatusAccepted, map[string]any{"claim": c, "issues": issues, "outcome": nil,
			"message": "Still with the payer after " + strconv.Itoa(seconds) + "s. The claim carries on; its webhook and GET /v1/claims/" + c.ID + " give the answer."})
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"claim": st.Claim, "issues": issues, "outcome": outcomeOf(st)})
}
