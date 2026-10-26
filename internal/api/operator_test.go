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
	id := "acacia_" + strings.ReplaceAll(store.NewID("t"), "-", "")[22:34] // the random tail of a UUIDv7

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

// A payer's list carries its rules; claims to it are checked against them,
// and the payer's policy decides whether a price above the tariff stops one.
func TestPayerRulesFromItsList(t *testing.T) {
	e := setup(t)
	op := operatorSession(t, e)
	id := "baobab_" + strings.ReplaceAll(store.NewID("t"), "-", "")[22:34]
	if s, p := op.do(t, "PUT", "/v1/operator/payers/"+id, map[string]any{
		"name": "Baobab Health", "country": "GH", "connector": "medserve", "fields": map[string]any{"insurer_code": "baobab", "insurer_id": "7"},
	}); s != 201 {
		t.Fatalf("create: %d %v", s, p)
	}
	csv := "Code,Name,Price,Max quantity,Sex,Min age,Max age,Authorization\n" +
		"CONS,Consultation,150.00,,,,,\n" +
		"FBC,Full blood count,80.00,1,,,,\n" +
		"ANC,Antenatal care visit,50.00,,F,12,55,\n" +
		"MRI,MRI brain,900.00,,,,,yes\n" +
		"BADSEX,Bad,1.00,,X,,,\n" +
		"BADAGE,Bad,1.00,,,60,40,\n"
	s, out := op.do(t, "PUT", "/v1/operator/payers/"+id+"/list", map[string]any{"csv": csv})
	if s != 200 || out["entries"] != 4.0 || len(out["skipped"].([]any)) != 2 {
		t.Fatalf("list: %d %v", s, out)
	}
	if e, _ := catalog.ListEntryFor(id, "ANC"); e.Sex != "female" || *e.MinAge != 12 || *e.MaxAge != 55 {
		t.Fatalf("rules read: %+v", e)
	}
	op.do(t, "PUT", "/v1/operator/payers/"+id+"/codes", map[string]any{"changes": []any{
		map[string]any{"item_id": "SRV-0001", "code": "CONS"},
		map[string]any{"item_id": "LAB-0001", "code": "FBC"},
	}})

	body := claimBody("M-RULES-1")
	body["payer_id"] = id
	body["member"] = map[string]any{"member_number": "M-RULES-1", "gender": "male"}
	std := func(item string, qty, price int) map[string]any {
		return map[string]any{"description": item, "quantity": qty, "unit_price": price,
			"code": map[string]any{"system": "payahub", "value": item}}
	}
	body["lines"] = []any{std("SRV-0001", 1, 18000), std("LAB-0001", 2, 8000)}
	check := func() map[string]string {
		_, v := e.do(t, "POST", "/v1/claims/validate", body)
		got := map[string]string{}
		for _, i := range v["issues"].([]any) {
			m := i.(map[string]any)
			got[m["code"].(string)] = m["severity"].(string)
		}
		return got
	}
	if got := check(); got["price_above_tariff"] != "warning" || got["quantity_over_limit"] != "error" {
		t.Fatalf("issues: %v", got)
	}
	if _, pub := e.do(t, "GET", "/v1/payers/"+id, nil); pub["rules"].(map[string]any)["price_policy"] != "warn" ||
		pub["rules"].(map[string]any)["has_list"] != true {
		t.Fatalf("public rules: %v", pub["rules"])
	}

	if s, _ := op.do(t, "PUT", "/v1/operator/payers/"+id+"/rules", map[string]any{"price_policy": "maybe", "unlisted_policy": "warn"}); s != 422 {
		t.Fatalf("bad policy accepted: %d", s)
	}
	s, p := op.do(t, "PUT", "/v1/operator/payers/"+id+"/rules", map[string]any{"price_policy": "block", "unlisted_policy": "warn"})
	if s != 200 || p["rules"].(map[string]any)["price_policy"] != "block" {
		t.Fatalf("rules: %d %v", s, p)
	}
	if got := check(); got["price_above_tariff"] != "error" {
		t.Fatalf("block policy: %v", got)
	}
	if _, ch := op.do(t, "GET", "/v1/operator/payers/"+id+"/changes", nil); ch["data"].([]any)[0].(map[string]any)["kind"] != "rules_changed" {
		t.Fatalf("change history: %v", ch["data"])
	}
}
