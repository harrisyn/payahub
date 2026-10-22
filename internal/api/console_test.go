package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/store"
)

func signup(t *testing.T, e env) (session env, account map[string]any) {
	t.Helper()
	email := strings.ToLower(store.NewID("u")) + "@clinic.test"
	status, out := e.do(t, "POST", "/v1/signup", map[string]any{
		"email": email, "password": "correct horse battery", "name": "Ama Owusu",
		"organization": "Ridge Clinic", "country": "gh",
	})
	if status != 201 {
		t.Fatalf("signup: %d %v", status, out)
	}
	out["email"] = email
	return env{srv: e.srv, key: out["session_token"].(string)}, out
}

func TestSignupLoginAndSessionOnlyRoutes(t *testing.T) {
	e := setup(t)
	ses, acct := signup(t, e)

	if s, out := e.do(t, "POST", "/v1/signup", map[string]any{"email": "nope", "password": "short"}); s != 422 ||
		len(out["error"].(map[string]any)["details"].(map[string]any)) != 4 {
		t.Fatalf("bad signup: %d %v", s, out)
	}
	if s, _ := e.do(t, "POST", "/v1/signup", map[string]any{
		"email": acct["email"], "password": "another long one", "name": "B", "organization": "C",
	}); s != 409 {
		t.Fatalf("duplicate email: %d", s)
	}
	if s, _ := e.do(t, "POST", "/v1/sessions", map[string]any{"email": acct["email"], "password": "wrong password"}); s != 401 {
		t.Fatalf("wrong password: %d", s)
	}
	s, login := e.do(t, "POST", "/v1/sessions", map[string]any{"email": strings.ToUpper(acct["email"].(string)), "password": "correct horse battery"})
	if s != 201 || !strings.HasPrefix(login["session_token"].(string), "ses_") {
		t.Fatalf("login: %d %v", s, login)
	}

	// The sandbox key from sign-up works on the claims API but not on console routes.
	key := env{srv: e.srv, key: acct["sandbox_key"].(string)}
	if s, _ := key.do(t, "GET", "/v1/claims", nil); s != 200 {
		t.Fatalf("sandbox key on claims: %d", s)
	}
	if s, _ := key.do(t, "POST", "/v1/api-keys", map[string]any{"environment": "live"}); s != 403 {
		t.Fatalf("API key minted a key: %d", s)
	}

	s, org := ses.do(t, "GET", "/v1/organization", nil)
	if s != 200 {
		t.Fatalf("organization: %d %v", s, org)
	}
	o := org["organization"].(map[string]any)
	if o["plan"] != "trial" || o["country"] != "GH" || o["user"].(map[string]any)["name"] != "Ama Owusu" {
		t.Fatalf("organization = %v", o)
	}
	steps := org["onboarding"].([]any)
	if len(steps) != 6 || steps[0].(map[string]any)["done"] != true || steps[1].(map[string]any)["done"] != false {
		t.Fatalf("steps = %v", steps)
	}

	if s, _ := ses.do(t, "DELETE", "/v1/sessions/current", nil); s != 204 {
		t.Fatalf("logout: %d", s)
	}
	if s, _ := ses.do(t, "GET", "/v1/organization", nil); s != 401 {
		t.Fatalf("session still works after logout: %d", s)
	}
}

func TestProfileRecommendsAMethod(t *testing.T) {
	ses, _ := signup(t, setup(t))
	s, out := ses.do(t, "PUT", "/v1/organization/profile", map[string]any{
		"billing_system": "spreadsheets", "claims_leave_by": "email", "monthly_claims": 400,
		"has_developers": false, "payers": []string{"glico", "sandbox"},
	})
	rec := out["recommendation"].(map[string]any)
	if s != 200 || rec["method"] != "batch" || rec["available"] != false || rec["fallback"] != "rest" {
		t.Fatalf("profile: %d %v", s, out)
	}
	if s, _ := ses.do(t, "PUT", "/v1/organization/profile", map[string]any{
		"billing_system": "emr", "claims_leave_by": "portal", "payers": []string{"nobody"},
	}); s != 422 {
		t.Fatalf("unknown payer accepted: %d", s)
	}
	_, org := ses.do(t, "GET", "/v1/organization", nil)
	if org["recommendation"].(map[string]any)["method"] != "batch" {
		t.Fatalf("recommendation not kept: %v", org["recommendation"])
	}
}

