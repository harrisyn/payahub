// Package icd10 is the ICD-10 diagnosis list: checking a claim's diagnosis
// codes and finding a code by its words.
package icd10

import (
	"bufio"
	"compress/gzip"
	"embed"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
)

//go:embed data/icd10cm.tsv.gz data/who.tsv
var data embed.FS

// System is how a claim names ICD-10. "icd10" and other spellings are read
// as the same (Is).
const System = "icd-10"

// Is reports whether a code system name means ICD-10.
func Is(system string) bool {
	s := strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(system))
	return s == "icd10" || s == "icd10cm" || s == "who:icd10"
}

type Code struct {
	Code     string `json:"code"` // dotted, as written: "B50.9"
	Title    string `json:"title"`
	Billable bool   `json:"billable"` // ICD-10-CM's flag: no finer code below it
}

type entry struct {
	compact string // "B509"
	code    Code
	words   []string
}

var (
	once     sync.Once
	entries  []entry // sorted by compact code
	byCode   map[string]int
	category map[string]bool
)

func load() {
	byCode = map[string]int{}
	category = map[string]bool{}
	read := func(r io.Reader) {
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			code, rest, _ := strings.Cut(sc.Text(), "\t")
			billable, title, _ := strings.Cut(rest, "\t")
			if code == "" {
				continue
			}
			if _, dup := byCode[code]; dup {
				continue
			}
			byCode[code] = len(entries)
			category[code[:3]] = true
			entries = append(entries, entry{compact: code, words: words(title),
				code: Code{Code: dotted(code), Title: title, Billable: billable == "1"}})
		}
	}
	f, err := data.Open("data/icd10cm.tsv.gz")
	if err != nil {
		panic(err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		panic(err)
	}
	read(gz)
	who, err := data.Open("data/who.tsv")
	if err != nil {
		panic(err)
	}
	read(who)
	sort.Slice(entries, func(i, j int) bool { return entries[i].compact < entries[j].compact })
	for i, e := range entries {
		byCode[e.compact] = i
	}
}

var codeRe = regexp.MustCompile(`^[A-Z][0-9][0-9A-Z][0-9A-Z]{0,4}$`)

// Compact turns a written code ("b50.9 ", "B509") into its compact form
// ("B509"), or reports that it cannot be an ICD-10 code.
func Compact(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '.'); i >= 0 {
		if i != 3 {
			return "", false
		}
		s = s[:3] + s[4:]
	}
	return s, codeRe.MatchString(s)
}

func dotted(compact string) string {
	if len(compact) <= 3 {
		return compact
	}
	return compact[:3] + "." + compact[3:]
}

// Status of a code against the list.
type Status int

const (
	Invalid  Status = iota // not shaped like an ICD-10 code
	Unknown                // shaped like one, but its category is not in ICD-10
	Category               // its category is known; the code itself is not listed (a WHO subcode, say)
	Listed                 // in the list
)

// Check finds a written code in the list.
func Check(s string) (Code, Status) {
	once.Do(load)
	c, ok := Compact(s)
	if !ok {
		return Code{}, Invalid
	}
	if i, ok := byCode[c]; ok {
		return entries[i].code, Listed
	}
	if category[c[:3]] {
		if i, ok := byCode[c[:3]]; ok {
			return entries[i].code, Category
		}
		return Code{}, Category
	}
	return Code{}, Unknown
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// Search finds codes by code prefix ("B50", "b50.") or by words in the title
// ("falciparum malaria"): every word given must start a word of the title.
// Broader codes come first, then code order.
func Search(q string, limit int) []Code {
	once.Do(load)
	out := []Code{}
	q = strings.TrimSpace(q)
	if q == "" || limit <= 0 {
		return out
	}
	if c, ok := Compact(strings.TrimSuffix(q, ".")); ok || (len(q) <= 3 && codeRe.MatchString(strings.ToUpper(q)+"00")) {
		if !ok {
			c = strings.ToUpper(q)
		}
		i := sort.Search(len(entries), func(i int) bool { return entries[i].compact >= c })
		for ; i < len(entries) && strings.HasPrefix(entries[i].compact, c) && len(out) < limit; i++ {
			out = append(out, entries[i].code)
		}
		return out
	}
	qw := words(q)
	if len(qw) == 0 {
		return out
	}
	var hits []int
	for i, e := range entries {
		if matchAll(qw, e.words) {
			hits = append(hits, i)
		}
	}
	sort.SliceStable(hits, func(a, b int) bool {
		// hits are in code order already; broader codes first keeps the
		// chapters together (I10 before I15 before O16).
		return len(entries[hits[a]].compact) < len(entries[hits[b]].compact)
	})
	for _, i := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, entries[i].code)
	}
	return out
}

func matchAll(q, title []string) bool {
	for _, w := range q {
		found := false
		for _, t := range title {
			if strings.HasPrefix(t, w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
