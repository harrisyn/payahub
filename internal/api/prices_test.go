package api_test

import (
	"encoding/base64"
	"os"
	"testing"
)

func TestPriceListImportAndClaims(t *testing.T) {
	e := setup(t)
	csv := "Clinic price list,,,\n" + // a title row before the header
		"Item Code,Description,Category,Unit Price\n" +
		"PCM5,Paracetamol 500mg Tab,Drugs,\"1,250.50\"\n" +
		"FBC,Full blood count,Laboratory,45\n" +
		"FBC,Full blood count again,Laboratory,46\n" +
		"BAD,Something,Drugs,twelve\n" +
		",No code,Drugs,3\n"
	file := base64.StdEncoding.EncodeToString([]byte(csv))

	s, out := e.do(t, "POST", "/v1/price-list/import", map[string]any{"file": file, "dry_run": true})
	if s != 200 || out["rows"].(float64) != 2 || len(out["skipped"].([]any)) != 3 || out["changes"] != nil {
		t.Fatalf("dry run: %d %v", s, out)
	}
	if _, list := e.do(t, "GET", "/v1/price-list", nil); len(list["data"].([]any)) != 0 {
		t.Fatalf("dry run saved: %v", list)
	}
	s, out = e.do(t, "POST", "/v1/price-list/import", map[string]any{"file": file})
	if s != 200 || out["changes"].(map[string]any)["added"].(float64) != 2 {
		t.Fatalf("import: %d %v", s, out)
	}
	// The same file again changes nothing.
	_, out = e.do(t, "POST", "/v1/price-list/import", map[string]any{"file": file})
	if c := out["changes"].(map[string]any); c["unchanged"].(float64) != 2 || c["added"].(float64) != 0 {
		t.Fatalf("re-import: %v", out)
	}

	_, list := e.do(t, "GET", "/v1/price-list", nil)
	rows := list["data"].([]any)
	byCode := map[string]map[string]any{}
	for _, r := range rows {
		byCode[r.(map[string]any)["local_code"].(string)] = r.(map[string]any)
	}
	if p := byCode["PCM5"]; p["unit_price"].(float64) != 125050 || p["kind"] != "medication" || p["currency"] != "GHS" ||
		p["suggestion"].(map[string]any)["item"].(map[string]any)["id"] != "MED-0001" {
		t.Fatalf("PCM5 = %v", p)
	}

	// A line with an own code and no price takes the list's price and name;
	// an explicit 0 stays 0; a line with its own price keeps it.
	b := claimBody("M-2001")
	b["lines"] = []any{
		map[string]any{"code": map[string]any{"system": "local", "value": "PCM5"}, "quantity": 2},
		map[string]any{"description": "FBC", "unit_price": 0, "code": map[string]any{"system": "local", "value": "FBC"}},
		map[string]any{"description": "FBC", "unit_price": 5000, "code": map[string]any{"system": "local", "value": "FBC"}},
	}
	s, c := e.do(t, "POST", "/v1/claims", b)
	if s != 201 {
		t.Fatalf("create: %d %v", s, c)
	}
	lines := c["lines"].([]any)
	l0 := lines[0].(map[string]any)
	if l0["unit_price"].(float64) != 125050 || l0["price_from"] != "price_list" || l0["description"] != "Paracetamol 500mg Tab" {
		t.Fatalf("line 0 = %v", l0)
	}
	if l := lines[1].(map[string]any); l["unit_price"].(float64) != 0 || l["price_from"] != nil {
		t.Fatalf("line 1 = %v", l)
	}
	if l := lines[2].(map[string]any); l["unit_price"].(float64) != 5000 {
		t.Fatalf("line 2 = %v", l)
	}
	if c["total"].(float64) != 2*125050+5000 {
		t.Fatalf("total = %v", c["total"])
	}

	// Replace leaves exactly the file's codes.
	xlsx, err := os.ReadFile("../sheet/testdata/prices.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	s, out = e.do(t, "POST", "/v1/price-list/import", map[string]any{
		"file": base64.StdEncoding.EncodeToString(xlsx), "mode": "replace"})
	if s != 200 {
		t.Fatalf("xlsx import: %d %v", s, out)
	}
	if c := out["changes"].(map[string]any); c["added"].(float64) != 3 || c["removed"].(float64) != 2 {
		t.Fatalf("replace: %v", out)
	}
	if s, _ := e.do(t, "DELETE", "/v1/price-list/1001", nil); s != 204 {
		t.Fatalf("delete: %d", s)
	}
	if s, out := e.do(t, "PUT", "/v1/price-list", map[string]any{"prices": []any{
		map[string]any{"local_code": "X1", "name": "Dressing", "unit_price": -1},
	}}); s != 422 {
		t.Fatalf("negative price accepted: %d %v", s, out)
	}
}
