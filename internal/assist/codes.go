package assist

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/deid"
	"github.com/harrisyn/payahub/internal/icd10"
)

// PayerCodesPrompt is the version of the payer code instructions. Change it
// with any change to payerCodesSystem or the schema.
const PayerCodesPrompt = "payer-codes-v1"

const payerCodesSystem = `You find an insurer's own code for each item of a standard catalogue of health services, tests and medicines.

For each standard item, choose the one code from the insurer's list that bills the same thing: the same service or test, or the same medicine in the same strength and form. Insurer lists use short forms and brand names; read them as a claims officer would. When candidates are given for an item, choose only from them.

If no code in the list is the same thing, answer an empty code. A near miss is not a match: a different strength, form, test or body part is a different item. Choosing nothing is better than choosing wrongly.

confidence is the probability that your choice is right (or, with an empty code, that nothing matches). reason states in a few plain words why, for example "same drug, strength and form".`

var payerCodesSchema = object(map[string]any{
	"matches": array(object(map[string]any{
		"item_id":    str,
		"code":       str,
		"confidence": num,
		"reason":     str,
	})),
})

// CodePick is the model's choice of the payer's code for one standard item,
// checked against the payer's list. Found is false when it found none.
type CodePick struct {
	ItemID     string            `json:"item_id"`
	Entry      catalog.ListEntry `json:"entry"`
	Found      bool              `json:"found"`
	Confidence float64           `json:"confidence"`
	Reason     string            `json:"reason"`
	Evidence   claim.Evidence    `json:"evidence"`
}

// fullList is the most list entries sent whole; a longer list is narrowed to
// each item's closest entries by word matching first.
const (
	fullList   = 1500
	candidates = 15
)

// MatchPayerCodes asks the model for the payer's code behind each standard
// item, from the payer's own list. A code that is not in the list, or not
// among the candidates the item was shown, is dropped.
func MatchPayerCodes(ctx context.Context, c Completer, payerID string, itemIDs []string) (map[string]CodePick, []Run) {
	list := catalog.PayerList(payerID)
	whole := len(list) <= fullList
	var listText string
	if whole {
		var b strings.Builder
		b.WriteString("Insurer's list (code | name | group):\n")
		for _, e := range list {
			fmt.Fprintf(&b, "%s | %s | %s\n", oneLine(e.Code), oneLine(e.Name), oneLine(e.Group))
		}
		listText = b.String()
	}
	type ask struct {
		item    catalog.Item
		allowed map[string]bool // nil: the whole list
	}
	var asks []ask
	for _, id := range itemIDs {
		it, ok := catalog.LookupItem(id)
		if !ok {
			continue
		}
		a := ask{item: it}
		if !whole {
			a.allowed = map[string]bool{}
			for _, s := range catalog.SuggestPayerCodes(payerID, id, candidates) {
				a.allowed[s.Code] = true
			}
			if len(a.allowed) == 0 {
				continue // nothing in a long list comes near it
			}
		}
		asks = append(asks, a)
	}

	picks := map[string]CodePick{}
	var runs []Run
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for start := 0; start < len(asks); start += matchBatch {
		batch := asks[start:min(start+matchBatch, len(asks))]
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			var b strings.Builder
			b.WriteString("Standard items (id | kind | name | generic | synonyms")
			if !whole {
				b.WriteString(" | candidate codes")
			}
			b.WriteString("):\n")
			asked := map[string]ask{}
			for _, a := range batch {
				it := a.item
				fmt.Fprintf(&b, "%s | %s | %s | %s | %s", it.ID, it.Kind, it.Name, it.Generic, strings.Join(it.Synonyms, ", "))
				if !whole {
					var cs []string
					for _, s := range catalog.SuggestPayerCodes(payerID, it.ID, candidates) {
						cs = append(cs, oneLine(s.Code)+" = "+oneLine(s.Name))
					}
					b.WriteString(" | " + strings.Join(cs, "; "))
				}
				b.WriteString("\n")
				asked[it.ID] = a
			}
			parts := []ai.Part{}
			var cl []deid.Cleared
			if whole {
				t, c1 := deid.Catalogue(listText)
				parts, cl = append(parts, ai.Text(t)), append(cl, c1)
			}
			t, c2 := deid.Catalogue(b.String())
			parts, cl = append(parts, ai.Text(t)), append(cl, c2)
			req := ai.Request{Task: ai.TaskCodeMatching, System: payerCodesSystem, Parts: parts, Cleared: deid.Join(cl...),
				Schema: payerCodesSchema, SchemaName: "payer_codes", MaxTokens: 8000}
			var out struct {
				Matches []struct {
					ItemID     string  `json:"item_id"`
					Code       string  `json:"code"`
					Confidence float64 `json:"confidence"`
					Reason     string  `json:"reason"`
				} `json:"matches"`
			}
			run := call(ctx, c, req, PayerCodesPrompt, []byte(payerID+"\n"+b.String()), &out)
			got := map[string]CodePick{}
			if run.Err == nil {
				for _, m := range out.Matches {
					a, ok := asked[m.ItemID]
					if !ok {
						continue
					}
					conf := clamp(m.Confidence)
					ev := claim.Evidence{Origin: claim.OriginAIMapped, Confidence: &conf, ModelVersion: run.Model,
						PromptVersion: PayerCodesPrompt, PromptHash: run.PromptHash, Assertion: strings.TrimSpace(m.Reason)}
					p := CodePick{ItemID: m.ItemID, Confidence: conf, Reason: ev.Assertion, Evidence: ev}
					if code := strings.TrimSpace(m.Code); code != "" {
						e, ok := catalog.ListEntryFor(payerID, code)
						if !ok || (a.allowed != nil && !a.allowed[code]) {
							continue // not in the list, or not among its candidates: no pick at all
						}
						p.Entry, p.Found = e, true
						// A pick whose strength or form differs from the item's is
						// never a sure one, whatever the model says.
						if ok, why := catalog.Fits(m.ItemID, e.Name); !ok {
							p.Confidence = min(p.Confidence, 0.5)
							p.Reason = strings.TrimSpace(p.Reason + " (the list entry names " + why + ")")
							p.Evidence.Confidence = &p.Confidence
							p.Evidence.Assertion = p.Reason
						}
					}
					got[m.ItemID] = p
				}
			}
			mu.Lock()
			defer mu.Unlock()
			runs = append(runs, run)
			for k, v := range got {
				picks[k] = v
			}
		})
	}
	wg.Wait()
	return picks, runs
}

