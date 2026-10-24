package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/harrisyn/payahub/internal/access"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

// writes says which permission each console write needs. A session may
// read anything in its organisation; a write that is not listed here is
// refused, so a new route is closed until someone decides who may use it.
// API keys act for the whole organisation and skip this.
var writes = map[string]access.Perm{
	"POST /v1/claims":                                     access.WorkClaims,
	"POST /v1/claims/import":                              access.WorkClaims,
	"POST /v1/claims/validate":                            access.WorkClaims,
	"POST /v1/claims/{id}/submit":                         access.WorkClaims,
	"POST /v1/claims/{id}/review":                         access.WorkClaims,
	"POST /v1/claims/extract":                             access.WorkClaims,
	"POST /v1/items/suggest":                              access.Setup,
	"PUT /v1/organization/profile":                        access.Setup,
	"PUT /v1/payer-settings/{payer}/{env}":                access.Setup,
	"PUT /v1/connector-accounts/{connector}/{env}":        access.Setup,
	"POST /v1/connector-accounts/{connector}/{env}/check": access.Setup,
	"PUT /v1/item-mappings":                               access.Setup,
	"DELETE /v1/item-mappings/{code}":                     access.Setup,
	"PUT /v1/price-list":                                  access.Setup,
	"POST /v1/price-list/import":                          access.Setup,
	"DELETE /v1/price-list/{code}":                        access.Setup,
	"PUT /v1/organization/plan":                           access.Billing,
	"POST /v1/wallet/fundings":                            access.Billing,
	"POST /v1/wallet/payout-accounts":                     access.Billing,
	"DELETE /v1/wallet/payout-accounts/{id}":              access.Billing,
	"POST /v1/wallet/withdrawals":                         access.Billing,
	"POST /v1/api-keys":                                   access.Developers,
	"DELETE /v1/api-keys/{id}":                            access.Developers,
	"POST /v1/webhook-endpoints":                          access.Developers,
	"DELETE /v1/webhook-endpoints/{id}":                   access.Developers,
	"POST /v1/webhook-endpoints/{id}/ping":                access.Developers,
	"POST /v1/invitations":                                access.Team,
	"DELETE /v1/invitations/{id}":                         access.Team,
	"PUT /v1/members/{id}":                                access.Team,
	"DELETE /v1/members/{id}":                             access.Team,
}

// anyone lists writes every member may make.
var anyone = map[string]bool{"DELETE /v1/sessions/current": true}

// permitted reports whether a session may make this request. Operator routes
// have their own check.
func permitted(org store.Org, r *http.Request) bool {
	if !org.Session || r.Method == http.MethodGet || r.Method == http.MethodHead || anyone[r.Pattern] ||
		strings.HasPrefix(r.Pattern, r.Method+" /v1/operator/") {
		return true
	}
	p, ok := writes[r.Pattern]
	return ok && access.Can(org.Role, p)
}

var permText = map[access.Perm]string{
	access.WorkClaims: "work on claims",
	access.Setup:      "change the organisation's setup",
	access.Billing:    "manage billing and the wallet",
	access.Developers: "manage API keys and webhooks",
	access.Team:       "manage the team",
}

func forbidden(w http.ResponseWriter, r *http.Request) {
	msg := "Your role does not allow this. Ask an owner or admin."
	if p, ok := writes[r.Pattern]; ok {
		msg = "Your role does not let you " + permText[p] + ". Ask an owner or admin."
	}
	writeError(w, http.StatusForbidden, "not_permitted", msg, nil)
}

