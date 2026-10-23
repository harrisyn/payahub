package api_test

import (
	"strings"
	"testing"

	"github.com/harrisyn/payahub/internal/claim"
)

func TestCatalogue(t *testing.T) {
	e := setup(t)
	_, payers := e.do(t, "GET", "/v1/payers", nil)
	byID := map[string]map[string]any{}
	for _, p := range payers["data"].([]any) {
		byID[p.(map[string]any)["id"].(string)] = p.(map[string]any)
	}
	if p := byID["sandbox"]; p["kind"] != "standalone" || p["status"] != "available" {
		t.Fatalf("sandbox = %v", p)
	}
	if p := byID["sandbox_mutual"]; p["kind"] != "integrated" || p["connector"].(map[string]any)["id"] != "sandbox_exchange" {
		t.Fatalf("sandbox_mutual = %v", p)
	}
	if p := byID["glico"]; p["kind"] != "integrated" || p["status"] != "connecting" || p["connector"].(map[string]any)["name"] != "XCeli" {
		t.Fatalf("glico = %v", p)
	}

	_, meds := e.do(t, "GET", "/v1/items?kind=medication", nil)
	for _, it := range meds["data"].([]any) {
		if it.(map[string]any)["kind"] != "medication" {
			t.Fatalf("kind filter let through %v", it)
		}
	}
	_, item := e.do(t, "GET", "/v1/items/MED-0001", nil)
	if item["item"].(map[string]any)["generic"] != "paracetamol" || len(item["payer_codes"].([]any)) < 3 {
		t.Fatalf("item = %v", item)
	}

	s, sug := e.do(t, "POST", "/v1/items/suggest", map[string]any{"items": []any{
		map[string]any{"code": "PCM5", "name": "Paracetamol 500mg tabs"},
		map[string]any{"code": "PCM1", "name": "Paracetamol 1000mg tabs"},
		map[string]any{"code": "CONS", "name": "OPD consultation"},
	}})
	if s != 200 {
		t.Fatalf("suggest: %d %v", s, sug)
	}
	rows := sug["data"].([]any)
	top := func(i int) map[string]any {
		return rows[i].(map[string]any)["suggestions"].([]any)[0].(map[string]any)
	}
	if top(0)["item"].(map[string]any)["id"] != "MED-0001" || top(0)["confidence"].(float64) < 0.6 {
		t.Fatalf("paracetamol 500: %v", top(0))
	}
	// A different strength is not the same medicine, however close the words.
	if top(1)["confidence"].(float64) >= top(0)["confidence"].(float64) {
		t.Fatalf("1000 mg matched as well as 500 mg: %v", top(1))
	}
	if top(2)["item"].(map[string]any)["id"] != "SRV-0001" {
		t.Fatalf("consultation: %v", top(2))
	}
}

// A claim line sent with the clinic's own code reaches the payer with the
// payer's code: own code -> standard item -> payer code.
func TestItemMappingsResolveClaimLines(t *testing.T) {
	e := setup(t)
	if s, out := e.do(t, "PUT", "/v1/item-mappings", map[string]any{"mappings": []any{
		map[string]any{"local_code": "CONS", "local_name": "OPD consultation", "item_id": "NOPE"},
	}}); s != 422 {
		t.Fatalf("unknown item accepted: %d %v", s, out)
	}
	s, out := e.do(t, "PUT", "/v1/item-mappings", map[string]any{"mappings": []any{
		map[string]any{"local_code": "CONS", "local_name": "OPD consultation", "item_id": "SRV-0001"},
		map[string]any{"local_code": "PCM5", "local_name": "Paracetamol 500mg", "item_id": "MED-0001"},
	}})
	if s != 200 || len(out["data"].([]any)) != 2 || out["data"].([]any)[0].(map[string]any)["confirmed_by"] != "api:sandbox" {
		t.Fatalf("save mappings: %d %v", s, out)
	}

	body := func(lines ...any) map[string]any {
		b := claimBody("M-1001")
		b["payer_id"] = "sandbox_mutual"
		b["lines"] = lines
		return b
	}
	own := func(code string) map[string]any {
		return map[string]any{"description": code, "unit_price": 1000, "code": map[string]any{"system": "local", "value": code}}
	}
	std := map[string]any{"description": "AL", "unit_price": 3000, "code": map[string]any{"system": "payahub", "value": "MED-0003"}}

	s, c := e.do(t, "POST", "/v1/claims", body(own("CONS"), own("PCM5"), std))
	if s != 201 {
		t.Fatalf("create: %d %v", s, c)
	}
	want := []string{"MUT-SRV-0001", "MUT-MED-0001", "MUT-MED-0003"}
	for i, l := range c["lines"].([]any) {
		m := l.(map[string]any)["mapping"].(map[string]any)
		if m["target"].(map[string]any)["value"] != want[i] {
			t.Fatalf("line %d = %v", i, l)
		}
	}
	id := c["id"].(string)
	if s, out := e.do(t, "POST", "/v1/claims/"+id+"/submit", nil); s != 202 {
		t.Fatalf("submit: %d %v", s, out)
	}
	e.waitFor(t, id, claim.Acknowledged)

	// An own code nobody has mapped stops the claim, saying what to do.
	s, c = e.do(t, "POST", "/v1/claims", body(own("XRAY9")))
	if s != 201 {
		t.Fatalf("create: %d %v", s, c)
	}
	s, out = e.do(t, "POST", "/v1/claims/"+c["id"].(string)+"/submit", nil)
	issues := out["error"].(map[string]any)["details"].([]any)
	said := false
	for _, i := range issues {
		said = said || strings.Contains(i.(map[string]any)["message"].(string), `map your code "XRAY9"`)
	}
	if s != 422 || !said {
		t.Fatalf("unmapped code: %d %v", s, out)
	}
	// Mapping it afterwards lets the same claim through.
	e.do(t, "PUT", "/v1/item-mappings", map[string]any{"mappings": []any{
		map[string]any{"local_code": "XRAY9", "item_id": "IMG-0001"},
	}})
	if s, out := e.do(t, "POST", "/v1/claims/"+c["id"].(string)+"/submit", nil); s != 202 {
		t.Fatalf("submit after mapping: %d %v", s, out)
	}
	if s, _ := e.do(t, "DELETE", "/v1/item-mappings/XRAY9", nil); s != 204 {
		t.Fatalf("delete: %d", s)
	}
}
