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
)

// MatchPrompt is the version of the matching instructions. Change it with
// any change to matchSystem or the schema.
const MatchPrompt = "match-items-v1"

const matchSystem = `You match a clinic's own names for services, tests and medicines to a standard catalogue.

For each clinic item, choose the one catalogue id that means the same thing: the same service or test, or the same medicine in the same strength and form. Clinic names use short forms (PCM for paracetamol, FBC for full blood count, OPD for outpatient) and brand names; read them as a pharmacist or lab clerk would.

If no catalogue entry is the same thing, answer an empty item_id. A near miss is not a match: a different strength, form, test or body part is a different item. Choosing nothing is better than choosing wrongly.

confidence is the probability that your choice is right (or, with an empty item_id, that nothing matches). reason states in a few plain words what the clinic item is, for example "paracetamol 500 mg tablet".`

var matchSchema = object(map[string]any{
	"matches": array(object(map[string]any{
		"code":       str,
		"item_id":    str,
		"confidence": num,
		"reason":     str,
	})),
})

// Own is an organisation's own item.
type Own struct {
	Code string
	Name string
	Kind catalog.ItemKind
}

// Pick is the model's choice for one own item, checked against the
// catalogue. ItemID is empty when the model found no match.
type Pick struct {
	Code       string
	Item       catalog.Item
	Found      bool
	Confidence float64
	Reason     string
	Evidence   claim.Evidence
}

const matchBatch = 40

// catalogueText lists every standard item once, in the form the model reads.
func catalogueText() string {
	var b strings.Builder
	b.WriteString("Catalogue (id | kind | name | generic | synonyms):\n")
	for _, k := range catalog.ItemKinds {
		for _, it := range catalog.Items(k) {
			fmt.Fprintf(&b, "%s | %s | %s | %s | %s\n", it.ID, it.Kind, it.Name, it.Generic, strings.Join(it.Synonyms, ", "))
		}
	}
	return b.String()
}

// MatchItems asks the model for the standard item behind each own item, in
// batches run side by side. Answers that name an unknown id, an item of the
// wrong kind or a code that was not asked about are dropped. It returns the
// checked picks by own code, and a Run for each call; a failed batch leaves
// its items without a pick.
func MatchItems(ctx context.Context, c Completer, own []Own) (map[string]Pick, []Run) {
	cat := catalogueText()
	picks := map[string]Pick{}
	var runs []Run
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for start := 0; start < len(own); start += matchBatch {
		batch := own[start:min(start+matchBatch, len(own))]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			p, run := matchBatchOf(ctx, c, cat, batch)
			mu.Lock()
			defer mu.Unlock()
			runs = append(runs, run)
			for k, v := range p {
				picks[k] = v
			}
		}()
	}
	wg.Wait()
	return picks, runs
}

func matchBatchOf(ctx context.Context, c Completer, cat string, batch []Own) (map[string]Pick, Run) {
	var list strings.Builder
	list.WriteString("Clinic items (code | name | kind if known):\n")
	asked := map[string]Own{}
	for _, o := range batch {
		fmt.Fprintf(&list, "%s | %s | %s\n", oneLine(o.Code), oneLine(o.Name), o.Kind)
		asked[o.Code] = o
	}
	// A price list names no patient; it is scrubbed all the same.
	catText, c1 := deid.Catalogue(cat)
	listText, c2 := deid.Catalogue(list.String())
	req := ai.Request{
		Task:       ai.TaskCodeMatching,
		System:     matchSystem,
		Parts:      []ai.Part{ai.Text(catText), ai.Text(listText)},
		Cleared:    deid.Join(c1, c2),
		Schema:     matchSchema,
		SchemaName: "item_matches",
		MaxTokens:  8000,
	}
	var out struct {
		Matches []struct {
			Code       string  `json:"code"`
			ItemID     string  `json:"item_id"`
			Confidence float64 `json:"confidence"`
			Reason     string  `json:"reason"`
		} `json:"matches"`
	}
	run := call(ctx, c, req, MatchPrompt, []byte(list.String()), &out)
	picks := map[string]Pick{}
	if run.Err != nil {
		return picks, run
	}
	for _, m := range out.Matches {
		o, ok := asked[m.Code]
		if !ok {
			continue
		}
		conf := clamp(m.Confidence)
		ev := claim.Evidence{Origin: claim.OriginAIMapped, Confidence: &conf, ModelVersion: run.Model,
			PromptVersion: MatchPrompt, PromptHash: run.PromptHash, Assertion: strings.TrimSpace(m.Reason)}
		p := Pick{Code: m.Code, Confidence: conf, Reason: ev.Assertion, Evidence: ev}
		if id := strings.TrimSpace(m.ItemID); id != "" {
			it, ok := catalog.LookupItem(id)
			if !ok || (o.Kind != "" && it.Kind != o.Kind) {
				continue // an invented id or the wrong kind: no pick at all
			}
			p.Item, p.Found = it, true
		}
		picks[m.Code] = p
	}
	return picks, run
}

// oneLine keeps a clinic's text from breaking the list it is quoted in.
func oneLine(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ", "|", "/").Replace(strings.TrimSpace(s))
}
