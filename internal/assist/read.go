package assist

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/deid"
	"github.com/harrisyn/payahub/internal/icd10"
)

// ReadPrompt is the version of the claim-form instructions.
const ReadPrompt = "read-claim-form-v2"

const readSystem = `You read one health insurance claim form, invoice or bill from a clinic in Ghana and copy what it says into fields.

Black boxes cover the patient's name, member and card numbers, date of birth, phone and address. They were removed on purpose before you were shown the document; do not guess what is under them, and do not copy any name you see.

Copy, do not infer. Each field has a value, a confidence and a quote:
- value: what the document says, normalised as each field describes. Leave it empty when the document does not say it or you cannot read it.
- confidence: the probability that value is exactly right. Handwriting, smudges, crossings-out and guesses lower it.
- quote: the words on the document the value came from, as written (empty when value is empty).

Dates are YYYY-MM-DD (Ghanaian forms write the day first: 03/10/2026 is 2026-10-03). Amounts are in cedis as a plain decimal number (GHS 1,250.00 is 1250.00). Gender is male or female. Diagnoses: text as written, and icd10 only when the form writes the code itself; never look a code up. Lines: one per service, test, procedure or medicine charged, with its quantity (1 when not written) and unit price; when only a line total is written, unit_price is the total divided by the quantity.

If the document is not a claim form, invoice or bill, return every field empty.`

func field() map[string]any {
	return object(map[string]any{"value": str, "confidence": num, "quote": str})
}

var readSchema = object(map[string]any{
	"payer_name":         field(),
	"gender":             field(),
	"provider_name":      field(),
	"encounter_date":     field(),
	"discharge_date":     field(),
	"authorization_code": field(),
	"diagnoses": array(object(map[string]any{
		"text":    field(),
		"icd10":   field(),
		"primary": map[string]any{"type": "boolean"},
	})),
	"lines": array(object(map[string]any{
		"description":  field(),
		"quantity":     field(),
		"unit_price":   field(),
		"service_date": field(),
	})),
})

type read struct {
	Value      string  `json:"value"`
	Confidence float64 `json:"confidence"`
	Quote      string  `json:"quote"`
}

// Field is what Payahub makes of one value the model read: how sure it is,
// the words it came from, and whether a person must look at it.
type Field struct {
	Confidence  float64 `json:"confidence"`
	Quote       string  `json:"quote,omitempty"`
	Read        string  `json:"read,omitempty"` // the model's value when it was changed or dropped
	Note        string  `json:"note,omitempty"`
	NeedsReview bool    `json:"needs_review"`
}

// Extraction is a claim proposed from a document. It is not saved: a person
// (or the organisation's system) checks it and creates the claim from it.
type Extraction struct {
	Claim     claim.Input      `json:"claim"`
	Fields    map[string]Field `json:"fields"` // by path: member.member_number, lines.0.unit_price
	PayerName string           `json:"payer_name,omitempty"`
	Review    int              `json:"review"` // how many fields need a person
	Evidence  claim.Evidence   `json:"evidence"`
	// Privacy says what was blacked out before the model saw the document,
	// and Pages are the blacked-out pages it saw (PNG).
	Privacy deid.Report `json:"privacy"`
	Pages   [][]byte    `json:"-"`
	// uncoded are the diagnoses the form gave no valid code for.
	uncoded []int
}

// Document is a file to read.
type Document struct {
	Name      string
	MediaType string
	Data      []byte
}

// MaxDocument is the largest file accepted, after any base64 the provider adds.
const MaxDocument = 10 << 20

var mediaTypes = map[string]bool{"application/pdf": true, "image/png": true, "image/jpeg": true, "image/webp": true}

