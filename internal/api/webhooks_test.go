package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/webhook"
)

type received struct {
	mu   sync.Mutex
	got  []map[string]any
	bad  int
	fail bool
}

func (rc *received) types(claimID string) []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var out []string
	for _, ev := range rc.got {
		if d, _ := ev["data"].(map[string]any); d != nil && d["claim_id"] == claimID {
			out = append(out, ev["type"].(string))
		}
	}
	return out
}

func TestClinicSystemsHearEveryClaimEvent(t *testing.T) {
	e := setup(t)
	rc := &received{}
	var secret string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("webhook-timestamp"), 10, 64)
		want, _ := webhook.Sign(secret, r.Header.Get("webhook-id"), ts, body)
		rc.mu.Lock()
		defer rc.mu.Unlock()
		if rc.fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Header.Get("webhook-signature") != want || time.Since(time.Unix(ts, 0)) > time.Minute {
			rc.bad++
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var ev map[string]any
		_ = json.Unmarshal(body, &ev)
		if ev["id"] != r.Header.Get("webhook-id") {
			rc.bad++
		}
		rc.got = append(rc.got, ev)
	}))
	defer hook.Close()

	if s, out := e.do(t, "POST", "/v1/webhook-endpoints", map[string]any{"url": "ftp://x"}); s != 422 {
		t.Fatalf("bad url: %d %v", s, out)
	}
	s, made := e.do(t, "POST", "/v1/webhook-endpoints", map[string]any{"url": hook.URL + "/payahub", "description": "HMS"})
	if s != 201 {
		t.Fatalf("endpoint: %d %v", s, made)
	}
	secret = made["secret"].(string)
	ep := made["endpoint"].(map[string]any)
	if ep["environment"] != "sandbox" {
		t.Fatalf("an API key's endpoint is for its own environment: %v", ep)
	}

	// Create, send and settle a claim: every step reaches the clinic's system.
	s, out := e.do(t, "POST", "/v1/claims?submit=true&wait=10", claimBody("M-HOOK-1"))
	if s != 200 {
		t.Fatalf("send: %d %v", s, out)
	}
	id := out["claim"].(map[string]any)["id"].(string)
	var got []string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got = rc.types(id); slices.Contains(got, "claim.acknowledged") {
			break
		}
	}
	for _, want := range []string{"claim.created", "claim.submission_queued", "claim.submission_started", "claim.acknowledged"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	rc.mu.Lock()
	for _, ev := range rc.got {
		if ev["type"] == "claim.acknowledged" {
			if d := ev["data"].(map[string]any); d["state"] != "acknowledged" || d["payer_claim_number"] == nil {
				t.Errorf("acknowledged event: %v", ev)
			}
		}
	}
	if rc.bad > 0 {
		t.Errorf("%d deliveries were badly signed", rc.bad)
	}
	rc.mu.Unlock()

	// A ping, and the delivery log.
	epID := ep["id"].(string)
	if s, _ := e.do(t, "POST", "/v1/webhook-endpoints/"+epID+"/ping", nil); s != 202 {
		t.Fatalf("ping: %d", s)
	}
	// An endpoint that is down: the attempt is recorded and retried later.
	rc.mu.Lock()
	rc.fail = true
	rc.mu.Unlock()
	if s, _ := e.do(t, "POST", "/v1/claims", claimBody("M-HOOK-2")); s != 201 {
		t.Fatal("second claim")
	}
	var log []any
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		_, l := e.do(t, "GET", "/v1/webhook-endpoints/"+epID+"/deliveries", nil)
		log = l["data"].([]any)
		if d := log[0].(map[string]any); d["attempts"].(float64) >= 1 && d["last_status"] != nil {
			break
		}
	}
	latest := log[0].(map[string]any)
	if latest["status"] != "pending" || latest["last_status"].(float64) != 503 || latest["last_error"] == "" {
		t.Fatalf("a failed delivery: %v", latest)
	}
	_, list := e.do(t, "GET", "/v1/webhook-endpoints", nil)
	if l := list["data"].([]any); len(l) != 1 || l[0].(map[string]any)["failing"].(float64) < 1 || l[0].(map[string]any)["secret"] != nil {
		t.Fatalf("list: %v", l)
	}

	// Another organisation cannot see or remove it.
	other := setup(t)
	if s, _ := other.do(t, "DELETE", "/v1/webhook-endpoints/"+epID, nil); s != 404 {
		t.Fatalf("another organisation deleted it: %d", s)
	}
	if s, _ := e.do(t, "DELETE", "/v1/webhook-endpoints/"+epID, nil); s != 204 {
		t.Fatalf("delete: %d", s)
	}
}
