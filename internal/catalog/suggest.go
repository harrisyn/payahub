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
)

type indexed struct {
	item    Item
	phrases [][]string // name, generic and each synonym, tokenised
	numbers []string   // the strength's numbers
}

func indexItem(it Item) indexed {
	x := indexed{item: it}
	for _, s := range append([]string{it.Name, it.Generic}, it.Synonyms...) {
		if t := tokens(s); len(t) > 0 {
			x.phrases = append(x.phrases, t)
		}
	}
	x.numbers = numbers(tokens(it.Strength))
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
	for _, x := range std.index {
		if kind != "" && x.item.Kind != kind {
			continue
		}
		best := 0.0
		for _, p := range x.phrases {
			score := overlap(q, p)
			if slices.Equal(q, p) {
				score = 0.97
			}
			best = max(best, score)
		}
		// A different strength is a different item, however close the words.
		if len(qn) > 0 && len(x.numbers) > 0 && !slices.Equal(qn, x.numbers) {
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