// ReadClaimForm reads a claim form into a proposed claim. The patient's
// identity is blacked out on this server first (ocr reads the words), so
// the model sees none of it; the name, numbers and birth date are read
// locally from the blacked-out places instead, for a person to check. Each
// value is checked: dates and amounts must parse, ICD-10 codes must be in
// the list, the payer must be in the catalogue, and a line's standard item
// comes from the catalogue's own matching, never from the model.
func ReadClaimForm(ctx context.Context, c Completer, ocr deid.OCR, doc Document, threshold float64) (Extraction, Run, error) {
	if !mediaTypes[doc.MediaType] {
		return Extraction{}, Run{}, fmt.Errorf("a claim form must be a PDF, PNG, JPEG or WebP file")
	}
	red, err := deid.Redact(ctx, ocr, doc.Data, doc.MediaType, deid.Identity{})
	if err != nil {
		return Extraction{}, Run{}, err
	}
	ask, asked := deid.Catalogue("Read this document into the fields.")
	parts := make([]ai.Part, 0, len(red.Pages)+1)
	for i, pg := range red.Pages {
		parts = append(parts, ai.Part{MediaType: "image/png", Data: pg, Name: fmt.Sprintf("page-%d.png", i+1)})
	}
	req := ai.Request{
		Task:       ai.TaskDocumentIntake,
		System:     readSystem,
		Parts:      append(parts, ai.Text(ask)),
		Schema:     readSchema,
		SchemaName: "claim_form",
		MaxTokens:  16000,
		Cleared:    deid.Join(red.Cleared, asked),
	}
	var out struct {
		PayerName         read `json:"payer_name"`
		Gender            read `json:"gender"`
		ProviderName      read `json:"provider_name"`
		EncounterDate     read `json:"encounter_date"`
		DischargeDate     read `json:"discharge_date"`
		AuthorizationCode read `json:"authorization_code"`
		Diagnoses         []struct {
			Text    read `json:"text"`
			ICD10   read `json:"icd10"`
			Primary bool `json:"primary"`
		} `json:"diagnoses"`
		Lines []struct {
			Description read `json:"description"`
			Quantity    read `json:"quantity"`
			UnitPrice   read `json:"unit_price"`
			ServiceDate read `json:"service_date"`
		} `json:"lines"`
	}
	run := call(ctx, c, req, ReadPrompt, doc.Data, &out)
	if run.Err != nil {
		return Extraction{}, run, run.Err
	}

	x := Extraction{Fields: map[string]Field{}}
	conf := 0.0
	x.Evidence = claim.Evidence{Origin: claim.OriginAIExtracted, ModelVersion: run.Model, PromptVersion: ReadPrompt, PromptHash: run.PromptHash}
	put := func(path string, r read, value string, note string) string {
		f := Field{Confidence: clamp(r.Confidence), Quote: strings.TrimSpace(r.Quote)}
		if value == "" && strings.TrimSpace(r.Value) != "" {
			f.Read, f.Note, f.Confidence = r.Value, note, 0
		}
		if value != "" || f.Read != "" {
			f.NeedsReview = f.Confidence < threshold
			x.Fields[path] = f
			conf += f.Confidence
		}
		return value
	}
	text := func(r read) string { return strings.TrimSpace(r.Value) }

	in := &x.Claim
	in.Currency = "GHS"
	x.PayerName = text(out.PayerName)
	if p, ok := payerNamed(x.PayerName); ok {
		in.PayerID = put("payer_id", out.PayerName, p.ID, "")
	} else {
		x.Fields["payer_id"] = Field{Read: x.PayerName, Quote: strings.TrimSpace(out.PayerName.Quote), NeedsReview: true,
			Note: "No single insurer of this name in the catalogue; choose it."}
	}
	x.Privacy, x.Pages = red.Cleared.Report, red.Pages
	identity(&x, red.Found)
	if g := strings.ToLower(text(out.Gender)); g == "male" || g == "female" {
		in.Member.Gender = put("member.gender", out.Gender, g, "")
	} else {
		put("member.gender", out.Gender, "", "Not male or female.")
	}
	in.Provider.Name = put("provider.name", out.ProviderName, text(out.ProviderName), "")
	if d, ok := date(out.EncounterDate.Value); ok {
		put("encounter.start_date", out.EncounterDate, day(d), "")
		in.Encounter.StartDate = d
	} else {
		put("encounter.start_date", out.EncounterDate, "", "Not a date.")
	}
	if d, ok := date(out.DischargeDate.Value); ok && !d.Before(in.Encounter.StartDate.Time) {
		put("encounter.end_date", out.DischargeDate, day(d), "")
		in.Encounter.EndDate = &d
		in.Encounter.Type = claim.Inpatient
	} else {
		put("encounter.end_date", out.DischargeDate, "", "Not a date after the visit date.")
	}
	in.AuthorizationCode = put("authorization_code", out.AuthorizationCode, text(out.AuthorizationCode), "")

	primary := false
	for _, d := range out.Diagnoses {
		i := len(in.Diagnoses)
		desc := put(fmt.Sprintf("diagnoses.%d.description", i), d.Text, text(d.Text), "")
		dg := claim.Diagnosis{Description: desc, Role: claim.Secondary, Code: claim.Code{System: icd10.System}}
		if d.Primary && !primary {
			dg.Role, primary = claim.Primary, true
		}
		path := fmt.Sprintf("diagnoses.%d.code", i)
		code, st := icd10.Check(d.ICD10.Value)
		coded := st == icd10.Listed || st == icd10.Category
		if coded {
			dg.Code.Value, dg.Code.Display = put(path, d.ICD10, code.Code, ""), code.Title
		} else if hits := icd10.Search(desc, 1); desc != "" && len(hits) == 1 {
			// Found by its words: a suggestion for a person, never trusted.
			r := read{Value: hits[0].Code, Confidence: 0.5, Quote: d.Text.Quote}
			dg.Code.Value, dg.Code.Display = put(path, r, hits[0].Code, ""), hits[0].Title
			f := x.Fields[path]
			f.Note, f.NeedsReview = "No code on the form; this is the closest by its words.", true
			x.Fields[path] = f
		} else {
			put(path, d.ICD10, "", "Not an ICD-10 code; choose one.")
			x.Fields[path] = Field{Read: d.ICD10.Value, Note: "No ICD-10 code; choose one.", NeedsReview: true}
		}
		if dg.Code.Value == "" && desc == "" {
			continue
		}
		if !coded && desc != "" {
			x.uncoded = append(x.uncoded, i)
		}
		in.Diagnoses = append(in.Diagnoses, dg)
	}
	if !primary && len(in.Diagnoses) > 0 {
		in.Diagnoses[0].Role = claim.Primary
	}

	for _, l := range out.Lines {
		i := len(in.Lines)
		desc := text(l.Description)
		if desc == "" {
			continue
		}
		ln := claim.Line{Description: put(fmt.Sprintf("lines.%d.description", i), l.Description, desc, ""), Quantity: 1}
		if q, err := strconv.ParseInt(strings.TrimSpace(l.Quantity.Value), 10, 64); err == nil && q > 0 && q < 10000 {
			ln.Quantity = q
			put(fmt.Sprintf("lines.%d.quantity", i), l.Quantity, strconv.FormatInt(q, 10), "")
		} else {
			put(fmt.Sprintf("lines.%d.quantity", i), l.Quantity, "", "Not a whole number; 1 is assumed.")
		}
		if p, ok := cedis(l.UnitPrice.Value); ok {
			ln.UnitPrice = p
			put(fmt.Sprintf("lines.%d.unit_price", i), l.UnitPrice, strconv.FormatInt(p, 10), "")
		} else {
			put(fmt.Sprintf("lines.%d.unit_price", i), l.UnitPrice, "", "Not an amount.")
			x.Fields[fmt.Sprintf("lines.%d.unit_price", i)] = Field{Read: l.UnitPrice.Value, Note: "No price read; enter it, or leave it to the price list.", NeedsReview: true}
		}
		if d, ok := date(l.ServiceDate.Value); ok {
			put(fmt.Sprintf("lines.%d.service_date", i), l.ServiceDate, day(d), "")
			ln.ServiceDate = &d
		}
		if sg := catalog.Suggest(desc, "", 1); len(sg) == 1 && sg[0].Confidence >= 0.6 {
			ln.Code = &claim.Code{System: catalog.System, Value: sg[0].Item.ID, Display: sg[0].Item.Name}
			x.Fields[fmt.Sprintf("lines.%d.code", i)] = Field{Confidence: sg[0].Confidence, Note: "Matched by name: " + sg[0].Item.Name,
				NeedsReview: sg[0].Confidence < threshold}
		}
		in.Lines = append(in.Lines, ln)
	}
	for _, f := range x.Fields {
		if f.NeedsReview {
			x.Review++
		}
	}
	if n := len(x.Fields); n > 0 {
		avg := clamp(conf / float64(n))
		x.Evidence.Confidence = &avg
	}
	return x, run, nil
}

