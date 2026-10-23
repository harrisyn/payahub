package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/draw"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/harrisyn/payahub/internal/deid"
)

func readField(v string, c float64) map[string]any {
	return map[string]any{"value": v, "confidence": c, "quote": v}
}

// modelStub is an OpenAI-compatible endpoint that matches items and reads
// forms the way a model would, or fails when down is set.
func modelStub(t *testing.T, down *atomic.Bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var answer any
		switch {
		case bytes.Contains(body, []byte("Clinic items")):
			answer = map[string]any{"matches": []any{
				map[string]any{"code": "PCM", "item_id": "MED-0001", "confidence": 0.96, "reason": "paracetamol 500 mg tablet"},
				map[string]any{"code": "BOGUS", "item_id": "MED-4040", "confidence": 0.99, "reason": "invented"},
			}}
		case bytes.Contains(body, []byte("data:image/png;base64,")):
			if bytes.Contains(body, []byte("Kofi")) || bytes.Contains(body, []byte("M-1001")) {
				t.Errorf("the patient's identity reached the model")
			}
			answer = map[string]any{
				"payer_name": readField("Sandbox Mutual", 0.97), "gender": readField("male", 0.97), "provider_name": readField("Ridge Clinic", 0.99),
				"encounter_date": readField("2026-10-03", 0.95), "discharge_date": readField("", 0), "authorization_code": readField("", 0),
				"diagnoses": []any{map[string]any{"text": readField("Malaria", 0.96), "icd10": readField("B54", 0.95), "primary": true}},
				"lines": []any{map[string]any{"description": readField("Consultation, general practitioner", 0.95),
					"quantity": readField("1", 0.97), "unit_price": readField("150.00", 0.8), "service_date": readField("", 0)}},
			}
		default:
			t.Errorf("unexpected model request: %.200s", body)
		}
		content, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": "stub-1", "usage": map[string]any{"prompt_tokens": 1200, "completion_tokens": 80},
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(content)}}}})
	}))
}

func upload(t *testing.T, e env, name, contentType string, data []byte) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="file"; filename="` + name + `"`}
	h["Content-Type"] = []string{contentType}
	part, _ := mw.CreatePart(h)
	part.Write(data)
	mw.Close()
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/claims/extract", &buf)
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestModelsMatchItemsAndReadForms(t *testing.T) {
	e := setup(t)
	clearAIProviders(t)
	op := operatorSession(t, e)
	pdf := formPNG()

	// Before any model is set up: word matching, and forms are not read.
	_, out := e.do(t, "POST", "/v1/items/suggest", map[string]any{"items": []any{map[string]any{"code": "PCM", "name": "PCM 500 tabs"}}})
	if out["method"] != "words" {
		t.Fatalf("no model, yet method %v", out["method"])
	}
	if s, out := upload(t, e, "form.png", "image/png", pdf); s != 409 || out["error"].(map[string]any)["code"] != "no_model" {
		t.Fatalf("read a form with no model: %d %v", s, out)
	}

	var down atomic.Bool
	srv := modelStub(t, &down)
	defer srv.Close()
	if s, p := op.do(t, "PUT", "/v1/operator/ai-providers/stub", map[string]any{"kind": "openai_compatible", "name": "Stub",
		"base_url": srv.URL + "/v1", "model": "stub-1", "json_mode": "object", "tasks": []string{"document_intake", "code_matching"}}); s != 201 {
		t.Fatalf("provider: %d %v", s, p)
	}

	items := map[string]any{"items": []any{
		map[string]any{"code": "PCM", "name": "PCM 500 tabs"},
		map[string]any{"code": "BOGUS", "name": "Something"},
	}}
	_, out = e.do(t, "POST", "/v1/items/suggest", items)
	if out["method"] != "model" {
		t.Fatalf("method %v: %v", out["method"], out)
	}
	rows := out["data"].([]any)
	first := rows[0].(map[string]any)["suggestions"].([]any)[0].(map[string]any)
	if first["source"] != "model" || first["item"].(map[string]any)["id"] != "MED-0001" || first["evidence"].(map[string]any)["prompt_version"] != "match-items-v1" {
		t.Fatalf("model pick: %v", first)
	}
	for _, sg := range rows[1].(map[string]any)["suggestions"].([]any) {
		if sg.(map[string]any)["source"] == "model" {
			t.Fatalf("an invented item was suggested: %v", sg)
		}
	}
	if _, out := e.do(t, "POST", "/v1/items/suggest?method=words", items); out["method"] != "words" {
		t.Fatal("method=words still asked the model")
	}

	// A form becomes a proposed claim, which then saves like any other.
	s, x := upload(t, e, "form.png", "image/png", pdf)
	if s != 200 {
		t.Fatalf("extract: %d %v", s, x)
	}
	c := x["claim"].(map[string]any)
	if m := c["member"].(map[string]any); c["payer_id"] == "" || m["member_number"] != "M-1001" || m["last_name"] != "Boateng" {
		t.Fatalf("claim: %v", c)
	}
	if p := x["privacy"].(map[string]any); p["method"] != "image" {
		t.Fatalf("privacy: %v", p)
	}
	if f := x["fields"].(map[string]any)["lines.0.unit_price"].(map[string]any); f["needs_review"] != true {
		t.Errorf("a price read at 0.8 was not marked for review: %v", f)
	}
	if s, created := e.do(t, "POST", "/v1/claims", c, "Idempotency-Key", "from-form-1"); s != 201 {
		t.Fatalf("the proposed claim did not save: %d %v", s, created)
	}
	if s, _ := upload(t, e, "page.html", "application/pdf", []byte("<html>not a form</html>")); s != 415 {
		t.Fatalf("an HTML file was read: %d", s)
	}

	// When the model is down, matching falls back to words and says so.
	down.Store(true)
	_, out = e.do(t, "POST", "/v1/items/suggest", items)
	if out["method"] != "words" || !strings.Contains(out["note"].(string), "could not answer") {
		t.Fatalf("fallback: %v", out)
	}
	if s, _ := upload(t, e, "form.png", "image/png", pdf); s != 502 {
		t.Fatalf("a failed read: %d", s)
	}

	_, u := op.do(t, "GET", "/v1/operator/ai-usage", nil)
	var runs, failed float64
	for _, row := range u["data"].([]any) {
		runs += row.(map[string]any)["runs"].(float64)
		failed += row.(map[string]any)["failed"].(float64)
	}
	if runs != 4 || failed != 2 {
		t.Fatalf("usage: %v runs, %v failed: %v", runs, failed, u)
	}
	if s, _ := e.do(t, "GET", "/v1/operator/ai-usage", nil); s != 403 && s != 401 {
		t.Fatalf("a customer read model usage: %d", s)
	}
}

// formOCR stands in for Tesseract: every page has the patient's name and
// member number beside their labels.
type formOCR struct{}

func (formOCR) Words(context.Context, image.Image) ([]deid.Word, error) {
	w := func(text string, x, y int, line string) deid.Word {
		return deid.Word{Text: text, Box: image.Rect(x, y, x+len(text)*6, y+10), Line: line}
	}
	return []deid.Word{w("Name:", 2, 2, "1"), w("Kofi", 40, 2, "1"), w("Boateng", 70, 2, "1"),
		w("Member", 2, 20, "2"), w("No:", 45, 20, "2"), w("M-1001", 70, 20, "2")}, nil
}

func formPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 200, 60))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
