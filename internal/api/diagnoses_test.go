package api_test

import "testing"

func TestDiagnosesAreCheckedAgainstICD10(t *testing.T) {
	e := setup(t)
	_, found := e.do(t, "GET", "/v1/diagnoses?q=falciparum+malaria&limit=3", nil)
	if rows := found["data"].([]any); len(rows) == 0 || rows[0].(map[string]any)["code"] != "B50" {
		t.Fatalf("search: %v", found)
	}

	withDiagnosis := func(code string) map[string]any {
		b := claimBody("M-3001")
		b["diagnoses"] = []any{map[string]any{"code": map[string]any{"system": "ICD10", "value": code}, "role": "primary"}}
		return b
	}
	// A listed code gets its title.
	s, c := e.do(t, "POST", "/v1/claims", withDiagnosis("b50.9"))
	if s != 201 {
		t.Fatalf("create: %d %v", s, c)
	}
	if d := c["diagnoses"].([]any)[0].(map[string]any)["code"].(map[string]any); d["display"] != "Plasmodium falciparum malaria, unspecified" {
		t.Fatalf("display: %v", d)
	}

	issues := func(code string) []any {
		_, v := e.do(t, "POST", "/v1/claims/validate", withDiagnosis(code))
		return v["issues"].([]any)
	}
	has := func(is []any, code string) bool {
		for _, i := range is {
			if i.(map[string]any)["code"] == code {
				return true
			}
		}
		return false
	}
	if is := issues("B50.9"); has(is, "diagnosis_invalid") || has(is, "diagnosis_unknown") {
		t.Fatalf("B50.9 flagged: %v", is)
	}
	if is := issues("B24"); has(is, "diagnosis_unknown") {
		t.Fatalf("WHO B24 flagged: %v", is)
	}
	if is := issues("malaria"); !has(is, "diagnosis_invalid") {
		t.Fatalf("free text accepted: %v", is)
	}
	if is := issues("U99.9"); !has(is, "diagnosis_unknown") {
		t.Fatalf("unknown category passed: %v", is)
	}
}