// identity fills the patient's details from what was read on this server
// in the blacked-out places. Printed-text reading is weaker on handwriting
// than a model, so a person checks every one.
func identity(x *Extraction, found map[string]string) {
	local := func(path, v string) string {
		if v = strings.TrimSpace(v); v == "" {
			return ""
		}
		x.Fields[path] = Field{Confidence: 0.5, Quote: v, NeedsReview: true,
			Note: "Read on Payahub's server from the part hidden from the model; check it."}
		return v
	}
	m := &x.Claim.Member
	first, last := found["first_name"], found["last_name"]
	if first == "" && last == "" {
		if f := strings.Fields(found["name"]); len(f) > 1 {
			first, last = strings.Join(f[:len(f)-1], " "), f[len(f)-1]
		} else if len(f) == 1 {
			last = f[0]
		}
	}
	m.FirstName = local("member.first_name", first)
	m.LastName = local("member.last_name", last)
	m.MemberNumber = local("member.member_number", found["member_number"])
	m.PolicyNumber = local("member.policy_number", found["policy_number"])
	if v := strings.TrimSpace(found["birth_date"]); v != "" {
		for _, f := range []string{"02/01/2006", "2/1/2006", "02-01-2006", "02.01.2006", "2006-01-02", "2 Jan 2006", "2 January 2006"} {
			if t, err := time.Parse(f, v); err == nil && t.Year() > 1900 && t.Before(time.Now()) {
				d := claim.Date{Time: t}
				local("member.birth_date", day(d))
				m.BirthDate = &d
				break
			}
		}
		if m.BirthDate == nil {
			x.Fields["member.birth_date"] = Field{Read: v, NeedsReview: true, Note: "Not a date; enter it."}
		}
	}
}

