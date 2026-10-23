package assist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/harrisyn/payahub/internal/ai"
)

// fake answers with fn's JSON and remembers what it was asked.
type fake struct {
	mu   sync.Mutex
	reqs []ai.Request
	fn   func(r ai.Request) any
}

func (f *fake) Complete(_ context.Context, r ai.Request) (ai.Response, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	b, _ := json.Marshal(f.fn(r))
	return ai.Response{Text: "Here it is:\n```json\n" + string(b) + "\n```", Provider: "fake", Model: "fake-1"}, nil
}

func TestMatchItemsKeepsOnlyCatalogueAnswers(t *testing.T) {
	f := &fake{fn: func(r ai.Request) any {
		return map[string]any{"matches": []any{
			map[string]any{"code": "PCM", "item_id": "MED-0001", "confidence": 0.97, "reason": "paracetamol 500 mg tablet"},
			map[string]any{"code": "FBC", "item_id": "LAB-9999", "confidence": 0.99, "reason": "invented"},
			map[string]any{"code": "XRAY", "item_id": "MED-0001", "confidence": 0.9, "reason": "wrong kind"},
			map[string]any{"code": "GIFT", "item_id": "", "confidence": 0.8, "reason": "a gift voucher"},
			map[string]any{"code": "NOT-ASKED", "item_id": "LAB-0001", "confidence": 0.9, "reason": "?"},
		}}
	}}
	own := []Own{{Code: "PCM", Name: "PCM 500 tabs"}, {Code: "FBC", Name: "FBC"}, {Code: "XRAY", Name: "Chest xray", Kind: "imaging"}, {Code: "GIFT", Name: "Gift voucher"}}
	picks, runs := MatchItems(context.Background(), f, own)
	if len(runs) != 1 || runs[0].Err != nil || runs[0].PromptVersion != MatchPrompt || runs[0].PromptHash == "" {
		t.Fatalf("runs: %+v", runs)
	}
	if p := picks["PCM"]; !p.Found || p.Item.ID != "MED-0001" || p.Evidence.Origin != "ai_mapped" || p.Evidence.ModelVersion != "fake-1" || *p.Evidence.Confidence != 0.97 {
		t.Errorf("PCM: %+v", p)
	}
	for _, c := range []string{"FBC", "XRAY", "NOT-ASKED"} {
		if _, ok := picks[c]; ok {
			t.Errorf("%s kept an answer it should not have: %+v", c, picks[c])
		}
	}
	if p, ok := picks["GIFT"]; !ok || p.Found {
		t.Errorf("GIFT should be a 'no match' pick: %+v", p)
	}
	// The catalogue is sent, and the clinic's text cannot break the list.
	if !strings.Contains(f.reqs[0].Parts[0].Text, "MED-0001 | medication | Paracetamol 500 mg tablet") {
		t.Error("catalogue not in the request")
	}
}

func TestMatchItemsBatches(t *testing.T) {
	f := &fake{fn: func(r ai.Request) any { return map[string]any{"matches": []any{}} }}
	var own []Own
	for i := range 85 {
		own = append(own, Own{Code: fmt.Sprint(i), Name: "item | with\nnewline"})
	}
	_, runs := MatchItems(context.Background(), f, own)
	if len(runs) != 3 || len(f.reqs) != 3 {
		t.Fatalf("%d runs for 85 items", len(runs))
	}
	if strings.Contains(f.reqs[0].Parts[1].Text, "with\nnewline") {
		t.Error("a newline in a clinic name reached the list")
	}
}

func v(value string, conf float64) map[string]any {
	return map[string]any{"value": value, "confidence": conf, "quote": value}
}

