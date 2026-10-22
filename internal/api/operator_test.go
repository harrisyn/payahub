package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/store"
)

func operatorSession(t *testing.T, e env) env {
	t.Helper()
	ses, acct := signup(t, e)
	if err := st.SetOperator(context.Background(), acct["email"].(string), true); err != nil {
		t.Fatal(err)
	}
	// Sessions are cached; sign in again for a token that carries the right.
	_, login := e.do(t, "POST", "/v1/sessions", map[string]any{"email": acct["email"], "password": "correct horse battery"})
	_ = ses
	return env{srv: e.srv, key: login["session_token"].(string)}
}

func TestOnlyOperatorsChangeTheCatalogue(t *testing.T) {
	e := setup(t)
	ses, _ := signup(t, e)
	if s, out := ses.do(t, "GET", "/v1/operator/payers", nil); s != 403 || out["error"].(map[string]any)["code"] != "operator_required" {
		t.Fatalf("a clinic user reached the operator routes: %d %v", s, out)
	}
	if s, _ := e.do(t, "GET", "/v1/operator/payers", nil); s != 403 {
		t.Fatalf("an API key reached the operator routes: %d", s)
	}
	op := operatorSession(t, e)
	if _, org := op.do(t, "GET", "/v1/organization", nil); org["operator"] != true {
		t.Fatalf("operator flag: %v", org["operator"])
	}
}

func TestOperatorAddsAPayerLoadsItsListAndMapsCodes(t *testing.T) {
	e := setup(t)
	op := operatorSession(t, e)
	id := "acacia_" + strings.ToLower(store.NewID("t")[2:10])

	if s, out := op.do(t, "PUT", "/v1/operator/payers/"+id, map[string]any{
		"name": "Acacia Health", "country": "gh", "connector": "medserve", "fields": map[string]any{"nope": "x"},
	}); s != 422 || out["error"].(map[string]any)["details"].(map[string]any)["fields.nope"] == nil {
		t.Fatalf("unknown field accepted: %d %v", s, out)
	}
	s, p := op.do(t, "PUT", "/v1/operator/payers/"+id, map[string]any{
		"name": "Acacia Health", "country": "gh", "connector": "medserve", "fields": map[string]any{"insurer_id": "12"},
	})
	if s != 201 || p["status"] != "connecting" || len(p["missing_fields"].([]any)) != 1 {
		t.Fatalf("create: %d %v", s, p)
	}
	s, p = op.do(t, "PUT", "/v1/operator/payers/"+id, map[string]any{
		"name": "Acacia Health", "country": "GH", "connector": "medserve", "fields": map[string]any{"insurer_code": "acacia", "insurer_id": "12"},
	})
	if s != 200 || p["status"] != "available" || p["kind"] != "integrated" {
		t.Fatalf("ready once its insurer code is set: %d %v", s, p)
	}
	if _, ok := catalog.LookupPayer(id); !ok {
		t.Fatalf("the catalogue was not refreshed")
	}
	// Clinics see it straight away.
	if _, list := e.do(t, "GET", "/v1/payers/"+id, nil); list["name"] != "Acacia Health" {
		t.Fatalf("public view: %v", list)
	}

	csv := "Code,Description,Category,Price\n" +
		"PARA500,Paracetamol 500mg tablet,Drugs,0.50\n" +
		"CONS-GP,General practitioner consultation,Services,\"1,200.00\"\n" +
		",no code,,\n" +
		"PARA500,duplicate,,\n" +
		"BAD,Bad price,,abc\n"
	s, out := op.do(t, "PUT", "/v1/operator/payers/"+id+"/list", map[string]any{"csv": csv})
	if s != 200 || out["entries"] != 2.0 || len(out["skipped"].([]any)) != 3 {
		t.Fatalf("list: %d %v", s, out)
	}
	if e, ok := catalog.ListEntryFor(id, "CONS-GP"); !ok || *e.Price != 120000 {
		t.Fatalf("price in minor units: %+v", e)
	}

	s, codes := op.do(t, "GET", "/v1/operator/payers/"+id+"/codes", nil)
	if s != 200 || codes["list_size"] != 2.0 {
		t.Fatalf("codes: %d %v", s, codes)
	}
	var paraItem string
	for _, row := range codes["data"].([]any) {
		r := row.(map[string]any)
		sg := r["suggestions"].([]any)
		if len(sg) > 0 && sg[0].(map[string]any)["code"] == "PARA500" {
			paraItem = r["item"].(map[string]any)["id"].(string)
			break
		}
	}
	if paraItem == "" {
		t.Fatalf("no item suggested PARA500")
	}

	s, out = op.do(t, "PUT", "/v1/operator/payers/"+id+"/codes", map[string]any{"changes": []any{
		map[string]any{"item_id": paraItem, "code": "PARA500"},
		map[string]any{"item_id": "SRV-0001", "code": "NOT-LISTED"},
	}})
	if s != 200 || out["coded_items"] != 2.0 || len(out["warnings"].([]any)) != 1 {
		t.Fatalf("set codes: %d %v", s, out)
	}
	if pc, ok := catalog.CodeFor(id, paraItem); !ok || pc.Description != "Paracetamol 500mg tablet" {
		t.Fatalf("description from the list: %+v", pc)
	}
	op.do(t, "PUT", "/v1/operator/payers/"+id+"/codes", map[string]any{"changes": []any{
		map[string]any{"item_id": "SRV-0001", "code": ""},
	}})
	if _, ok := catalog.CodeFor(id, "SRV-0001"); ok {
		t.Fatalf("an empty code removes it")
	}

	_, changes := op.do(t, "GET", "/v1/operator/payers/"+id+"/changes", nil)
	if n := len(changes["data"].([]any)); n != 5 {
		t.Fatalf("every change is logged: %d", n)
	}

	// Deactivated: kept for old claims, offered to no one, refused on new claims.
	op.do(t, "PUT", "/v1/operator/payers/"+id, map[string]any{
		"name": "Acacia Health", "country": "GH", "connector": "medserve", "inactive": true,
		"fields": map[string]any{"insurer_code": "acacia"},
	})
	_, list := e.do(t, "GET", "/v1/payers", nil)
	for _, p := range list["data"].([]any) {
		if p.(map[string]any)["id"] == id {
			t.Fatalf("inactive payer still offered")
		}
	}
}

func TestSeedNeverOverwritesAnOperatorsEdit(t *testing.T) {
	e := setup(t)
	op := operatorSession(t, e)
	ctx := context.Background()
	note := "Reached through XCeli. Edited " + store.NewID("n")
	s, _ := op.do(t, "PUT", "/v1/operator/payers/glico", map[string]any{
		"name": "GLICO Healthcare", "country": "GH", "connector": "xceli", "notes": note,
	})
	if s != 200 {
		t.Fatalf("edit glico: %d", s)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.RefreshCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if p, _ := catalog.LookupPayer("glico"); p.Notes != note {
		t.Fatalf("seed overwrote the edit: %+v", p)
	}
}
