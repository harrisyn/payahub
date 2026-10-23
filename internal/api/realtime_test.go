package api_test

import (
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
)

func TestSubmitAndWaitForThePayer(t *testing.T) {
	e := setup(t)

	// Create and send in one call, waiting for the answer.
	start := time.Now()
	s, out := e.do(t, "POST", "/v1/claims?submit=true", claimBody("M-RT-1"), "Prefer", "wait=20", "Idempotency-Key", "rt-1")
	if s != 200 {
		t.Fatalf("create and wait: %d %v", s, out)
	}
	o := out["outcome"].(map[string]any)
	if o["state"] != "acknowledged" || o["final"] != true || o["payer_claim_number"] == "" {
		t.Fatalf("outcome: %v", o)
	}
	t.Logf("answered in %s", time.Since(start).Round(time.Millisecond))
	id := out["claim"].(map[string]any)["id"].(string)

	// Repeating the call sends nothing twice and gives the same answer.
	s, again := e.do(t, "POST", "/v1/claims?submit=true", claimBody("M-RT-1"), "Prefer", "wait=20", "Idempotency-Key", "rt-1")
	if s != 200 || again["claim"].(map[string]any)["id"] != id || again["outcome"].(map[string]any)["state"] != "acknowledged" {
		t.Fatalf("replay: %d %v", s, again)
	}
	if n := countKind(eventKinds(t, e, id), "submission_started"); n != 1 {
		t.Fatalf("sent %d times", n)
	}

	// A refusal comes back with the payer's reason.
	_, out = e.do(t, "POST", "/v1/claims?submit=true&wait=20", claimBody("REJECT-RT"))
	o = out["outcome"].(map[string]any)
	if o["state"] != "rejected" || o["error"] == nil {
		t.Fatalf("rejection: %v", out)
	}

	// Submit and wait on a saved claim.
	saved := e.create(t, "M-RT-2")
	start = time.Now()
	if s, out := e.do(t, "POST", "/v1/claims/"+saved+"/submit", nil, "Prefer", "wait=20"); s != 200 || out["outcome"].(map[string]any)["state"] != "acknowledged" {
		t.Fatalf("submit and wait: %d %v", s, out)
	}
	t.Logf("a saved claim answered in %s", time.Since(start).Round(time.Millisecond))

	// A payer slower than the wait: 202, and the claim still arrives.
	s, out = e.do(t, "POST", "/v1/claims?submit=true&wait=1", claimBody("SLOW-RT"))
	if s != 202 || out["outcome"] != nil {
		t.Fatalf("slow payer: %d %v", s, out)
	}
	e.waitFor(t, out["claim"].(map[string]any)["id"].(string), claim.Acknowledged)

	// A claim that fails the checks is saved, not sent, and says why.
	bad := claimBody("M-RT-3")
	bad["diagnoses"] = []any{}
	s, out = e.do(t, "POST", "/v1/claims?submit=true&wait=5", bad)
	if s != 201 || out["submitted"] != false || out["error"].(map[string]any)["code"] != "not_submittable" {
		t.Fatalf("invalid claim: %d %v", s, out)
	}
}

func countKind(kinds []string, k string) int {
	n := 0
	for _, x := range kinds {
		if x == k {
			n++
		}
	}
	return n
}
