package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/harrisyn/payahub/internal/store"
)

// codesStub answers like a model asked for payer codes, diagnosis codes and
// a form whose diagnosis has no code. It fails the test if a patient's name
// reaches it.
func codesStub(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("Kofi")) || bytes.Contains(body, []byte("Mensah")) {
			t.Errorf("a patient's name reached the model: %.300s", body)
		}
		var answer any
		switch {
		case bytes.Contains(body, []byte("Insurer's list")):
			answer = map[string]any{"matches": []any{
				map[string]any{"item_id": "MED-0001", "code": "PARA5", "confidence": 0.97, "reason": "same drug, strength and form"},
				map[string]any{"item_id": "LAB-0001", "code": "HB-FULL", "confidence": 0.82, "reason": "full blood count"},
				map[string]any{"item_id": "SRV-0001", "code": "NOT-LISTED", "confidence": 0.99, "reason": "invented"},
				map[string]any{"item_id": "IMG-0001", "code": "", "confidence": 0.9, "reason": "no chest x-ray in the list"},
				map[string]any{"item_id": "MED-0017", "code": "PARA5", "confidence": 0.96, "reason": "paracetamol"},
			}}
		case bytes.Contains(body, []byte("Diagnoses (key")):
			var codes []any
			for key, code := range map[string]string{"0": "J06.9", "1": "I10", "2": "Q99.999", "3": ""} {
				if bytes.Contains(body, []byte(`\n`+key+` |`)) {
					codes = append(codes, map[string]any{"key": key, "code": code, "confidence": 0.97, "reason": "as written"})
				}
			}
			answer = map[string]any{"codes": codes}
		case bytes.Contains(body, []byte("data:image/png;base64,")):
			answer = map[string]any{
				"payer_name": readField("Sandbox Mutual", 0.97), "gender": readField("male", 0.97), "provider_name": readField("Ridge Clinic", 0.99),
				"encounter_date": readField("2026-10-03", 0.95), "discharge_date": readField("", 0), "authorization_code": readField("", 0),
				"diagnoses": []any{map[string]any{"text": readField("URTI", 0.96), "icd10": readField("", 0), "primary": true}},
				"lines": []any{map[string]any{"description": readField("Consultation, general practitioner", 0.95),
					"quantity": readField("1", 0.97), "unit_price": readField("150.00", 0.97), "service_date": readField("", 0)}},
			}
		default:
			t.Errorf("unexpected model request: %.200s", body)
		}
		content, _ := json.Marshal(answer)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"model": "stub-1", "usage": map[string]any{"prompt_tokens": 900, "completion_tokens": 60},
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": string(content)}}}})
	}))
}

// A model finds a payer's code only in the payer's own list, and an operator
// accepts its sure picks.
func TestModelPayerCodesComeFromThePayersList(t *testing.T) {
	e := setup(t)
	clearAIProviders(t)
	op := operatorSession(t, e)
	id := "acacia_" + strings.ReplaceAll(store.NewID("t"), "-", "")[22:34]
	if s, p := op.do(t, "PUT", "/v1/operator/payers/"+id, map[string]any{
		"name": "Acacia Health", "country": "GH", "connector": "medserve", "fields": map[string]any{"insurer_code": "acacia", "insurer_id": "9"},
	}); s != 201 {
		t.Fatalf("create: %d %v", s, p)
	}
	ask := func() (int, map[string]any) {
		return op.do(t, "POST", "/v1/operator/payers/"+id+"/codes/model", map[string]any{
			"item_ids": []string{"MED-0001", "LAB-0001", "SRV-0001", "IMG-0001", "MED-0017"}})
	}
	if s, out := ask(); s != 409 || out["error"].(map[string]any)["code"] != "no_list" {
		t.Fatalf("no list: %d %v", s, out)
	}
	if s, out := op.do(t, "PUT", "/v1/operator/payers/"+id+"/list", map[string]any{
		"csv": "Code,Name\nPARA5,Paracetamol 500mg tab\nHB-FULL,Haemogram complete\nCONS1,Consultation\n"}); s != 200 {
		t.Fatalf("list: %d %v", s, out)
	}
	if s, out := ask(); s != 409 || out["error"].(map[string]any)["code"] != "no_model" {
		t.Fatalf("no model: %d %v", s, out)
	}
	srv := codesStub(t)
	defer srv.Close()
	if s, p := op.do(t, "PUT", "/v1/operator/ai-providers/stub", map[string]any{"kind": "openai_compatible", "name": "Stub",
		"base_url": srv.URL + "/v1", "model": "stub-1", "json_mode": "object", "tasks": []string{"code_matching"}}); s != 201 {
		t.Fatalf("provider: %d %v", s, p)
	}
	s, out := ask()
	if s != 200 || out["answered"] != 4.0 || out["found"] != 3.0 {
		t.Fatalf("ask: %d %v", s, out)
	}

	_, codes := op.do(t, "GET", "/v1/operator/payers/"+id+"/codes", nil)
	picks := map[string]map[string]any{}
	for _, row := range codes["data"].([]any) {
		r := row.(map[string]any)
		if mp, ok := r["model_pick"].(map[string]any); ok {
			picks[r["item"].(map[string]any)["id"].(string)] = mp
		}
	}
	if p := picks["MED-0001"]; p["code"] != "PARA5" || p["prompt_version"] != "payer-codes-v1" || p["model"] != "stub-1" {
		t.Fatalf("pick: %v", picks)
	}
	if _, ok := picks["SRV-0001"]; ok {
		t.Fatalf("a code outside the list was kept: %v", picks["SRV-0001"])
	}
	if p := picks["IMG-0001"]; p["code"] != "" {
		t.Fatalf("no match should be kept as none: %v", p)
	}
	// A syrup is not a tablet of another strength, however sure the model is.
	if p := picks["MED-0017"]; p["confidence"].(float64) > 0.5 || !strings.Contains(p["reason"].(string), "different strength") {
		t.Fatalf("a pick of another strength: %v", p)
	}

	// Accepting the model's sure picks saves only those at 0.9 or more.
	s, acc := op.do(t, "POST", "/v1/operator/payers/"+id+"/codes/accept", map[string]any{"source": "model", "min_confidence": 0.9})
	if s != 200 || acc["applied"] != 1.0 {
		t.Fatalf("accept: %d %v", s, acc)
	}
	_, codes = op.do(t, "GET", "/v1/operator/payers/"+id+"/codes?kind=medication", nil)
	for _, row := range codes["data"].([]any) {
		r := row.(map[string]any)
		if r["item"].(map[string]any)["id"] == "MED-0001" {
			if c, _ := r["code"].(map[string]any); c == nil || c["code"] != "PARA5" {
				t.Fatalf("accepted code: %v", r)
			}
		}
	}
	if s, _ := e.do(t, "POST", "/v1/operator/payers/"+id+"/codes/model", map[string]any{}); s != 403 && s != 401 {
		t.Fatalf("a customer asked for payer codes: %d", s)
	}
}

