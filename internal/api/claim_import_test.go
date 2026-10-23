package api_test

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func TestImportClaimsFromASpreadsheet(t *testing.T) {
	e := setup(t)
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	// A price list entry lets a row leave out its price.
	e.do(t, "PUT", "/v1/price-list", map[string]any{"prices": []any{
		map[string]any{"local_code": "FBC" + tag, "name": "Full blood count", "unit_price": 4500},
	}})
	e.do(t, "PUT", "/v1/item-mappings", map[string]any{"mappings": []any{
		map[string]any{"local_code": "FBC" + tag, "item_id": "LAB-0001"},
	}})
	csv := "Claim No,Payer,Member No,Patient Name,Visit Date,Diagnosis,Item Code,Description,Qty,Unit Price\n" +
		"A" + tag + ",Sandbox Mutual,M-1,Ama Boateng,21/10/2026,B50.9;R50.9,MED-0003,Coartem,1,30.00\n" +
		"A" + tag + ",,,,,,FBC" + tag + ",,1,\n" +
		"B" + tag + ",sandbox_mutual,M-2,Kojo Asante,2026-10-21,malaria,MED-0001,Paracetamol,2,0.50\n" +
		"C" + tag + ",sandbox_mutual,M-3,Esi Mensah,45951,J06.9,NOPE9,Something,1,\n" +
		"D" + tag + ",nobody,M-4,Yaw Ofori,2026-10-21,J06.9,MED-0001,Paracetamol,1,1.00\n"
	file := base64.StdEncoding.EncodeToString([]byte(csv))

	s, out := e.do(t, "POST", "/v1/claims/import", map[string]any{"file": file, "dry_run": true})
	if s != 200 {
		t.Fatalf("dry run: %d %v", s, out)
	}
	claims := out["claims"].([]any)
	if len(claims) != 4 || out["ready"].(float64) != 1 {
		t.Fatalf("dry run: %v", out)
	}
	a := claims[0].(map[string]any)
	if a["lines"].(float64) != 2 || a["total"].(float64) != 3000+4500 || a["state"] != "ready" || len(a["rows"].([]any)) != 2 {
		t.Fatalf("A = %v", a)
	}
	b := claims[1].(map[string]any)
	if b["state"] != "draft" || !hasIssue(b["issues"], "diagnosis_invalid") {
		t.Fatalf("B = %v", b)
	}
	if c := claims[2].(map[string]any); len(c["errors"].([]any)) == 0 {
		t.Fatalf("C (no price anywhere) = %v", c)
	}
	if d := claims[3].(map[string]any); len(d["errors"].([]any)) == 0 {
		t.Fatalf("D (unknown payer) = %v", d)
	}
	if a["claim_id"] != nil {
		t.Fatalf("dry run saved: %v", a)
	}

	s, out = e.do(t, "POST", "/v1/claims/import", map[string]any{"file": file, "submit": true})
	if s != 200 {
		t.Fatalf("import: %d %v", s, out)
	}
	a = out["claims"].([]any)[0].(map[string]any)
	if a["claim_id"] == nil || a["queued"] != true {
		t.Fatalf("A not saved and queued: %v", a)
	}
	if b := out["claims"].([]any)[1].(map[string]any); b["claim_id"] == nil || b["queued"] == true {
		t.Fatalf("B should be saved as a draft, not sent: %v", b)
	}
	// The same file again creates nothing new.
	_, again := e.do(t, "POST", "/v1/claims/import", map[string]any{"file": file})
	if a2 := again["claims"].([]any)[0].(map[string]any); a2["claim_id"] != a["claim_id"] || a2["replayed"] != true {
		t.Fatalf("re-import made a new claim: %v", a2)
	}
	_, c := e.do(t, "GET", "/v1/claims/"+a["claim_id"].(string), nil)
	if c["provenance"].(map[string]any)["source"] != "batch" || c["member"].(map[string]any)["first_name"] != "Ama" {
		t.Fatalf("claim = %v", c)
	}
}

func hasIssue(list any, code string) bool {
	for _, i := range list.([]any) {
		if i.(map[string]any)["code"] == code {
			return true
		}
	}
	return false
}
