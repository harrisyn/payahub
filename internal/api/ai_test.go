package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOperatorConfiguresAIProviders(t *testing.T) {
	e := setup(t)
	clearAIProviders(t)
	ses, _ := signup(t, e)
	op := operatorSession(t, e)

	// A local OpenAI-compatible endpoint that answers the check.
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": "local-model", "choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": `{"ok": true}`}}}})
	}))
	defer srv.Close()

	body := map[string]any{"kind": "openai_compatible", "name": "Local model", "base_url": srv.URL + "/v1",
		"model": "local-model", "json_mode": "object", "tasks": []string{"document_intake"}, "priority": 10, "api_key": "sk-local-secret-1234"}
	if s, _ := ses.do(t, "PUT", "/v1/operator/ai-providers/local", body); s != 403 {
		t.Fatalf("a customer configured a model provider: %d", s)
	}
	s, p := op.do(t, "PUT", "/v1/operator/ai-providers/local", body)
	if s != 201 || p["has_key"] != true || p["key_hint"] != "1234" {
		t.Fatalf("create: %d %v", s, p)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("the key came back: %s", raw)
	}

	// Saving without a key keeps the one sealed.
	body["api_key"] = ""
	body["priority"] = 5
	if s, p := op.do(t, "PUT", "/v1/operator/ai-providers/local", body); s != 200 || p["has_key"] != true {
		t.Fatalf("update: %d %v", s, p)
	}
	s, p = op.do(t, "POST", "/v1/operator/ai-providers/local/check", nil)
	if s != 200 || p["check_ok"] != true || gotKey != "Bearer sk-local-secret-1234" {
		t.Fatalf("check: %d %v key %q", s, p, gotKey)
	}

	// Validation names each bad field.
	s, bad := op.do(t, "PUT", "/v1/operator/ai-providers/Bad%20Id", map[string]any{"kind": "gemini", "tasks": []string{"poetry"}, "effort": "huge", "base_url": "http://example.com"})
	fields, _ := bad["error"].(map[string]any)["details"].(map[string]any)
	for _, f := range []string{"id", "kind", "name", "model", "tasks", "effort", "base_url"} {
		if _, ok := fields[f]; !ok {
			t.Errorf("no problem reported for %s: %v (%d)", f, fields, s)
		}
	}
	if s, _ := op.do(t, "PUT", "/v1/operator/ai-providers/claude", map[string]any{"kind": "anthropic", "name": "Claude", "model": "claude-opus-5-5"}); s != 422 {
		t.Fatalf("an Anthropic provider without a key was saved: %d", s)
	}

	_, list := op.do(t, "GET", "/v1/operator/ai-providers", nil)
	if n := len(list["data"].([]any)); n != 1 {
		t.Fatalf("listed %d providers", n)
	}
	if s, _ := op.do(t, "DELETE", "/v1/operator/ai-providers/local", nil); s != 204 {
		t.Fatalf("delete: %d", s)
	}
	if s, _ := op.do(t, "DELETE", "/v1/operator/ai-providers/local", nil); s != 404 {
		t.Fatalf("second delete: %d", s)
	}
}

// clearAIProviders empties the platform's model providers, which every
// organisation shares, so a test starts from none and leaves none behind.
func clearAIProviders(t *testing.T) {
	t.Helper()
	clear := func() {
		if _, err := st.Pool.Exec(context.Background(), `DELETE FROM ai_providers; DELETE FROM ai_runs`); err != nil {
			t.Fatal(err)
		}
	}
	clear()
	t.Cleanup(clear)
}