func TestPlatformAccountsAreSealedAndChecked(t *testing.T) {
	ses, _ := signup(t, setup(t))
	// GLICO sits behind XCeli: the organisation turns GLICO on and says it
	// has an XCeli account, then enters that account once.
	s, out := ses.do(t, "PUT", "/v1/payer-settings/glico/live", map[string]any{"enabled": true, "account": "have"})
	if s != 200 || out["data"].([]any)[0].(map[string]any)["account"] != "have" {
		t.Fatalf("payer setting: %d %v", s, out)
	}
	creds := map[string]any{
		"auth_base_url": "https://auth.example", "username": "ridge", "password": "hunter2-very-secret",
		"provider_id": "269", "provider_name": "Ridge",
	}
	if s, out := ses.do(t, "PUT", "/v1/connector-accounts/xceli/live", map[string]any{
		"credentials": map[string]any{"username": "only-this"},
	}); s != 422 || out["error"].(map[string]any)["code"] != "missing_credentials" {
		t.Fatalf("missing credentials accepted: %d %v", s, out)
	}
	if s, out := ses.do(t, "PUT", "/v1/connector-accounts/xceli/live", map[string]any{
		"credentials": map[string]any{"insurer_api_url": "https://x"},
	}); s != 422 || out["error"].(map[string]any)["code"] != "unknown_credential" {
		t.Fatalf("payer field accepted as an account credential: %d %v", s, out)
	}
	s, out = ses.do(t, "PUT", "/v1/connector-accounts/xceli/live", map[string]any{"credentials": creds})
	if s != 200 || len(out["data"].([]any)[0].(map[string]any)["credential_fields"].([]any)) != 5 {
		t.Fatalf("save: %d %v", s, out)
	}
	var sealed []byte
	if err := st.Pool.QueryRow(context.Background(),
		`SELECT credentials_sealed FROM connector_accounts WHERE connector = 'xceli' ORDER BY updated_at DESC LIMIT 1`).
		Scan(&sealed); err != nil || len(sealed) == 0 || strings.Contains(string(sealed), "hunter2") {
		t.Fatalf("credentials not sealed: %v", err)
	}
	s, check := ses.do(t, "POST", "/v1/connector-accounts/xceli/live/check", nil)
	if s != 200 || check["ok"] != false || !strings.Contains(check["message"].(string), "still being connected") {
		t.Fatalf("check xceli: %d %v", s, check)
	}

	// One Sandbox Exchange account serves both payers behind it.
	ses.do(t, "PUT", "/v1/payer-settings/sandbox_life/sandbox", map[string]any{"enabled": true, "account": "have"})
	if s, _ := ses.do(t, "POST", "/v1/connector-accounts/sandbox_exchange/sandbox/check", nil); s != 422 {
		t.Fatalf("check without an account: %d", s)
	}
	ses.do(t, "PUT", "/v1/connector-accounts/sandbox_exchange/sandbox", map[string]any{
		"credentials": map[string]any{"username": "ridge", "password": "any"},
	})
	s, check = ses.do(t, "POST", "/v1/connector-accounts/sandbox_exchange/sandbox/check", nil)
	if s != 200 || check["ok"] != true || !strings.Contains(check["message"].(string), "Sandbox Life") {
		t.Fatalf("check exchange: %d %v", s, check)
	}
	// The standalone sandbox payer needs no account at all.
	if s, check := ses.do(t, "POST", "/v1/connector-accounts/sandbox/sandbox/check", nil); s != 200 || check["ok"] != true {
		t.Fatalf("check sandbox: %d %v", s, check)
	}
	if s, _ := ses.do(t, "PUT", "/v1/payer-settings/nobody/live", map[string]any{"enabled": true}); s != 404 {
		t.Fatalf("unknown payer: %d", s)
	}
	if s, _ := ses.do(t, "PUT", "/v1/payer-settings/glico/live", map[string]any{"enabled": true, "account": "maybe"}); s != 422 {
		t.Fatalf("bad account answer: %d", s)
	}
	if s, _ := ses.do(t, "PUT", "/v1/connector-accounts/nobody/live", map[string]any{"credentials": map[string]any{}}); s != 404 {
		t.Fatalf("unknown connector: %d", s)
	}
}

