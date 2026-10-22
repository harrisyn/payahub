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
		"consult": "consultation", "xray": "x-ray", "gm": "g",
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
}

func indexItem(it Item) indexed {
	x := indexed{item: it}
	for _, s := range append([]string{it.Name, it.Generic}, it.Synonyms...) {
		if t := tokens(s); len(t) > 0 {
			x.phrases = append(x.phrases, t)
		}
	}
	x.numbers = numbers(tokens(it.Strength + " " + it.Name))
	return x
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
	q := tokens(name)
	if len(q) == 0 {
		return []Suggestion{}
	}
	qn := numbers(q)
	var out []Suggestion
	for _, x := range get().index {
		if kind != "" && x.item.Kind != kind {
			continue
		}
		best := 0.0
		for _, p := range x.phrases {
			score := 0.0
			if slices.Equal(q, p) {
				score = 0.97
			} else if related(q, p) {
				score = overlap(q, p)
			}
			best = max(best, score)
		}
		// A different strength is a different item, however close the words.
		if !sameStrength(qn, x.numbers) {
			best *= 0.5
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
