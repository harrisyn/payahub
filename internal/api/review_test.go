package api_test

import (
	"testing"

	"github.com/harrisyn/payahub/internal/claim"
)

func TestDuplicateClaimNeedsAPersonToConfirm(t *testing.T) {
	e := setup(t)
	first := e.create(t, "M-DUP-1")
	e.do(t, "POST", "/v1/claims/"+first+"/submit", nil)
	e.waitFor(t, first, claim.Acknowledged)

	second := e.create(t, "M-DUP-1")
	_, checks := e.do(t, "GET", "/v1/claims/"+second+"/checks", nil)
	if checks["state"] != "needs_review" {
		t.Fatalf("checks: %v", checks)
	}
	issue := checks["issues"].([]any)[0].(map[string]any)
	if issue["code"] != "duplicate_claim" || issue["field"] != "claim:"+first {
		t.Fatalf("issue: %v", issue)
	}
	if s, out := e.do(t, "POST", "/v1/claims/"+second+"/submit", nil); s != 422 {
		t.Fatalf("a duplicate was sent: %d %v", s, out)
	}

	// Only issues awaiting review can be confirmed.
	if s, _ := e.do(t, "POST", "/v1/claims/"+second+"/review", map[string]any{"confirm": []any{
		map[string]any{"code": "line_unmapped", "field": "lines[0].mapping"}}}); s != 422 {
		t.Fatalf("confirmed an issue the claim does not have: %d", s)
	}
	s, out := e.do(t, "POST", "/v1/claims/"+second+"/review", map[string]any{
		"confirm": []any{map[string]any{"code": "duplicate_claim", "field": "claim:" + first}},
		"note":    "Second visit the same day, seen again in the evening."})
	if s != 200 || out["validation"].(map[string]any)["state"] != "ready" {
		t.Fatalf("review: %d %v", s, out)
	}
	reviews := out["claim"].(map[string]any)["reviews"].([]any)
	if len(reviews) != 1 || reviews[0].(map[string]any)["by"] != "API key" {
		t.Fatalf("review record: %v", reviews)
	}
	if s, out := e.do(t, "POST", "/v1/claims/"+second+"/submit", nil); s != 202 {
		t.Fatalf("submit after review: %d %v", s, out)
	}
	e.waitFor(t, second, claim.Acknowledged)
	if kinds := eventKinds(t, e, second); kinds[1] != "validation_failed" || kinds[2] != "reviewed" {
		t.Fatalf("events: %v", kinds)
	}
}