// DiagnosesPrompt is the version of the diagnosis coding instructions.
const DiagnosesPrompt = "diagnoses-v1"

const diagnosesSystem = `You code diagnoses written by clinicians in Ghana to ICD-10 (WHO, with ICD-10-CM subcodes where they are more exact).

Clinicians write short forms (BP or HTN for hypertension, URTI, UTI, PUD, DM, AGE for acute gastroenteritis, RTA for a road traffic injury) and local usage. Read them as a clinical coder would. Choose the most specific code the words support; when they do not say more, choose the unspecified code (for example B54 for malaria with no species).

Some words are given with codes found by searching the ICD-10 titles; use them if they fit, but you are not limited to them. If the words are not a diagnosis, or are too vague to code, answer an empty code.

confidence is the probability that your code is right. reason states in a few plain words what the diagnosis is, for example "uncomplicated malaria, species not stated".`

var diagnosesSchema = object(map[string]any{
	"codes": array(object(map[string]any{
		"key":        str,
		"code":       str,
		"confidence": num,
		"reason":     str,
	})),
})

// DiagnosisText is a diagnosis in a clinician's words, to be coded.
type DiagnosisText struct {
	Key  string
	Text string
}

// DiagnosisPick is the model's ICD-10 code for one diagnosis, checked
// against the ICD-10 list. Found is false when it gave none.
type DiagnosisPick struct {
	Key        string         `json:"key"`
	Code       icd10.Code     `json:"code"`
	Found      bool           `json:"found"`
	Confidence float64        `json:"confidence"`
	Reason     string         `json:"reason"`
	Evidence   claim.Evidence `json:"evidence"`
}

