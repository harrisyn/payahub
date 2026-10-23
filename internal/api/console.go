package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/onboarding"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

// Console routes serve the web app: sign-up, onboarding, payers and keys.
// They are ordinary /v1 routes; the ones that change who can reach the
// organisation's data (keys, credentials, plan) need a signed-in session, so
// a leaked API key cannot mint new keys or read payer settings.
func (s *Server) consoleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/signup", s.signup)
	mux.HandleFunc("POST /v1/sessions", s.login)
	mux.Handle("DELETE /v1/sessions/current", s.session(s.logout))
	mux.Handle("GET /v1/organization", s.session(s.getOrganization))
	mux.Handle("PUT /v1/organization/profile", s.session(s.putProfile))
	mux.Handle("PUT /v1/organization/plan", s.session(s.putPlan))
	mux.Handle("GET /v1/api-keys", s.session(s.listKeys))
	mux.Handle("POST /v1/api-keys", s.session(s.createKey))
	mux.Handle("DELETE /v1/api-keys/{id}", s.session(s.revokeKey))
	mux.Handle("GET /v1/payer-settings", s.session(s.listPayerSettings))
	mux.Handle("PUT /v1/payer-settings/{payer}/{env}", s.session(s.putPayerSetting))
	mux.Handle("GET /v1/connector-accounts", s.session(s.listAccounts))
	mux.Handle("PUT /v1/connector-accounts/{connector}/{env}", s.session(s.putAccount))
	mux.Handle("POST /v1/connector-accounts/{connector}/{env}/check", s.session(s.checkAccount))
	mux.Handle("GET /v1/claims", s.auth(s.listClaims))
}

func (s *Server) session(next http.HandlerFunc) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		if !orgFrom(r.Context()).Session {
			writeError(w, http.StatusForbidden, "session_required",
				"This route needs a signed-in console session, not an API key.", nil)
			return
		}
		next(w, r)
	})
}

// decode reads a small JSON body strictly.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return false
	}
	return true
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	var in store.Signup
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	in.Name = strings.TrimSpace(in.Name)
	in.Organization = strings.TrimSpace(in.Organization)
	in.Country = strings.ToUpper(strings.TrimSpace(in.Country))
	if in.Country == "" {
		in.Country = "GH"
	}
	problems := map[string]string{}
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email {
		problems["email"] = "Enter a work email like you@clinic.com."
	}
	if utf8.RuneCountInString(in.Password) < 10 {
		problems["password"] = "Use at least 10 characters."
	}
	if in.Name == "" {
		problems["name"] = "Enter your name."
	}
	if in.Organization == "" {
		problems["organization"] = "Enter your organisation's name."
	}
	if len(in.Country) != 2 {
		problems["country"] = "Use a two-letter country code, like GH."
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_signup", "Some details need fixing.", problems)
		return
	}
	a, err := s.Store.CreateAccount(r.Context(), in)
	switch {
	case errors.Is(err, store.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "An account with that email already exists. Sign in instead.", nil)
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, a)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	token, err := s.Store.Login(r.Context(), strings.TrimSpace(in.Email), in.Password)
	switch {
	case errors.Is(err, store.ErrBadLogin):
		writeError(w, http.StatusUnauthorized, "bad_login", "That email and password do not match an account.", nil)
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, map[string]string{"session_token": token})
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := s.Store.Logout(r.Context(), strings.TrimSpace(token)); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Step is one line of the onboarding checklist the console shows.
type Step struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

