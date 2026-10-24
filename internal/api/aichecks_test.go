package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Operators run the matching checks: always on word matching, and on the
// model when one serves code matching. A model that answers the same item
// whatever it is asked fails, groups included, and every run is kept.
func TestMatchingRegressionChecks(t *testing.T) {
	e := setup(t)
	clearAIProviders(t)
	op := operatorSession(t, e)
	if s, _ := e.do(t, "POST", "/v1/operator/ai/checks", nil); s != 403 {
		t.Fatalf("an API key ran the checks: %d", s)
	}

	s, out := op.do(t, "POST", "/v1/operator/ai/checks", nil)
	runs := out["data"].([]any)
	words := runs[0].(map[string]any)
	if s != 200 || len(runs) != 1 || words["method"] != "words" || words["passed"] != words["total"] {
		t.Fatalf("words only: %d %v", s, out)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ms []any
		for i := 1; i <= 80; i++ {
			ms = append(ms, map[string]any{"code": fmt.Sprintf("C%03d", i), "item_id": "MED-0005", "confidence": 0.9, "reason": "amlodipine"})
		}
		content, _ := json.Marshal(map[string]any{"matches": ms})
		json.NewEncoder(w).Encode(map[string]any{"model": "lazy-1", "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 10},
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(content)}}}})
	}))
	defer srv.Close()
	defer clearAIProviders(t)
	if s, p := op.do(t, "PUT", "/v1/operator/ai-providers/lazy", map[string]any{"kind": "openai_compatible", "name": "Lazy",
		"base_url": srv.URL + "/v1", "model": "lazy-1", "json_mode": "object", "tasks": []string{"code_matching"}}); s != 201 {
		t.Fatalf("provider: %d %v", s, p)
	}
	_, out = op.do(t, "POST", "/v1/operator/ai/checks", nil)
	model := out["data"].([]any)[1].(map[string]any)
	if model["method"] != "model" || model["models"] != "lazy-1" || model["prompt_version"] != "match-items-v1" ||
		model["passed"] != 1.0 || model["groups_passed"] != 0.0 {
		t.Fatalf("a model that ignores its input: %v", model)
	}
	fails := model["report"].(map[string]any)["group_failures"].([]any)
	if len(fails) == 0 || len(fails) != int(model["groups"].(float64)) {
		t.Fatalf("group failures: %v", fails)
	}

	_, hist := op.do(t, "GET", "/v1/operator/ai/checks", nil)
	if list := hist["data"].([]any); len(list) < 3 || list[0].(map[string]any)["method"] != "model" || hist["cases"].(float64) < 50 {
		t.Fatalf("history: %v", hist)
	}
}