// MatchDiagnoses asks the model for the ICD-10 code of each diagnosis. The
// words are scrubbed of id (and of anything shaped like an identifier) first.
// A code that is not in the ICD-10 list is dropped.
func MatchDiagnoses(ctx context.Context, c Completer, texts []DiagnosisText, id deid.Identity) (map[string]DiagnosisPick, []Run) {
	picks := map[string]DiagnosisPick{}
	var runs []Run
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for start := 0; start < len(texts); start += matchBatch {
		batch := texts[start:min(start+matchBatch, len(texts))]
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			var b strings.Builder
			b.WriteString("Diagnoses (key | words | codes found by searching the titles):\n")
			asked := map[string]bool{}
			var cls []deid.Cleared
			for _, t := range batch {
				// Each text on its own first, so a label at its start ("Name: ...") is seen.
				words, c1 := deid.Text(t.Text, id)
				cls = append(cls, c1)
				var found []string
				for _, h := range icd10.Search(words, 5) {
					found = append(found, h.Code+" "+h.Title)
				}
				fmt.Fprintf(&b, "%s | %s | %s\n", oneLine(t.Key), oneLine(words), strings.Join(found, "; "))
				asked[t.Key] = true
			}
			text, c2 := deid.Text(b.String(), id)
			cl := deid.Join(append(cls, c2)...)
			req := ai.Request{Task: ai.TaskCodeMatching, System: diagnosesSystem, Parts: []ai.Part{ai.Text(text)}, Cleared: cl,
				Schema: diagnosesSchema, SchemaName: "diagnosis_codes", MaxTokens: 4000}
			var out struct {
				Codes []struct {
					Key        string  `json:"key"`
					Code       string  `json:"code"`
					Confidence float64 `json:"confidence"`
					Reason     string  `json:"reason"`
				} `json:"codes"`
			}
			run := call(ctx, c, req, DiagnosesPrompt, []byte(text), &out)
			got := map[string]DiagnosisPick{}
			if run.Err == nil {
				for _, m := range out.Codes {
					if !asked[m.Key] {
						continue
					}
					conf := clamp(m.Confidence)
					ev := claim.Evidence{Origin: claim.OriginAIMapped, Confidence: &conf, ModelVersion: run.Model,
						PromptVersion: DiagnosesPrompt, PromptHash: run.PromptHash, Assertion: strings.TrimSpace(m.Reason)}
					p := DiagnosisPick{Key: m.Key, Confidence: conf, Reason: ev.Assertion, Evidence: ev}
					if v := strings.TrimSpace(m.Code); v != "" {
						code, st := icd10.Check(v)
						if st != icd10.Listed {
							continue // not a code in the list: no pick at all
						}
						p.Code, p.Found = code, true
					}
					got[m.Key] = p
				}
			}
			mu.Lock()
			defer mu.Unlock()
			runs = append(runs, run)
			for k, v := range got {
				picks[k] = v
			}
		})
	}
	wg.Wait()
	return picks, runs
}

// CodeDiagnoses asks the model for the ICD-10 code of each diagnosis the
// form gave in words only, in place of the closest title by its words. The
// patient's details read from the form are scrubbed from the words first. A
// code under threshold is marked for a person to check.
func CodeDiagnoses(ctx context.Context, c Completer, x *Extraction, threshold float64) []Run {
	if len(x.uncoded) == 0 {
		return nil
	}
	in := &x.Claim
	texts := make([]DiagnosisText, 0, len(x.uncoded))
	for _, i := range x.uncoded {
		texts = append(texts, DiagnosisText{Key: fmt.Sprint(i), Text: in.Diagnoses[i].Description})
	}
	m := in.Member
	id := deid.Identity{Names: []string{m.FirstName, m.LastName}, Numbers: []string{m.MemberNumber, m.PolicyNumber}, Phones: []string{m.Phone}}
	if m.BirthDate != nil {
		t := m.BirthDate.Time
		id.BirthDate = &t
	}
	picks, runs := MatchDiagnoses(ctx, c, texts, id)
	for _, i := range x.uncoded {
		p, ok := picks[fmt.Sprint(i)]
		if !ok || !p.Found {
			continue
		}
		path := fmt.Sprintf("diagnoses.%d.code", i)
		was := x.Fields[path]
		in.Diagnoses[i].Code.Value, in.Diagnoses[i].Code.Display = p.Code.Code, p.Code.Title
		f := Field{Confidence: p.Confidence, Quote: was.Quote, Read: was.Read, NeedsReview: p.Confidence < threshold,
			Note: "No code on the form; coded by the model from the words (" + p.Reason + ")."}
		if was.NeedsReview && !f.NeedsReview {
			x.Review--
		} else if !was.NeedsReview && f.NeedsReview {
			x.Review++
		}
		x.Fields[path] = f
	}
	return runs
}
