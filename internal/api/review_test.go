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

// The review queue shows what holds unsent claims back. Mapping an own code
// there moves every claim waiting on it; a model's low-confidence pick waits
// for a person to confirm.
func TestReviewQueue(t *testing.T) {
	e := setup(t)
	own := func(member, code, name string) string {
		b := claimBody(member)
		b["payer_id"] = "sandbox_mutual"
		b["lines"] = []any{map[string]any{"description": name, "unit_price": 2500, "quantity": 2,
			"code": map[string]any{"system": "local", "value": code}}}
		s, c := e.do(t, "POST", "/v1/claims", b)
		if s != 201 {
			t.Fatalf("create: %d %v", s, c)
		}
		return c["id"].(string)
	}
	a := own("M-RQ-1", "FBC7", "Full blood count")
	b := own("M-RQ-2", "FBC7", "FBC")
	own("M-RQ-3", "XR-CHEST", "Chest X-ray")
	if s, _ := e.do(t, "POST", "/v1/claims/"+a+"/submit", nil); s != 422 {
		t.Fatalf("an unmapped claim was sent: %d", s)
	}

	s, q := e.do(t, "GET", "/v1/review-queue", nil)
	codes := q["codes"].([]any)
	if s != 200 || len(codes) != 2 {
		t.Fatalf("queue: %d %v", s, q)
	}
	top := codes[0].(map[string]any)
	if top["code"] != "FBC7" || top["claims"] != 2.0 || top["amount"] != 10000.0 || top["name"] != "FBC" {
		t.Fatalf("most held code first, with its newest name and amount: %v", top)
	}
	if sg := top["suggestions"].([]any); len(sg) == 0 {
		t.Fatalf("no suggestion for FBC7: %v", top)
	}

	// Mapping the code checks both claims again: they are ready to send.
	s, out := e.do(t, "PUT", "/v1/item-mappings", map[string]any{"mappings": []any{
		map[string]any{"local_code": "FBC7", "local_name": "Full blood count", "item_id": "LAB-0001"}}})
	re, _ := out["rechecked"].(map[string]any)
	if s != 200 || re["claims"] != 2.0 || re["ready"] != 2.0 {
		t.Fatalf("recheck: %d %v", s, out)
	}
	for _, id := range []string{a, b} {
		_, c := e.do(t, "GET", "/v1/claims/"+id, nil)
		l := c["lines"].([]any)[0].(map[string]any)
		if c["state"] != "ready" || l["item"] != "LAB-0001" {
			t.Fatalf("claim %s after mapping: %v %v", id, c["state"], l)
		}
	}
	if kinds := eventKinds(t, e, b); kinds[len(kinds)-1] != "rechecked" {
		t.Fatalf("events: %v", kinds)
	}
	_, q = e.do(t, "GET", "/v1/review-queue", nil)
	if codes := q["codes"].([]any); len(codes) != 1 || codes[0].(map[string]any)["code"] != "XR-CHEST" {
		t.Fatalf("queue after mapping: %v", q["codes"])
	}
	if q["counts"].(map[string]any)["ready"] != 2.0 {
		t.Fatalf("counts: %v", q["counts"])
	}
	if s, _ := e.do(t, "POST", "/v1/claims/"+a+"/submit", nil); s != 202 {
		t.Fatalf("submit after mapping: %d", s)
	}

	// A model's pick under the threshold stops the claim for a person.
	low := claimBody("M-RQ-4")
	low["lines"] = []any{map[string]any{"description": "Malaria RDT", "unit_price": 4000,
		"mapping": map[string]any{
			"source":   map[string]any{"system": "local", "value": "RDT"},
			"target":   map[string]any{"system": "payer:sandbox", "value": "L01"},
			"evidence": map[string]any{"origin": "ai_mapped", "confidence": 0.62, "assertion": "Malaria rapid test"},
		}}}
	_, c := e.do(t, "POST", "/v1/claims", low)
	id := c["id"].(string)
	if s, _ := e.do(t, "POST", "/v1/claims/"+id+"/submit", nil); s != 422 {
		t.Fatalf("a low-confidence pick was sent: %d", s)
	}
	_, q = e.do(t, "GET", "/v1/review-queue", nil)
	waiting := q["claims"].([]any)
	if len(waiting) != 1 {
		t.Fatalf("waiting: %v", waiting)
	}
	w := waiting[0].(map[string]any)
	issue := w["issues"].([]any)[0].(map[string]any)
	if w["claim"].(map[string]any)["id"] != id || issue["code"] != "needs_review" || issue["field"] != "lines[0].mapping" {
		t.Fatalf("waiting claim: %v", w)
	}
	e.do(t, "POST", "/v1/claims/"+id+"/review", map[string]any{"confirm": []any{
		map[string]any{"code": "needs_review", "field": "lines[0].mapping"}}})
	if _, q = e.do(t, "GET", "/v1/review-queue", nil); len(q["claims"].([]any)) != 0 {
		t.Fatalf("still waiting after review: %v", q["claims"])
	}

}
