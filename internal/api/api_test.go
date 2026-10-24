package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/api"
	"github.com/harrisyn/payahub/internal/claim"
	_ "github.com/harrisyn/payahub/internal/connector/all"
	"github.com/harrisyn/payahub/internal/connector/sandbox"
	"github.com/harrisyn/payahub/internal/mail"
	"github.com/harrisyn/payahub/internal/payment"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/vault"
)

// These tests need Postgres: set PAYAHUB_TEST_DATABASE_URL (CI does).
type env struct {
	srv *httptest.Server
	key string
}

var (
	setupOnce sync.Once
	shared    *httptest.Server
	st        *store.Store
	setupErr  error
)

func setup(t testing.TB) env {
	t.Helper()
	url := os.Getenv("PAYAHUB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PAYAHUB_TEST_DATABASE_URL not set")
	}
	setupOnce.Do(func() {
		ctx := context.Background()
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		if st, setupErr = store.Open(ctx, url); setupErr != nil {
			return
		}
		if setupErr = st.Migrate(ctx); setupErr != nil {
			return
		}
		// Running migrations twice must be harmless.
		if setupErr = st.Migrate(ctx); setupErr != nil {
			return
		}
		if setupErr = st.RefreshCatalog(ctx); setupErr != nil {
			return
		}
		v, err := vault.New(vault.NewKey())
		if setupErr = err; err != nil {
			return
		}
		rc, err := queue.NewClient(st, v, 200, logger, testMail)
		if setupErr = err; err != nil {
			return
		}
		if setupErr = rc.Start(ctx); setupErr != nil {
			return
		}
		payment.Register(&payment.Sandbox{Amount: st.SandboxAmount})
		shared = httptest.NewServer((&api.Server{Store: st, Queue: rc, Vault: v, Logger: logger, OCR: formOCR{}}).Handler())
	})
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	// A fresh organisation per test keeps tests independent on a shared database.
	org := store.NewID("org")
	key := "sk_test_" + org
	if err := st.EnsureOrgWithKey(context.Background(), org, t.Name(), "sandbox", key); err != nil {
		t.Fatal(err)
	}
	return env{srv: shared, key: key}
}

func (e env) do(t testing.TB, method, path string, body any, headers ...string) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	req.Header.Set("Authorization", "Bearer "+e.key)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func claimBody(member string) map[string]any {
	return map[string]any{
		"payer_id":  "sandbox",
		"member":    map[string]any{"member_number": member},
		"provider":  map[string]any{"name": "Ridge Clinic"},
		"encounter": map[string]any{"start_date": "2026-10-20"},
		"lines": []any{map[string]any{
			"description": "Consultation", "unit_price": 15000,
			"mapping": map[string]any{
				"source": map[string]any{"system": "local", "value": "CONS"},
				"target": map[string]any{"system": "payer:sandbox", "value": "C01"},
			},
		}},
		"diagnoses": []any{map[string]any{"code": map[string]any{"system": "icd-10", "value": "B54"}, "role": "primary"}},
	}
}

func (e env) create(t testing.TB, member string) string {
	t.Helper()
	status, out := e.do(t, "POST", "/v1/claims", claimBody(member))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	return out["id"].(string)
}

// waitFor polls the claim until it reaches want.
func (e env) waitFor(t testing.TB, id string, want claim.State) map[string]any {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, c := e.do(t, "GET", "/v1/claims/"+id, nil)
		if c["state"] == string(want) {
			return c
		}
		time.Sleep(50 * time.Millisecond)
	}
	_, c := e.do(t, "GET", "/v1/claims/"+id, nil)
	t.Fatalf("claim %s never reached %s; last %v", id, want, c["state"])
	return nil
}

func eventKinds(t testing.TB, e env, id string) []string {
	_, out := e.do(t, "GET", "/v1/claims/"+id+"/events", nil)
	var kinds []string
	for _, ev := range out["data"].([]any) {
		kinds = append(kinds, ev.(map[string]any)["kind"].(string))
	}
	return kinds
}

func TestAuthRequired(t *testing.T) {
	e := setup(t)
	e.key = ""
	if status, out := e.do(t, "POST", "/v1/claims", claimBody("M1")); status != 401 {
		t.Fatalf("got %d %v", status, out)
	}
	e.key = "sk_wrong"
	if status, _ := e.do(t, "GET", "/v1/claims/x", nil); status != 401 {
		t.Fatalf("got %d", status)
	}
}

func TestCreateValidationAndIdempotency(t *testing.T) {
	e := setup(t)
	bad := claimBody("M1")
	delete(bad, "provider")
	if status, out := e.do(t, "POST", "/v1/claims", bad); status != 422 || out["error"].(map[string]any)["code"] != "invalid_claim" {
		t.Fatalf("got %d %v", status, out)
	}

	s1, first := e.do(t, "POST", "/v1/claims", claimBody("M1"), "Idempotency-Key", "k1")
	s2, again := e.do(t, "POST", "/v1/claims", claimBody("M1"), "Idempotency-Key", "k1")
	if s1 != 201 || s2 != 200 || first["id"] != again["id"] {
		t.Fatalf("replay: %d %d %v %v", s1, s2, first["id"], again["id"])
	}
	if s, out := e.do(t, "POST", "/v1/claims", claimBody("M2"), "Idempotency-Key", "k1"); s != 422 {
		t.Fatalf("conflict: %d %v", s, out)
	}
}