func TestAPIKeysAndConsoleClaims(t *testing.T) {
	e := setup(t)
	ses, _ := signup(t, e)
	s, k := ses.do(t, "POST", "/v1/api-keys", map[string]any{"environment": "live", "name": "Front desk"})
	if s != 201 || !strings.HasPrefix(k["key"].(string), "sk_live_") {
		t.Fatalf("create key: %d %v", s, k)
	}
	_, list := ses.do(t, "GET", "/v1/api-keys", nil)
	if n := len(list["data"].([]any)); n != 2 {
		t.Fatalf("want sandbox + live keys, got %d", n)
	}
	for _, row := range list["data"].([]any) {
		if _, has := row.(map[string]any)["key"]; has {
			t.Fatal("listing returned a raw key")
		}
	}
	if s, _ := ses.do(t, "DELETE", "/v1/api-keys/"+k["id"].(string), nil); s != 204 {
		t.Fatalf("revoke: %d", s)
	}

	// A session sends sandbox claims, and the console lists them.
	id := ses.create(t, "M-1001")
	ses.do(t, "POST", "/v1/claims/"+id+"/submit", nil)
	ses.waitFor(t, id, claim.Acknowledged)
	_, claims := ses.do(t, "GET", "/v1/claims?environment=sandbox&limit=1", nil)
	rows := claims["data"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["state"] != "acknowledged" || rows[0].(map[string]any)["member_number"] != "M-1001" {
		t.Fatalf("claims = %v", claims)
	}
	_, org := ses.do(t, "GET", "/v1/organization", nil)
	if org["onboarding"].([]any)[4].(map[string]any)["done"] != true {
		t.Fatalf("first claim step not done: %v", org["onboarding"])
	}
	if s, plan := ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "enterprise"}); s != 200 || plan["plan"] != "enterprise_requested" {
		t.Fatalf("plan: %d %v", s, plan)
	}
}

func TestTrialLimitsLiveClaims(t *testing.T) {
	e := setup(t)
	ses, acct := signup(t, e)
	_, k := ses.do(t, "POST", "/v1/api-keys", map[string]any{"environment": "live"})
	live := env{srv: e.srv, key: k["key"].(string)}
	if _, err := st.Pool.Exec(context.Background(), `UPDATE organizations SET trial_ends_at = $2 WHERE id = $1`,
		acct["organization_id"], time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	id := live.create(t, "M-1001")
	if s, out := live.do(t, "POST", "/v1/claims/"+id+"/submit", nil); s != 402 || out["error"].(map[string]any)["code"] != "trial_limit" {
		t.Fatalf("expired trial sent a live claim: %d %v", s, out)
	}
	ses.do(t, "PUT", "/v1/organization/plan", map[string]any{"plan": "volume"})
	if s, _ := live.do(t, "POST", "/v1/claims/"+id+"/submit", nil); s != 202 {
		t.Fatalf("paid plan blocked: %d", s)
	}
	live.waitFor(t, id, claim.Acknowledged)
}
