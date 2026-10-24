package api_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/store"
)

var joinLink = regexp.MustCompile(`/join/(inv_[A-Za-z0-9_-]+)`)

// invite asks for an invitation and returns the token from its email.
func invite(t *testing.T, by env, role string) (email, token string) {
	t.Helper()
	email = strings.ToLower(store.NewID("m")) + "@clinic.test"
	s, out := by.do(t, "POST", "/v1/invitations", map[string]any{"email": email, "name": "Kofi Mensah", "role": role})
	if s != 201 {
		t.Fatalf("invite %s: %d %v", role, s, out)
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got := testMail.to(email); len(got) > 0 {
			m := joinLink.FindStringSubmatch(got[len(got)-1].Text)
			if m == nil {
				t.Fatalf("no link in %q", got[0].Text)
			}
			return email, m[1]
		}
	}
	t.Fatal("the invitation email never went out")
	return
}

func accept(t *testing.T, e env, token string) env {
	t.Helper()
	s, out := e.do(t, "POST", "/v1/invitations/accept/"+token, map[string]any{"name": "Kofi Mensah", "password": "a long enough password"})
	if s != 201 {
		t.Fatalf("accept: %d %v", s, out)
	}
	return env{srv: e.srv, key: out["session_token"].(string)}
}

func me(t *testing.T, e env) map[string]any {
	t.Helper()
	_, out := e.do(t, "GET", "/v1/organization", nil)
	return out["organization"].(map[string]any)["user"].(map[string]any)
}

func TestTeamMembersHaveRoles(t *testing.T) {
	e := setup(t)
	owner, _ := signup(t, e)
	if u := me(t, owner); u["role"] != "owner" {
		t.Fatalf("whoever signs up owns the organisation: %v", u)
	}

	// An invitation is emailed, shows who it is from, and works once.
	staffEmail, token := invite(t, owner, "claims")
	if s, v := e.do(t, "GET", "/v1/invitations/accept/"+token, nil); s != 200 || v["organization"] != "Ridge Clinic" || v["role"] != "claims" {
		t.Fatalf("view: %d %v", s, v)
	}
	if s, out := e.do(t, "POST", "/v1/invitations/accept/"+token, map[string]any{"name": "Kofi", "password": "short"}); s != 422 || out["error"].(map[string]any)["details"].(map[string]any)["password"] == nil {
		t.Fatalf("a short password: %d %v", s, out)
	}
	staff := accept(t, e, token)
	if s, _ := e.do(t, "POST", "/v1/invitations/accept/"+token, map[string]any{"name": "Kofi", "password": "a long enough password"}); s != 404 {
		t.Fatalf("a used invitation worked again: %d", s)
	}
	u := me(t, staff)
	if u["role"] != "claims" || !slices.Equal(u["permissions"].([]any), []any{"claims"}) {
		t.Fatalf("claims member: %v", u)
	}

	// Claims staff work on claims, and only that.
	if s, out := staff.do(t, "POST", "/v1/claims", claimBody("M-TEAM-1")); s != 201 {
		t.Fatalf("claims staff create a claim: %d %v", s, out)
	}
	for _, try := range []struct{ method, path string }{
		{"POST", "/v1/api-keys"},
		{"POST", "/v1/invitations"},
		{"POST", "/v1/wallet/withdrawals"},
		{"PUT", "/v1/organization/plan"},
	} {
		if s, out := staff.do(t, try.method, try.path, map[string]any{}); s != 403 || out["error"].(map[string]any)["code"] != "not_permitted" {
			t.Errorf("claims staff %s %s: %d %v", try.method, try.path, s, out)
		}
	}
	if s, _ := staff.do(t, "GET", "/v1/api-keys", nil); s != 200 {
		t.Errorf("every member reads: %d", s)
	}

	// An admin manages the team but cannot touch owners.
	_, list := owner.do(t, "GET", "/v1/members", nil)
	var ownerID, staffID string
	for _, m := range list["data"].([]any) {
		m := m.(map[string]any)
		if m["role"] == "owner" {
			ownerID = m["id"].(string)
		} else {
			staffID = m["id"].(string)
		}
	}
	if s, _ := owner.do(t, "PUT", "/v1/members/"+staffID, map[string]any{"role": "admin"}); s != 204 {
		t.Fatalf("promote: %d", s)
	}
	admin := staff
	if s, _ := admin.do(t, "PUT", "/v1/members/"+ownerID, map[string]any{"role": "viewer"}); s != 403 {
		t.Errorf("an admin demoted an owner: %d", s)
	}
	if s, _ := admin.do(t, "POST", "/v1/invitations", map[string]any{"email": "x@clinic.test", "role": "owner"}); s != 403 {
		t.Errorf("an admin invited an owner: %d", s)
	}
	if s, _ := admin.do(t, "PUT", "/v1/members/"+staffID, map[string]any{"role": "superuser"}); s != 422 {
		t.Errorf("an unknown role: %d", s)
	}

	// The last owner stays.
	if s, out := owner.do(t, "PUT", "/v1/members/"+ownerID, map[string]any{"role": "admin"}); s != 409 || out["error"].(map[string]any)["code"] != "last_owner" {
		t.Errorf("the last owner stepped down: %d %v", s, out)
	}
	if s, _ := owner.do(t, "DELETE", "/v1/members/"+ownerID, nil); s != 409 {
		t.Errorf("the last owner was removed: %d", s)
	}

	// A viewer reads but changes nothing.
	_, vt := invite(t, admin, "viewer")
	viewer := accept(t, e, vt)
	if s, _ := viewer.do(t, "GET", "/v1/claims", nil); s != 200 {
		t.Errorf("viewer reads claims: %d", s)
	}
	if s, _ := viewer.do(t, "POST", "/v1/claims", claimBody("M-TEAM-2")); s != 403 {
		t.Errorf("viewer made a claim: %d", s)
	}

	// Another organisation cannot touch this team, or invite its people.
	other, otherAcct := signup(t, e)
	if s, _ := other.do(t, "PUT", "/v1/members/"+staffID, map[string]any{"role": "viewer"}); s != 404 {
		t.Errorf("another organisation changed a member: %d", s)
	}
	if s, _ := owner.do(t, "POST", "/v1/invitations", map[string]any{"email": otherAcct["email"], "role": "viewer"}); s != 409 {
		t.Errorf("invited someone from another organisation: %d", s)
	}

	// A removed member is signed out at once, and can be invited back as the same person.
	if s, _ := owner.do(t, "DELETE", "/v1/members/"+staffID, nil); s != 204 {
		t.Fatalf("remove: %d", s)
	}
	if s, _ := admin.do(t, "GET", "/v1/claims", nil); s != 401 {
		t.Errorf("a removed member's session still works: %d", s)
	}
	_, list = owner.do(t, "GET", "/v1/members", nil)
	if len(list["data"].([]any)) != 2 {
		t.Errorf("members after removal: %v", list["data"])
	}
	s, out := owner.do(t, "POST", "/v1/invitations", map[string]any{"email": staffEmail, "role": "finance"})
	if s != 201 {
		t.Fatalf("invite back: %d %v", s, out)
	}
	msgs := testMail.to(staffEmail)
	for deadline := time.Now().Add(10 * time.Second); len(msgs) < 2 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		msgs = testMail.to(staffEmail)
	}
	back := accept(t, e, joinLink.FindStringSubmatch(msgs[len(msgs)-1].Text)[1])
	if u := me(t, back); u["id"] != staffID || u["role"] != "finance" {
		t.Errorf("came back as %v", u)
	}
}