// Diagnoses in words are coded by the model, only to real ICD-10 codes, with
// the title search beside them; a form's uncoded diagnosis is coded too.
func TestModelCodesDiagnosesInWords(t *testing.T) {
	e := setup(t)
	clearAIProviders(t)
	op := operatorSession(t, e)
	req := map[string]any{"diagnoses": []any{
		map[string]any{"text": "URTI"}, map[string]any{"text": "BP"},
		map[string]any{"text": "Name: Kofi Mensah, odd rash"}, map[string]any{"text": "malaria"}}}

	_, out := e.do(t, "POST", "/v1/diagnoses/suggest", req)
	if out["method"] != "words" {
		t.Fatalf("no model: %v", out)
	}
	if sg := out["data"].([]any)[3].(map[string]any)["suggestions"].([]any); len(sg) == 0 || sg[0].(map[string]any)["source"] != "words" {
		t.Fatalf("title search for malaria: %v", sg)
	}

	srv := codesStub(t)
	defer srv.Close()
	if s, p := op.do(t, "PUT", "/v1/operator/ai-providers/stub", map[string]any{"kind": "openai_compatible", "name": "Stub",
		"base_url": srv.URL + "/v1", "model": "stub-1", "json_mode": "object", "tasks": []string{"document_intake", "code_matching"}}); s != 201 {
		t.Fatalf("provider: %d %v", s, p)
	}
	s, out := e.do(t, "POST", "/v1/diagnoses/suggest", req)
	if s != 200 || out["method"] != "model" {
		t.Fatalf("model: %d %v", s, out)
	}
	rows := out["data"].([]any)
	first := func(i int) map[string]any {
		sg := rows[i].(map[string]any)["suggestions"].([]any)
		if len(sg) == 0 {
			return nil
		}
		return sg[0].(map[string]any)
	}
	if f := first(0); f["source"] != "model" || f["code"].(map[string]any)["code"] != "J06.9" || f["evidence"].(map[string]any)["prompt_version"] != "diagnoses-v1" {
		t.Fatalf("URTI: %v", f)
	}
	if f := first(1); f["code"].(map[string]any)["code"] != "I10" {
		t.Fatalf("BP: %v", f)
	}
	if f := first(2); f != nil && f["source"] == "model" {
		t.Fatalf("a code outside ICD-10 was suggested: %v", f)
	}
	if s, out := e.do(t, "POST", "/v1/diagnoses/suggest", map[string]any{"diagnoses": []any{}}); s != 422 {
		t.Fatalf("empty: %d %v", s, out)
	}

	// A form with a diagnosis in words only: the model codes it, marked as its.
	s, x := upload(t, e, "form.png", "image/png", formPNG())
	if s != 200 {
		t.Fatalf("extract: %d %v", s, x)
	}
	d := x["claim"].(map[string]any)["diagnoses"].([]any)[0].(map[string]any)
	f := x["fields"].(map[string]any)["diagnoses.0.code"].(map[string]any)
	if d["code"].(map[string]any)["value"] != "J06.9" || !strings.Contains(f["note"].(string), "coded by the model") || f["needs_review"] != false {
		t.Fatalf("form diagnosis: %v %v", d, f)
	}
}