func (s *Server) teamRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/members", s.session(s.listMembers))
	mux.Handle("PUT /v1/members/{id}", s.session(s.putMember))
	mux.Handle("DELETE /v1/members/{id}", s.session(s.removeMember))
	mux.Handle("POST /v1/invitations", s.session(s.invite))
	mux.Handle("DELETE /v1/invitations/{id}", s.session(s.revokeInvitation))
	// The invitation link itself is the credential for these two.
	mux.HandleFunc("GET /v1/invitations/accept/{token}", s.viewInvitation)
	mux.HandleFunc("POST /v1/invitations/accept/{token}", s.acceptInvitation)
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	org := orgFrom(r.Context())
	members, err := s.Store.Members(r.Context(), org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	invites, err := s.Store.Invitations(r.Context(), org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": members, "invitations": invites, "roles": access.Roles})
}

func (s *Server) teamError(w http.ResponseWriter, r *http.Request, err error, notFound string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", notFound, nil)
	case errors.Is(err, store.ErrLastOwner):
		writeError(w, http.StatusConflict, "last_owner", "Make someone else an owner first: an organisation keeps at least one.", nil)
	case errors.Is(err, store.ErrNotAllowed):
		writeError(w, http.StatusForbidden, "not_permitted", "Only an owner can add, change or remove an owner.", nil)
	case errors.Is(err, store.ErrAlreadyMember):
		writeError(w, http.StatusConflict, "already_member", "That person is already in your team.", map[string]string{"email": "Already a member."})
	case errors.Is(err, store.ErrEmailTaken):
		writeError(w, http.StatusConflict, "email_taken", "That email already has a Payahub account with another organisation.", map[string]string{"email": "Used by another organisation."})
	default:
		s.internal(w, r, err)
	}
}

func roleField(w http.ResponseWriter, role access.Role) bool {
	if !access.Valid(role) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_role", "Choose a role.", map[string]string{"role": "Choose a role."})
		return false
	}
	return true
}

func (s *Server) putMember(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role access.Role `json:"role"`
	}
	if !decode(w, r, &in) || !roleField(w, in.Role) {
		return
	}
	if err := s.Store.SetRole(r.Context(), orgFrom(r.Context()), r.PathValue("id"), in.Role); err != nil {
		s.teamError(w, r, err, "No member "+r.PathValue("id")+".")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RemoveMember(r.Context(), orgFrom(r.Context()), r.PathValue("id")); err != nil {
		s.teamError(w, r, err, "No member "+r.PathValue("id")+".")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) invite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string      `json:"email"`
		Name  string      `json:"name"`
		Role  access.Role `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email, in.Name = strings.TrimSpace(in.Email), strings.Join(strings.Fields(in.Name), " ")
	if addr, err := mail.ParseAddress(in.Email); err != nil || addr.Address != in.Email || len(in.Email) > 254 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email", "Enter an email like ama@clinic.com.", map[string]string{"email": "Enter an email like ama@clinic.com."})
		return
	}
	if utf8.RuneCountInString(in.Name) > 120 {
		in.Name = string([]rune(in.Name)[:120])
	}
	if !roleField(w, in.Role) {
		return
	}
	org := orgFrom(r.Context())
	inv, err := queue.Invite(r.Context(), s.Store, s.Queue, store.NewInvitation{OrgID: org.ID, Email: in.Email, Name: in.Name, Role: in.Role, By: org}, s.ConsoleURL)
	if err != nil {
		s.teamError(w, r, err, "")
		return
	}
	writeJSON(w, http.StatusCreated, inv)
}

func (s *Server) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RevokeInvitation(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id")); err != nil {
		s.teamError(w, r, err, "No open invitation "+r.PathValue("id")+".")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const invitationGone = "This invitation has expired, been withdrawn or already been used. Ask for a new one."

func (s *Server) viewInvitation(w http.ResponseWriter, r *http.Request) {
	v, err := s.Store.InvitationByToken(r.Context(), r.PathValue("token"))
	if err != nil {
		s.teamError(w, r, err, invitationGone)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	problems := map[string]string{}
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		problems["name"] = "Enter your name."
	}
	if msg := passwordProblem(in.Password); msg != "" {
		problems["password"] = msg
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_fields", "Check the highlighted fields.", problems)
		return
	}
	token, err := s.Store.AcceptInvitation(r.Context(), r.PathValue("token"), in.Name, in.Password)
	if err != nil {
		s.teamError(w, r, err, invitationGone)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"session_token": token})
}
