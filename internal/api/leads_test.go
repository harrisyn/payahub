package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/mail"
	"github.com/harrisyn/payahub/internal/store"
)

func TestLeadsAreSavedOnceAndOnlyOperatorsReadThem(t *testing.T) {
	e := setup(t)
	public := env{srv: e.srv}

	if s, out := public.do(t, "POST", "/v1/leads", map[string]any{"kind": "contact", "email": "nope"}); s != 422 ||
		len(out["error"].(map[string]any)["details"].(map[string]any)) != 3 {
		t.Fatalf("bad contact lead: %d %v", s, out)
	}
	if s, out := public.do(t, "POST", "/v1/leads", map[string]any{"kind": "payer", "email": "a@b.co", "phone": "024 123 4567"}); s != 422 ||
		out["error"].(map[string]any)["details"].(map[string]any)["payer_name"] == nil {
		t.Fatalf("payer lead without a payer: %d %v", s, out)
	}
	// A developer asking for the spec need not leave a number.
	if s, out := public.do(t, "POST", "/v1/leads", map[string]any{"kind": "specs", "email": "dev@hms.test"}); s != 201 {
		t.Fatalf("specs lead: %d %v", s, out)
	}

	key := store.NewID("lead-test")
	body := map[string]any{"kind": "payer", "email": "ama@ridge.test", "phone": "+233 (24) 123-4567",
		"payer_name": "Sample Health", "monthly_claims": 420, "payer_contact": "Claims desk, 030 000 0000"}
	s, first := public.do(t, "POST", "/v1/leads", body, "Idempotency-Key", key)
	if s != 201 {
		t.Fatalf("payer lead: %d %v", s, first)
	}
	s, again := public.do(t, "POST", "/v1/leads", body, "Idempotency-Key", key)
	if s != 200 || again["id"] != first["id"] {
		t.Fatalf("replay made a second lead: %d %v vs %v", s, again, first)
	}

	ses, _ := signup(t, e)
	if s, _ := ses.do(t, "GET", "/v1/operator/leads", nil); s != 403 {
		t.Fatalf("a clinic read the leads: %d", s)
	}
	op := operatorSession(t, e)
	s, list := op.do(t, "GET", "/v1/operator/leads?kind=payer", nil)
	if s != 200 {
		t.Fatalf("operator leads: %d %v", s, list)
	}
	found := 0
	for _, l := range list["data"].([]any) {
		l := l.(map[string]any)
		if l["id"] == first["id"] {
			found++
			if l["phone"] != "+233241234567" || l["monthly_claims"].(float64) != 420 || l["payer_name"] != "Sample Health" {
				t.Fatalf("lead saved wrong: %v", l)
			}
		}
		if l["kind"] != "payer" {
			t.Fatalf("filter let through %v", l["kind"])
		}
	}
	if found != 1 {
		t.Fatalf("lead listed %d times", found)
	}
}

func TestSpecIsPublic(t *testing.T) {
	e := setup(t)
	res, err := http.Get(e.srv.URL + "/v1/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.HasPrefix(string(b), "openapi:") {
		t.Fatalf("spec: %d %.40q", res.StatusCode, b)
	}
}

func TestOperatorsAreEmailedANewEnquiryOnce(t *testing.T) {
	e := setup(t)
	_, acct := signup(t, e)
	addr := acct["email"].(string)
	if err := st.SetOperator(context.Background(), addr, true); err != nil {
		t.Fatal(err)
	}
	key := store.NewID("lead-mail")
	body := map[string]any{"kind": "contact", "name": "Kofi\r\nBcc: x@evil.test", "email": "kofi@ridge.test",
		"phone": "024 555 0101", "organization": "Ridge Clinic " + key, "message": "We send 300 claims a month."}
	for range 2 {
		if s, out := (env{srv: e.srv}).do(t, "POST", "/v1/leads", body, "Idempotency-Key", key); s != 201 && s != 200 {
			t.Fatalf("lead: %d %v", s, out)
		}
	}
	var got []mail.Message
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		got = nil
		for _, m := range testMail.to(addr) {
			if strings.Contains(m.Subject, key) {
				got = append(got, m)
			}
		}
		if len(got) > 0 {
			break
		}
	}
	if len(got) != 1 {
		t.Fatalf("%d alerts for one enquiry", len(got))
	}
	m := got[0]
	if m.Subject != "New enquiry from Ridge Clinic "+key || !strings.Contains(m.Text, "Phone: +233") && !strings.Contains(m.Text, "Phone: 0245550101") ||
		!strings.Contains(m.Text, "We send 300 claims a month.") || !strings.Contains(m.Text, "kofi@ridge.test") {
		t.Fatalf("alert: %q\n%s", m.Subject, m.Text)
	}
	time.Sleep(300 * time.Millisecond)
	n := 0
	for _, m := range testMail.to(addr) {
		if strings.Contains(m.Subject, key) {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("the replay sent another alert: %d", n)
	}
}
