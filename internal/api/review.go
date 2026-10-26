package api

import (
	"net/http"

	"github.com/harrisyn/payahub/internal/assist"
	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

func (s *Server) reviewRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/review-queue", s.auth(s.reviewQueue))
}

// reviewQueue is what is holding the organisation's unsent claims back and
// needs a person: its own codes with no standard item (mapping one moves
// every claim waiting on it, through PUT /v1/item-mappings), and claims
// waiting for someone to confirm what a model proposed or a possible
// duplicate (POST /v1/claims/{id}/review).
func (s *Server) reviewQueue(w http.ResponseWriter, r *http.Request) {
	org := orgFrom(r.Context())
	env := r.URL.Query().Get("environment")
	if !org.Session {
		env = org.Environment
	}
	if env != "" && env != "live" && env != "sandbox" {
		writeError(w, http.StatusBadRequest, "invalid_environment", "environment is live or sandbox.", nil)
		return
	}
	held, err := s.Store.HeldCodes(r.Context(), org.ID, env, 50)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	type code struct {
		store.HeldCode
		Suggestions []suggestion `json:"suggestions"`
	}
	codes := make([]code, len(held))
	for i, h := range held {
		// Word matching only: the page is read often, and a model's pick
		// is a click away on the mapping page.
		codes[i] = code{h, mergeSuggestions(assist.Pick{}, false, catalog.Suggest(h.Name, "", 3), 3)}
	}

	waiting, err := s.Store.ClaimsInState(r.Context(), org.ID, env, string(claim.NeedsReview), 25)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	type entry struct {
		Claim  *claim.Claim   `json:"claim"`
		Issues []engine.Issue `json:"issues"`
	}
	claims := make([]entry, 0, len(waiting))
	for _, c := range waiting {
		v, err := queue.Check(r.Context(), s.Store.Pool, c)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		e := entry{Claim: c, Issues: []engine.Issue{}}
		for _, i := range v.Issues {
			if i.Severity == engine.SeverityReview {
				e.Issues = append(e.Issues, i)
			}
		}
		claims = append(claims, e)
	}
	counts := map[string]int{}
	for _, st := range []claim.State{claim.NeedsReview, claim.Ready} {
		if counts[string(st)], err = s.Store.CountClaims(r.Context(), org.ID, env, string(st)); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"codes": codes, "claims": claims, "counts": counts})
}