func TestReadClaimFormChecksEveryValue(t *testing.T) {
	f := &fake{fn: func(r ai.Request) any {
		return map[string]any{
			"payer_name": v("GLICO", 0.95), "member_number": v("GL-123456", 0.97), "policy_number": v("", 0),
			"first_name": v("Ama", 0.99), "last_name": v("Mensah", 0.99), "birth_date": v("1990-02-30", 0.9),
			"gender": v("Female", 0.95), "provider_name": v("Ridge Clinic", 0.98), "encounter_date": v("2026-10-03", 0.96),
			"discharge_date": v("", 0), "authorization_code": v("", 0),
			"diagnoses": []any{
				map[string]any{"text": v("Malaria", 0.95), "icd10": v("B54", 0.94), "primary": true},
				map[string]any{"text": v("Typhoid fever", 0.8), "icd10": v("ZZZ", 0.4), "primary": false},
			},
			"lines": []any{
				map[string]any{"description": v("Paracetamol 500 mg tablet", 0.95), "quantity": v("10", 0.9), "unit_price": v("1,250.50", 0.85), "service_date": v("", 0)},
				map[string]any{"description": v("Consultation", 0.97), "quantity": v("x", 0.3), "unit_price": v("free", 0.5), "service_date": v("2026-10-03", 0.9)},
			},
		}
	}}
	x, run, err := ReadClaimForm(context.Background(), f, Document{Name: "form.pdf", MediaType: "application/pdf", Data: []byte("%PDF-1.7")}, 0.9)
	if err != nil || run.Err != nil {
		t.Fatal(err, run.Err)
	}
	c := x.Claim
	if c.PayerID == "" || x.PayerName != "GLICO" {
		t.Errorf("payer: %q from %q", c.PayerID, x.PayerName)
	}
	if c.Member.BirthDate != nil || x.Fields["member.birth_date"].Read != "1990-02-30" || !x.Fields["member.birth_date"].NeedsReview {
		t.Errorf("an impossible date was kept: %v %+v", c.Member.BirthDate, x.Fields["member.birth_date"])
	}
	if c.Member.Gender != "female" || c.Encounter.StartDate.Format("2006-01-02") != "2026-10-03" {
		t.Errorf("member/encounter: %+v %v", c.Member, c.Encounter.StartDate)
	}
	if len(c.Diagnoses) != 2 || c.Diagnoses[0].Code.Value != "B54" || c.Diagnoses[0].Role != "primary" {
		t.Fatalf("diagnoses: %+v", c.Diagnoses)
	}
	if f := x.Fields["diagnoses.1.code"]; !f.NeedsReview || c.Diagnoses[1].Code.Value == "ZZZ" {
		t.Errorf("a bad code was trusted: %q %+v", c.Diagnoses[1].Code.Value, f)
	}
	if len(c.Lines) != 2 || c.Lines[0].Quantity != 10 || c.Lines[0].UnitPrice != 125050 {
		t.Fatalf("lines: %+v", c.Lines)
	}
	if c.Lines[0].Code == nil || c.Lines[0].Code.Value != "MED-0001" {
		t.Errorf("line item not matched by the catalogue: %+v", c.Lines[0].Code)
	}
	if c.Lines[1].Quantity != 1 || c.Lines[1].UnitPrice != 0 || !x.Fields["lines.1.unit_price"].NeedsReview {
		t.Errorf("bad quantity or price kept: %+v %+v", c.Lines[1], x.Fields["lines.1.unit_price"])
	}
	if x.Fields["lines.0.unit_price"].NeedsReview != true || x.Fields["member.member_number"].NeedsReview {
		t.Error("the threshold was not applied per field")
	}
	if x.Review == 0 || x.Evidence.PromptVersion != ReadPrompt || x.Evidence.Origin != "ai_extracted" {
		t.Errorf("summary: %+v", x)
	}
	if f.reqs[0].Parts[0].MediaType != "application/pdf" {
		t.Error("the document was not sent")
	}
}

func TestReadClaimFormRefusesOtherFiles(t *testing.T) {
	if _, _, err := ReadClaimForm(context.Background(), &fake{}, Document{MediaType: "text/html"}, 0.9); err == nil {
		t.Fatal("read an HTML file")
	}
}
