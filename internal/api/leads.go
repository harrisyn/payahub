package api

import (
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/openapi"
)

// Lead routes take enquiries from the public site and show them to operators.
// The interface spec is public, so a platform can build against it before it
// talks to anyone.
func (s *Server) leadRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/leads", s.createLead)
	mux.Handle("GET /v1/operator/leads", s.operator(s.opListLeads))
	mux.HandleFunc("GET /v1/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(openapi.Spec)
	})
}

var leadKinds = map[string]bool{"contact": true, "platform": true, "payer": true, "specs": true}

// A phone number is 7 to 15 digits once spaces, dashes, dots, brackets and a
// leading + are taken out.
var phoneRe = regexp.MustCompile(`^\+?[0-9]{7,15}$`)

func normalPhone(p string) string {
	return strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "").Replace(strings.TrimSpace(p))
}

func (s *Server) createLead(w http.ResponseWriter, r *http.Request) {
	var in store.Lead
	if !decode(w, r, &in) {
		return
	}
	trim := func(v *string, max int) {
		*v = strings.TrimSpace(*v)
		if utf8.RuneCountInString(*v) > max {
			*v = string([]rune(*v)[:max])
		}
	}
	trim(&in.Kind, 20)
	trim(&in.Name, 120)
	trim(&in.Email, 254)
	trim(&in.Organization, 160)
	trim(&in.PayerName, 160)
	trim(&in.PayerContact, 500)
	trim(&in.Message, 2000)
	in.Phone = normalPhone(in.Phone)

	problems := map[string]string{}
	if !leadKinds[in.Kind] {
		problems["kind"] = "Choose what you would like from us."
	}
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email {
		problems["email"] = "Enter an email like you@clinic.com."
	}
	// Everyone but a developer asking for the spec leaves a number to call.
	if (in.Kind != "specs" || in.Phone != "") && !phoneRe.MatchString(in.Phone) {
		problems["phone"] = "Enter a phone number, like 024 123 4567 or +233 24 123 4567."
	}
	switch in.Kind {
	case "contact":
		if in.Name == "" {
			problems["name"] = "Enter your name."
		}
	case "platform":
		if in.Organization == "" {
			problems["organization"] = "Enter the insurer or platform's name."
		}
	case "payer":
		if in.PayerName == "" {
			problems["payer_name"] = "Enter the payer's name."
		}
	}
	if in.MonthlyClaims != nil && (*in.MonthlyClaims < 0 || *in.MonthlyClaims > 10_000_000) {
		problems["monthly_claims"] = "Enter the number of claims a month."
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_lead", "Some details need fixing.", problems)
		return
	}
	lead, replayed, err := s.Store.CreateLead(r.Context(), in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"id": lead.ID, "kind": lead.Kind, "created_at": lead.CreatedAt})
}

func (s *Server) opListLeads(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && !leadKinds[kind] {
		writeError(w, http.StatusBadRequest, "bad_kind", "kind is one of contact, platform, payer or specs.", nil)
		return
	}
	leads, err := s.Store.ListLeads(r.Context(), kind, 500)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": leads})
}
