package api_test

import (
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

func TestImportPayersFromSerenity(t *testing.T) {
	e := setup(t)
	op := operatorSession(t, e)
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000)
	csv := "name,platform,external_reference_id,country,is_active\n" +
		"Acme Health " + tag + ",SmartClaims,ACM" + tag + ",GH,true\n" +
		"Acme Health " + tag + ",SmartClaims,ACM" + tag + ",GH,true\n" + // another Serenity org's copy
		"Beta Mutual " + tag + ",Petherposh,beta" + tag + ",,false\n" +
		"Gamma Care " + tag + ",Manual portal,,GH,true\n" +
		"GLICO Healthcare,XCeli,https://glico.example/api,GH,true\n" +
		"Delta Life " + tag + ",SmartClaims,D1,GH,true\n" +
		"Delta Life " + tag + ",SmartClaims,D2,GH,true\n"
	body := map[string]any{"file": base64.StdEncoding.EncodeToString([]byte(csv)), "dry_run": true}

	s, out := op.do(t, "POST", "/v1/operator/payers/import", body)
	if s != 200 {
		t.Fatalf("dry run: %d %v", s, out)
	}
	actions := map[int]string{}
	for _, r := range out["rows"].([]any) {
		m := r.(map[string]any)
		actions[int(m["line"].(float64))] = m["action"].(string)
	}
	want := map[int]string{2: "add", 3: "same", 4: "add", 5: "skip", 6: "fill", 7: "add", 8: "conflict"}
	for line, a := range want {
		if actions[line] != a {
			t.Errorf("line %d: %s, want %s (%v)", line, actions[line], a, out["rows"])
		}
	}
	if out["saved"].(float64) != 0 {
		t.Fatalf("dry run saved: %v", out)
	}

	// Saved for real, without GLICO, so other tests keep its settings.
	csv = "name,platform,external_reference_id,country,is_active\n" +
		"Acme Health " + tag + ",SmartClaims,ACM" + tag + ",GH,true\n" +
		"Beta Mutual " + tag + ",Petherposh,beta" + tag + ",,false\n"
	body = map[string]any{"file": base64.StdEncoding.EncodeToString([]byte(csv))}
	if s, out := op.do(t, "POST", "/v1/operator/payers/import", body); s != 200 || out["saved"].(float64) != 2 {
		t.Fatalf("import: %d %v", s, out)
	}
	_, list := op.do(t, "GET", "/v1/operator/payers", nil)
	found := 0
	for _, p := range list["data"].([]any) {
		m := p.(map[string]any)
		switch m["id"] {
		case "acme_health_" + tag:
			found++
			if m["fields"].(map[string]any)["hicd"] != "ACM"+tag || m["status"] != "available" {
				t.Fatalf("acme = %v", m)
			}
		case "beta_mutual_" + tag:
			found++
			if m["inactive"] != true || m["fields"].(map[string]any)["insurer_code"] != "beta"+tag {
				t.Fatalf("beta = %v", m)
			}
		}
	}
	if found != 2 {
		t.Fatalf("imported payers missing: %v", list)
	}
	// Importing the same file again changes nothing.
	if _, out := op.do(t, "POST", "/v1/operator/payers/import", body); out["saved"].(float64) != 0 {
		t.Fatalf("re-import saved: %v", out)
	}
}
