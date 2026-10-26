package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strings"
	"sync"
	"testing"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/deid"
)

// fake answers with fn's JSON and remembers what it was asked.
type fake struct {
	mu   sync.Mutex
	reqs []ai.Request
	fn   func(r ai.Request) any
}

func (f *fake) Complete(_ context.Context, r ai.Request) (ai.Response, error) {
	for _, p := range r.Parts {
		m := p.Data
		if m == nil {
			m = []byte(p.Text)
		}
		if !r.Cleared.Covers(m) {
			return ai.Response{}, ai.ErrNotDeidentified
		}
	}
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
			"payer_name": v("GLICO", 0.95),
			"gender":     v("Female", 0.95), "provider_name": v("Ridge Clinic", 0.98), "encounter_date": v("2026-10-03", 0.96),
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
	x, run, err := ReadClaimForm(context.Background(), f, formOCR{}, Document{Name: "form.png", MediaType: "image/png", Data: blank(400, 120)}, 0.9)
	if err != nil || run.Err != nil {
		t.Fatal(err, run.Err)
	}
	c := x.Claim
	if c.PayerID == "" || x.PayerName != "GLICO" {
		t.Errorf("payer: %q from %q", c.PayerID, x.PayerName)
	}
	if c.Member.BirthDate != nil || x.Fields["member.birth_date"].Read != "30/02/1990" || !x.Fields["member.birth_date"].NeedsReview {
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
	if x.Fields["lines.0.unit_price"].NeedsReview != true || x.Fields["provider.name"].NeedsReview {
		t.Error("the threshold was not applied per field")
	}
	// The identity came from this machine's reading, for a person to check.
	if c.Member.FirstName != "Ama" || c.Member.LastName != "Mensah" || c.Member.MemberNumber != "GL-123456" || !x.Fields["member.last_name"].NeedsReview {
		t.Errorf("identity: %+v %+v", c.Member, x.Fields["member.last_name"])
	}
	if x.Privacy.Method != "image" || x.Privacy.Removed["region"] == 0 || run.Privacy.Method == "" {
		t.Errorf("privacy: %+v %+v", x.Privacy, run.Privacy)
	}
	if x.Review == 0 || x.Evidence.PromptVersion != ReadPrompt || x.Evidence.Origin != "ai_extracted" {
		t.Errorf("summary: %+v", x)
	}
	// The model got a page with the name blacked out, not the file.
	sent := f.reqs[0].Parts[0]
	if sent.MediaType != "image/png" || bytes.Equal(sent.Data, blank(400, 120)) {
		t.Fatal("the page was not redacted")
	}
	img, _ := png.Decode(bytes.NewReader(sent.Data))
	if r, g, b, _ := img.At(150, 15).RGBA(); r|g|b != 0 {
		t.Error("the name is not blacked out")
	}
	if r, _, _, _ := img.At(150, 100).RGBA(); r == 0 {
		t.Error("the bill was blacked out")
	}
}

// formOCR reads a small form: the name, NHIS number and birth date on their
// own lines, and a bill line below.
type formOCR struct{}

func (formOCR) Words(context.Context, image.Image) ([]deid.Word, error) {
	w := func(text string, x, y, line int) deid.Word {
		return deid.Word{Text: text, Box: image.Rect(x, y, x+len(text)*8, y+14), Line: fmt.Sprint(line)}
	}
	return []deid.Word{
		w("Name:", 10, 10, 1), w("Ama", 100, 10, 1), w("Mensah", 140, 10, 1), w("Sex:", 300, 10, 1), w("F", 340, 10, 1),
		w("NHIS", 10, 40, 2), w("No:", 50, 40, 2), w("GL-123456", 100, 40, 2),
		w("DOB:", 10, 70, 3), w("30/02/1990", 100, 70, 3),
		w("Paracetamol", 10, 100, 4), w("1,250.50", 200, 100, 4),
	}, nil
}

func blank(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestReadClaimFormNeedsTheNameFound(t *testing.T) {
	f := &fake{fn: func(ai.Request) any { return map[string]any{} }}
	_, _, err := ReadClaimForm(context.Background(), f, noLabels{}, Document{MediaType: "image/png", Data: blank(50, 50)}, 0.9)
	if !errors.Is(err, deid.ErrNameNotFound) || len(f.reqs) != 0 {
		t.Fatalf("a form with no name label was sent: %v", err)
	}
}

type noLabels struct{}

func (noLabels) Words(context.Context, image.Image) ([]deid.Word, error) {
	return []deid.Word{{Text: "Kwame", Box: image.Rect(1, 1, 30, 10), Line: "1"}}, nil
}

func TestReadClaimFormRefusesOtherFiles(t *testing.T) {
	if _, _, err := ReadClaimForm(context.Background(), &fake{}, formOCR{}, Document{MediaType: "text/html"}, 0.9); err == nil {
		t.Fatal("read an HTML file")
	}
}