func (s *Server) getOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := orgFrom(ctx)
	o, err := s.Store.Organization(ctx, org.ID, org.UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	usage, err := s.Store.Usage(ctx, org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	settings, err := s.Store.ListPayerSettings(ctx, org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	acked, err := s.Store.HasAcknowledgedClaim(ctx, org.ID, "sandbox")
	if err != nil {
		s.internal(w, r, err)
		return
	}
	mappings, err := s.Store.ListItemMappings(ctx, org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{"organization": o, "usage": usage, "operator": org.Operator}
	if o.Profile != nil {
		out["recommendation"] = onboarding.Recommend(*o.Profile)
	}
	out["onboarding"] = []Step{
		{ID: "account", Done: true},
		{ID: "profile", Done: o.Profile != nil},
		{ID: "payers", Done: slices.ContainsFunc(settings, func(p store.PayerSetting) bool { return p.Enabled })},
		{ID: "items", Done: len(mappings) > 0},
		{ID: "first_claim", Done: acked},
		{ID: "go_live", Done: o.Plan != onboarding.PlanTrial},
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	var p onboarding.Profile
	if !decode(w, r, &p) {
		return
	}
	if errs := p.Check(); len(errs) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_profile", "Some answers need fixing.", errs)
		return
	}
	for _, id := range p.Payers {
		if _, ok := catalog.LookupPayer(id); !ok {
			writeError(w, http.StatusUnprocessableEntity, "unknown_payer", "No payer '"+id+"' in the catalogue.", nil)
			return
		}
	}
	if err := s.Store.SaveProfile(r.Context(), orgFrom(r.Context()).ID, p); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": p, "recommendation": onboarding.Recommend(p)})
}

func (s *Server) putPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Plan onboarding.Plan `json:"plan"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !slices.Contains(onboarding.Choosable, in.Plan) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_plan", "Choose flat, flat_volume, volume or enterprise.", nil)
		return
	}
	plan := in.Plan
	if plan == onboarding.PlanEnterprise {
		// Enterprise terms are agreed and invoiced offline; until then this is a request.
		plan = onboarding.PlanEnterpriseRequested
	}
	charged, err := s.Store.SetPlan(r.Context(), orgFrom(r.Context()).ID, plan)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plan": plan, "charged": charged})
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.ListAPIKeys(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": keys})
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Environment string `json:"environment"`
		Name        string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Environment != "sandbox" && in.Environment != "live" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_environment", "Environment is sandbox or live.", nil)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = strings.ToUpper(in.Environment[:1]) + in.Environment[1:] + " key"
	}
	k, err := s.Store.CreateAPIKey(r.Context(), orgFrom(r.Context()).ID, in.Environment, in.Name)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, k)
}

func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	err := s.Store.RevokeAPIKey(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key_not_found", "No key "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPayerSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Store.ListPayerSettings(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": settings})
}

func validEnv(w http.ResponseWriter, env string) bool {
	if env != "sandbox" && env != "live" {
		writeError(w, http.StatusNotFound, "invalid_environment", "Environment is sandbox or live.", nil)
		return false
	}
	return true
}

func (s *Server) putPayerSetting(w http.ResponseWriter, r *http.Request) {
	p, ok := catalog.LookupPayer(r.PathValue("payer"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_payer", "No payer '"+r.PathValue("payer")+"' in the catalogue.", nil)
		return
	}
	env := r.PathValue("env")
	if !validEnv(w, env) {
		return
	}
	var in struct {
		Enabled bool   `json:"enabled"`
		Account string `json:"account"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Account != "" && in.Account != "have" && in.Account != "need" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_account", "account is have or need.", nil)
		return
	}
	if err := s.Store.SavePayerSetting(r.Context(), orgFrom(r.Context()).ID, p.ID, env, in.Enabled, in.Account); err != nil {
		s.internal(w, r, err)
		return
	}
	s.listPayerSettings(w, r)
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.Store.ListConnectorAccounts(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": accounts})
}

// accountTarget reads and checks the {connector} and {env} path values.
func accountTarget(w http.ResponseWriter, r *http.Request) (connector.Manifest, string, bool) {
	m, _, ok := connector.Lookup(r.PathValue("connector"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_connector", "No platform or payer interface '"+r.PathValue("connector")+"'.", nil)
		return m, "", false
	}
	env := r.PathValue("env")
	return m, env, validEnv(w, env)
}

// putAccount saves the organisation's credentials on a platform (or a
// standalone payer's interface). They are sealed at once and never returned.
func (s *Server) putAccount(w http.ResponseWriter, r *http.Request) {
	m, env, ok := accountTarget(w, r)
	if !ok {
		return
	}
	var in struct {
		Credentials map[string]string `json:"credentials"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Credentials == nil {
		in.Credentials = map[string]string{}
	}
	known := map[string]bool{}
	for _, f := range m.Credentials {
		known[f.Name] = true
	}
	var fields []string
	for name, v := range in.Credentials {
		if !known[name] {
			writeError(w, http.StatusUnprocessableEntity, "unknown_credential", m.Name+" has no credential '"+name+"'.", nil)
			return
		}
		if v != "" {
			fields = append(fields, name)
		}
	}
	if err := connector.CheckCredentials(m, in.Credentials); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "missing_credentials", connector.AsError(err).Message, nil)
		return
	}
	slices.Sort(fields)
	org := orgFrom(r.Context())
	sealed, err := s.Vault.Seal(in.Credentials, vault.Owner(org.ID, m.Key, env))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.Store.SaveConnectorAccount(r.Context(), org.ID, m.Key, env, sealed, fields); err != nil {
		s.internal(w, r, err)
		return
	}
	s.listAccounts(w, r)
}

// checkAccount tries the saved account by verifying a test member with one
// of the payers it reaches (one the organisation uses, if any), and records
// the result.
func (s *Server) checkAccount(w http.ResponseWriter, r *http.Request) {
	m, env, ok := accountTarget(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	org := orgFrom(ctx)
	if len(m.Credentials) > 0 {
		sealed, err := store.SealedAccount(ctx, s.Store.Pool, org.ID, m.Key, env)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if sealed == nil {
			writeError(w, http.StatusUnprocessableEntity, "no_credentials", "Save your "+m.Name+" account first.", nil)
			return
		}
	}
	payers := catalog.PayersOn(m.Key)
	if len(payers) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no_payers", "No payer in the catalogue is reached through "+m.Name+" yet.", nil)
		return
	}
	target := payers[0]
	if settings, err := s.Store.ListPayerSettings(ctx, org.ID); err == nil {
		for _, p := range payers {
			if slices.ContainsFunc(settings, func(ps store.PayerSetting) bool { return ps.PayerID == p.ID && ps.Enabled }) {
				target = p
				break
			}
		}
	}

	okCheck, message := true, "Connected. "+m.Name+" answered a test member check for "+target.Name+"."
	conn, err := queue.OpenAccount(ctx, s.Store.Pool, s.Vault, org.ID, target, env)
	if err == nil {
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		_, err = conn.VerifyMember(checkCtx, "PAYAHUB-CHECK", "")
		cancel()
	}
	if err != nil {
		e := connector.AsError(err)
		switch {
		case m.Status != connector.StatusAvailable || e.Code == connector.NotSupported:
			okCheck, message = false, "Saved. "+m.Name+" is still being connected on our side, so we will check this account when it goes live."
		case e.Code == connector.MemberNotFound:
			// The payer answered, so the account works; the test member simply does not exist.
		default:
			okCheck, message = false, e.Message
		}
	}
	if len(m.Credentials) > 0 {
		if err := s.Store.RecordAccountCheck(ctx, org.ID, m.Key, env, okCheck, message); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": okCheck, "message": message})
}

func (s *Server) listClaims(w http.ResponseWriter, r *http.Request) {
	org := orgFrom(r.Context())
	q := r.URL.Query()
	env := q.Get("environment")
	if !org.Session {
		env = org.Environment // an API key sees only its own environment
	}
	limit := 50
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 200 {
		limit = n
	}
	var before *time.Time
	if b := q.Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "before must be an RFC 3339 time.", nil)
			return
		}
		before = &t
	}
	rows, err := s.Store.ListClaims(r.Context(), org.ID, env, q.Get("state"), before, limit+1)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{"data": rows}
	if len(rows) > limit {
		out["data"] = rows[:limit]
		out["next_before"] = rows[limit-1].CreatedAt.Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, out)
}
