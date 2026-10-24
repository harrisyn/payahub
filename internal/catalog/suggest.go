package catalog

import (
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Suggestion is a standard item that may be what an organisation's own code
// means. It is only a suggestion: a mapping counts once a person confirms it.
type Suggestion struct {
	Item       Item    `json:"item"`
	Confidence float64 `json:"confidence"`
}

// Words that carry no meaning for matching, and the common short forms on
// clinic price lists.
var (
	stop    = map[string]bool{"and": true, "of": true, "the": true, "per": true, "with": true, "for": true, "a": true}
	aliases = map[string]string{
		"tab": "tablet", "tabs": "tablet", "tablets": "tablet", "cap": "capsule", "caps": "capsule",
		"capsules": "capsule", "inj": "injection", "syr": "syrup", "susp": "suspension",
		"consult": "consultation", "xray": "x-ray", "gm": "g", "pcm": "paracetamol",
	}
	// forms are the words that say how a medicine is given. Words of one
	// family name the same form ("syrup" and "suspension" are both taken by
	// mouth as a liquid; "iv" is an injection or an infusion).
	forms = map[string]string{
		"tablet": "tablet", "capsule": "capsule", "syrup": "liquid", "suspension": "liquid", "mixture": "liquid",
		"elixir": "liquid", "injection": "injection", "infusion": "injection", "iv": "injection",
		"cream": "cream", "ointment": "ointment", "gel": "gel", "pessary": "pessary", "suppository": "suppository",
		"inhaler": "inhaler", "nebuliser": "nebuliser", "drops": "drops", "sachet": "sachet",
	}
	// generic words say what sort of thing an item is, not which one. Two
	// names that share only these and numbers ("Cetirizine 10 mg tablet",
	// "Amlodipine 10mg Tab"; "Pregnancy test", "Widal test") are not a match.
	generic = map[string]bool{
		"mg": true, "mcg": true, "g": true, "ml": true, "iu": true, "tablet": true, "capsule": true,
		"injection": true, "syrup": true, "suspension": true, "inhaler": true, "sachet": true, "cream": true,
		"test": true, "tests": true, "function": true, "consultation": true,
	}
)

// related reports whether two names share a word that says which item they
// are, not just its strength, form or sort.
func related(a, b []string) bool {
	for _, t := range a {
		if !generic[t] && !unicode.IsDigit(rune(t[0])) && slices.Contains(b, t) {
			return true
		}
	}
	return false
}

type indexed struct {
	item    Item
	phrases [][]string // name, generic and each synonym, tokenised
	numbers []string   // the numbers in its strength and name
	forms   []string   // the form families its form names
}

func indexItem(it Item) indexed {
	x := indexed{item: it}
	for _, s := range append([]string{it.Name, it.Generic}, it.Synonyms...) {
		if t := tokens(s); len(t) > 0 {
			x.phrases = append(x.phrases, t)
		}
	}
	x.numbers = numbers(tokens(it.Strength + " " + it.Name))
	x.forms = formsOf(tokens(it.Form))
	return x
}

func formsOf(ts []string) []string {
	var out []string
	for _, t := range ts {
		if f, ok := forms[t]; ok && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// sameForm reports whether a name's forms fit an item: a name that names no
// form fits any; one that does must share a form family with the item.
func sameForm(given, item []string) bool {
	if len(given) == 0 || len(item) == 0 {
		return true
	}
	for _, f := range given {
		if slices.Contains(item, f) {
			return true
		}
	}
	return false
}

// brandsOf collects the one-word other names of medicines (brands such as
// Flagyl or Coartem) that a clinic may write instead of the generic, so a
// name with the brand finds every strength and form of the generic. A word
// two generics claim is left out.
func brandsOf(items []Item) map[string][]string {
	out := map[string][]string{}
	claimed := map[string]string{}
	for _, it := range items {
		if it.Kind != Medication || it.Generic == "" {
			continue
		}
		gen := tokens(it.Generic)
		for _, syn := range it.Synonyms {
			t := tokens(syn)
			if len(t) != 1 || len(t[0]) < 4 || slices.Contains(gen, t[0]) {
				continue
			}
			w := t[0]
			if g, ok := claimed[w]; ok && g != it.Generic {
				out[w] = nil
				continue
			}
			claimed[w] = it.Generic
			if _, gone := out[w]; !gone || out[w] != nil {
				out[w] = gen
			}
		}
	}
	for w, g := range out {
		if g == nil {
			delete(out, w)
		}
	}
	return out
}

// expand writes a medicine's brand as its generic.
func expand(q []string, brands map[string][]string) []string {
	var out []string
	for _, t := range q {
		if g, ok := brands[t]; ok {
			out = append(out, g...)
			continue
		}
		out = append(out, t)
	}
	return out
}

// tokens lowercases, splits letters from digits ("500mg" -> 500, mg), drops
// stop words and expands short forms.
func tokens(s string) []string {
	var out []string
	var cur []rune
	var curDigit bool
	flush := func() {
		if len(cur) == 0 {
			return
		}
		w := string(cur)
		cur = cur[:0]
		if a, ok := aliases[w]; ok {
			w = a
		}
		if !stop[w] {
			out = append(out, w)
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '.' && curDigit:
			d := unicode.IsDigit(r) || r == '.'
			if len(cur) > 0 && d != curDigit {
				flush()
			}
			curDigit = d
			cur = append(cur, r)
		default:
			flush()
		}
	}
	flush()
	return packSize(out)
}

// packSize drops a pack size written after the strength ("Amlodipine 10mg
// tab x30", "x 100"): it says how many, not which item.
func packSize(ts []string) []string {
	out := ts[:0]
	for i := 0; i < len(ts); i++ {
		if ts[i] == "x" && i > 0 && i+1 < len(ts) && unicode.IsDigit(rune(ts[i+1][0])) {
			i++
			continue
		}
		out = append(out, ts[i])
	}
	return out
}

func numbers(ts []string) []string {
	var out []string
	for _, t := range ts {
		if t != "" && unicode.IsDigit(rune(t[0])) {
			out = append(out, t)
		}
	}
	return out
}

// sameStrength reports whether a name's numbers fit an item: every number
// the name gives is one of the item's ("Mixtard 30/70" fits "insulin
// biphasic 30/70 100 IU/ml"; "Ceftriaxone 1g" does not fit the 500 mg one).
func sameStrength(given, item []string) bool {
	if len(item) == 0 {
		return true
	}
	for _, n := range given {
		if !slices.Contains(item, n) {
			return false
		}
	}
	return true
}

func overlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	in := 0
	for _, t := range a {
		if slices.Contains(b, t) {
			in++
		}
	}
	union := len(a) + len(b) - in
	return float64(in) / float64(union)
}

// Suggest ranks standard items for an organisation's item name (and optional
// kind). The best few come back with a confidence between 0 and 1.
func Suggest(name string, kind ItemKind, limit int) []Suggestion {
	c := get()
	q := tokens(name)
	if len(q) == 0 {
		return []Suggestion{}
	}
	// A brand is tried as written (some name one product: Calpol is the
	// syrup) and as its generic, which scores a little lower.
	queries := [][]string{q}
	if e := expand(q, c.brands); !slices.Equal(e, q) {
		queries = append(queries, e)
	}
	qn, qf := numbers(q), formsOf(q)
	var out []Suggestion
	for _, x := range c.index {
		if kind != "" && x.item.Kind != kind {
			continue
		}
		best := 0.0
		for n, q := range queries {
			for _, p := range x.phrases {
				score := 0.0
				if slices.Equal(q, p) {
					score = 0.97
				} else if related(q, p) {
					score = overlap(q, p)
				}
				if n > 0 {
					score *= 0.9
				}
				best = max(best, score)
			}
		}
		// A different strength is a different item, however close the words.
		if !sameStrength(qn, x.numbers) {
			best *= 0.5
		}
		// So is a different form: a pessary is not a cream.
		if !sameForm(qf, x.forms) {
			best *= 0.6
		}
		if best >= 0.2 {
			out = append(out, Suggestion{Item: x.item, Confidence: float64(int(best*100)) / 100})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []Suggestion{}
	}
	return out
}

// CodeSuggestion is an entry of a payer's list that may be its code for a
// standard item. An operator confirms it before it is used.
type CodeSuggestion struct {
	ListEntry
	Confidence float64 `json:"confidence"`
}

// SuggestPayerCodes ranks the payer's own list entries for a standard item
// by the same word matching as Suggest: any phrase of the item (name, generic
// name, synonyms) against the entry's name, a different strength halving the
// score.
func SuggestPayerCodes(payerID, itemID string, limit int) []CodeSuggestion {
	c := get()
	it, ok := c.byItem[itemID]
	if !ok {
		return []CodeSuggestion{}
	}
	x := indexItem(it)
	out := []CodeSuggestion{}
	for _, e := range c.lists[payerID] {
		best := 0.0
		for _, p := range x.phrases {
			score := 0.0
			if slices.Equal(e.words, p) {
				score = 0.97
			} else if related(e.words, p) {
				score = overlap(e.words, p)
			}
			best = max(best, score)
		}
		if !sameStrength(e.numbers, x.numbers) {
			best *= 0.5
		}
		if best >= 0.2 {
			out = append(out, CodeSuggestion{ListEntry: e.entry, Confidence: float64(int(best*100)) / 100})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