func TestConcurrentDuplicatesCreateOneClaim(t *testing.T) {
	e := setup(t)
	ids := make(chan string, 10)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_, out := e.do(t, "POST", "/v1/claims", claimBody("M1"), "Idempotency-Key", "same")
			ids <- out["id"].(string)
		})
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("expected one claim, got %d", len(seen))
	}
}

func TestSubmitEndToEnd(t *testing.T) {
	e := setup(t)
	id := e.create(t, "M-1001")
	status, out := e.do(t, "POST", "/v1/claims/"+id+"/submit", nil)
	if status != 202 {
		t.Fatalf("submit: %d %v", status, out)
	}
	c := e.waitFor(t, id, claim.Acknowledged)
	if c["payer_claim_number"] != sandbox.ClaimNumber(id) {
		t.Errorf("payer claim number %v", c["payer_claim_number"])
	}
	// Submitting again is a no-op, not a second payer submission.
	if status, _ := e.do(t, "POST", "/v1/claims/"+id+"/submit", nil); status != 202 {
		t.Errorf("resubmit: %d", status)
	}
	if n := sandbox.Sends(id); n != 1 {
		t.Errorf("sent %d times", n)
	}
	want := []string{"created", "submission_queued", "submission_started", "acknowledged"}
	if got := eventKinds(t, e, id); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events %v", got)
	}
}

func TestSubmitRejectedByPayer(t *testing.T) {
	e := setup(t)
	id := e.create(t, "REJECT-1")
	e.do(t, "POST", "/v1/claims/"+id+"/submit", nil)
	e.waitFor(t, id, claim.Rejected)
}

func TestValidationFailureIsNotQueued(t *testing.T) {
	e := setup(t)
	body := claimBody("M-1")
	body["diagnoses"] = []any{}
	status, out := e.do(t, "POST", "/v1/claims", body)
	if status != 201 {
		t.Fatal(out)
	}
	id := out["id"].(string)
	status, out = e.do(t, "POST", "/v1/claims/"+id+"/submit", nil)
	if status != 422 || out["error"].(map[string]any)["code"] != "not_submittable" {
		t.Fatalf("got %d %v", status, out)
	}
}

// A timeout before the payer saw the claim: retried and delivered once.
func TestTransportFailureIsRetried(t *testing.T) {
	e := setup(t)
	id := e.create(t, "FLAKY-1")
	e.do(t, "POST", "/v1/claims/"+id+"/submit", nil)
	e.waitFor(t, id, claim.Acknowledged)
	if n := sandbox.Sends(id); n != 2 {
		t.Errorf("expected one failed and one good send, got %d", n)
	}
}

// The payer accepted the claim but the reply was lost: the retry finds the
// claim at the payer instead of submitting it a second time.
func TestLostReplyReconcilesWithoutDuplicate(t *testing.T) {
	e := setup(t)
	id := e.create(t, "LOSTREPLY-1")
	e.do(t, "POST", "/v1/claims/"+id+"/submit", nil)
	e.waitFor(t, id, claim.Acknowledged)
	if n := sandbox.Sends(id); n != 1 {
		t.Errorf("payer received the claim %d times", n)
	}
	kinds := eventKinds(t, e, id)
	if fmt.Sprint(kinds) != fmt.Sprint([]string{"created", "submission_queued", "submission_started", "attempt_failed", "acknowledged"}) {
		t.Errorf("events %v", kinds)
	}
}

// TestThroughput pushes PAYAHUB_LOAD claims through create, submit and
// delivery to the sandbox payer, and reports the rate. Skipped by default.
func TestThroughput(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("PAYAHUB_LOAD"))
	if n == 0 {
		t.Skip("set PAYAHUB_LOAD to run")
	}
	e := setup(t)
	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 200

	ids := make([]string, n)
	start := time.Now()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 100)
	for i := range n {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			ids[i] = e.create(t, "M-"+strconv.Itoa(i))
			if s, out := e.do(t, "POST", "/v1/claims/"+ids[i]+"/submit", nil); s != 202 {
				t.Errorf("submit %d %v", s, out)
			}
		})
	}
	wg.Wait()
	accepted := time.Since(start)

	ctx := context.Background()
	for {
		var pending int
		_ = st.Pool.QueryRow(ctx, `SELECT count(*) FROM claims WHERE id = ANY($1) AND state <> 'acknowledged'`, ids).Scan(&pending)
		if pending == 0 {
			break
		}
		if time.Since(start) > 5*time.Minute {
			t.Fatalf("%d claims still pending", pending)
		}
		time.Sleep(100 * time.Millisecond)
	}
	done := time.Since(start)
	t.Logf("%d claims: accepted (create+submit) in %v = %.0f/s; all acknowledged in %v = %.0f/s",
		n, accepted.Round(time.Millisecond), float64(n)/accepted.Seconds(), done.Round(time.Millisecond), float64(n)/done.Seconds())
}

// testMail keeps every email the workers send.
var testMail = &mailbox{}

type mailbox struct {
	mu   sync.Mutex
	sent []mail.Message
}

func (m *mailbox) Send(_ context.Context, msg mail.Message) error {
	if err := mail.Check(msg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

// to returns the messages sent to addr so far.
func (m *mailbox) to(addr string) []mail.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []mail.Message
	for _, msg := range m.sent {
		if slices.Contains(msg.To, addr) {
			out = append(out, msg)
		}
	}
	return out
}