func date(s string) (claim.Date, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil || t.Year() < 1900 || t.After(time.Now().AddDate(0, 0, 1)) {
		return claim.Date{}, false
	}
	return claim.Date{Time: t}, true
}

// cedis reads "1,250.00" or "GHS 1250" as minor units.
func cedis(s string) (int64, bool) {
	s = strings.NewReplacer(",", "", "GHS", "", "GH₵", "", "₵", "", " ", "").Replace(strings.ToUpper(strings.TrimSpace(s)))
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || f > 10_000_000 {
		return 0, false
	}
	return int64(math.Round(f * 100)), true
}

// payerNamed finds a catalogue payer by the name written on a form: the
// same name, or one name containing the other, when only one payer fits.
func payerNamed(name string) (catalog.Payer, bool) {
	n := norm(name)
	if len(n) < 3 {
		return catalog.Payer{}, false
	}
	var hits []catalog.Payer
	for _, p := range catalog.Payers() {
		pn := norm(p.Name)
		if pn == n {
			return p, true
		}
		if strings.Contains(pn, n) || strings.Contains(n, pn) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 1 {
		return hits[0], true
	}
	return catalog.Payer{}, false
}

func norm(s string) string {
	var b strings.Builder
	for _, w := range strings.Fields(strings.ToLower(s)) {
		switch w {
		case "ltd", "ltd.", "limited", "company", "co", "co.", "insurance", "health", "medical", "scheme", "plc":
			continue
		}
		b.WriteString(w)
		b.WriteByte(' ')
	}
	return strings.TrimSpace(b.String())
}

func day(d claim.Date) string { return d.Format("2006-01-02") }
