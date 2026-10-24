package api_test

import (
	"context"
	"strings"
	"testing"
)

func TestMemberChecksAnswerAtTheDesk(t *testing.T) {
	e := setup(t)
	check := func(body map[string]any, headers ...string) (int, map[string]any) {
		t.Helper()
		body["payer_id"] = "sandbox"
		return e.do(t, "POST", "/v1/member-checks", body, headers...)
	}
	result := func(m map[string]any) map[string]any {
		r, _ := m["result"].(map[string]any)
		return r
	}

	s, ok := check(map[string]any{"member_number": "M-DESK-1"})
	if s != 200 || ok["status"] != "done" || result(ok)["eligible"] != true || result(ok)["plan_name"] != "Sandbox Gold" {
		t.Fatalf("an eligible member: %d %v", s, ok)
	}
	if s, m := e.do(t, "GET", "/v1/member-checks/"+ok["id"].(string), nil); s != 200 || m["id"] != ok["id"] {
		t.Fatalf("read back: %d %v", s, m)
	}

	if s, m := check(map[string]any{"member_number": "NOTFOUND-9"}); s != 200 || m["status"] != "failed" || m["error_code"] != "member_not_found" {
		t.Fatalf("an unknown member: %d %v", s, m)
	}
	if s, m := check(map[string]any{"member_number": "INACTIVE-2"}); s != 200 || result(m)["eligible"] != false || result(m)["reason"] != "inactive" {
		t.Fatalf("a lapsed member: %d %v", s, m)
	}

	// A payer that asks for a code: ask, have it sent, then check with it.
	if _, m := check(map[string]any{"member_number": "OTP-5"}); result(m)["otp_required"] != true {
		t.Fatalf("code needed: %v", m)
	}
	if _, m := check(map[string]any{"member_number": "OTP-5", "action": "send_otp"}); !strings.Contains(result(m)["challenge"].(string), "sent") {
		t.Fatalf("send code: %v", m)
	}
	if _, m := check(map[string]any{"member_number": "OTP-5", "otp": "123456"}); result(m)["eligible"] != true {
		t.Fatalf("with the code: %v", m)
	}
	var plain int
	if err := st.Pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = 'member_check' AND args::text LIKE '%123456%'`).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("the member's code is readable in the job table (%d, %v)", plain, err)
	}

	// Idempotent, and private to the organisation.
	_, first := check(map[string]any{"member_number": "M-DESK-2"}, "Idempotency-Key", "desk-2")
	if _, again := check(map[string]any{"member_number": "M-DESK-2"}, "Idempotency-Key", "desk-2"); again["id"] != first["id"] {
		t.Fatalf("a repeat made a second check: %v %v", first["id"], again["id"])
	}
	if s, _ := check(map[string]any{"member_number": "M-DESK-3"}, "Idempotency-Key", "desk-2"); s != 422 {
		t.Fatalf("a reused key with another body: %d", s)
	}
	other, _ := signup(t, e)
	if s, _ := other.do(t, "GET", "/v1/member-checks/"+ok["id"].(string), nil); s != 404 {
		t.Fatalf("another organisation read the check: %d", s)
	}
	if s, m := e.do(t, "POST", "/v1/member-checks", map[string]any{"payer_id": "nobody", "member_number": ""}); s != 422 {
		t.Fatalf("bad input: %d %v", s, m)
	}
}
