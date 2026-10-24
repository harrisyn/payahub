// Package deid removes what identifies a patient before anything is sent to
// a language model. A patient's name, member, policy and card numbers,
// phone, email, address and exact date of birth never leave Payahub for a
// model; sex, age, diagnoses, tests and bill lines may.
//
// The gate is enforced, not advisory: every ai.Request carries a Cleared,
// and a Cleared can only be made here, from material this package has
// scrubbed. A provider refuses any part of a request that a Cleared does
// not cover, so unscrubbed text or an unredacted form cannot reach a model
// by mistake.
package deid

import (
	"crypto/sha256"
	"maps"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Identity is what is known about a patient before scrubbing: each of these
// values is removed wherever it appears, as well as anything shaped like an
// identifier.
type Identity struct {
	Names     []string // first, last and other names
	Numbers   []string // member, policy, Ghana Card and hospital numbers
	Phones    []string
	Emails    []string
	Addresses []string
	BirthDate *time.Time
}

// Report says what was removed, by kind, for the audit record. It never
// holds the removed values.
type Report struct {
	Method  string         `json:"method"` // text, catalogue or image
	Removed map[string]int `json:"removed,omitempty"`
	Pages   int            `json:"pages,omitempty"`
}

func (r *Report) add(kind string, n int) {
	if n == 0 {
		return
	}
	if r.Removed == nil {
		r.Removed = map[string]int{}
	}
	r.Removed[kind] += n
}

// Cleared vouches for the exact text and files that were scrubbed. Its zero
// value covers nothing.
type Cleared struct {
	digests map[[32]byte]struct{}
	Report  Report
}

func cleared(r Report, material ...[]byte) Cleared {
	c := Cleared{digests: map[[32]byte]struct{}{}, Report: r}
	for _, m := range material {
		c.digests[sha256.Sum256(m)] = struct{}{}
	}
	return c
}

// Covers reports whether b is material this package scrubbed.
func (c Cleared) Covers(b []byte) bool {
	_, ok := c.digests[sha256.Sum256(b)]
	return ok
}

// Join covers everything each of cs covers.
func Join(cs ...Cleared) Cleared {
	out := Cleared{digests: map[[32]byte]struct{}{}}
	for _, c := range cs {
		maps.Copy(out.digests, c.digests)
		if out.Report.Method == "" {
			out.Report.Method = c.Report.Method
		} else if c.Report.Method != "" && !strings.Contains(out.Report.Method, c.Report.Method) {
			out.Report.Method += "+" + c.Report.Method
		}
		for k, n := range c.Report.Removed {
			out.Report.add(k, n)
		}
		out.Report.Pages += c.Report.Pages
	}
	return out
}

// Age is the patient's age in whole years on the given day: what a model
// may know instead of the date of birth.
func Age(birth, on time.Time) int {
	y := on.Year() - birth.Year()
	if on.Month() < birth.Month() || (on.Month() == birth.Month() && on.Day() < birth.Day()) {
		y--
	}
	return max(y, 0)
}

var (
	email    = regexp.MustCompile(`(?i)\b[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}\b`)
	ghCard   = regexp.MustCompile(`(?i)\bGHA[\s-]?\d{9}[\s-]?\d\b`)
	ghPost   = regexp.MustCompile(`\b[A-Z]{2}-\d{3,4}-\d{4}\b`) // GhanaPost digital address
	intlTel  = regexp.MustCompile(`\+\d[\d\s-]{7,15}\d`)
	ghTel    = regexp.MustCompile(`\b0[235]\d[\s-]?\d{3}[\s-]?\d{4}\b`)
	longNum  = regexp.MustCompile(`\b\d(?:[\s/-]?\d){7,}\b`) // member and card numbers; amounts carry a decimal point or are shorter
	labelled = regexp.MustCompile(`(?im)^([^\S\n]*(?:patient(?:'?s)?\s+name|name\s+of\s+patient|full\s+name|surname|other\s+names?|first\s+name|last\s+name|patient|name|member(?:ship)?\s*(?:no\.?|number|id)?|nhis\s*(?:no\.?|number|id)?|policy\s*(?:no\.?|number)?|card\s*(?:no\.?|number)|ghana\s+card|folder\s*(?:no\.?|number)|hospital\s*(?:no\.?|number)|opd\s*(?:no\.?|number)|phone|tel(?:ephone)?|mobile|contact|e-?mail|address|residence|house\s+(?:no\.?|number)|digital\s+address|next\s+of\s+kin|date\s+of\s+birth|d\.?o\.?b\.?|received\s+from|bill(?:ed)?\s+to)\s*[:#-][^\S\n]*)(\S[^\n]*)$`)
)

// Text scrubs text that may describe a patient: id's values, then anything
// shaped like an identifier, then the value after any identifying label
// ("Name:", "NHIS No:", "DOB:").
func Text(s string, id Identity) (string, Cleared) {
	r := Report{Method: "text"}
	s = known(s, id, &r)
	s = shapes(s, &r, true)
	s = labelled.ReplaceAllStringFunc(s, func(m string) string {
		r.add("labelled", 1)
		g := labelled.FindStringSubmatch(m)
		return g[1] + "[removed]"
	})
	return s, cleared(r, []byte(s))
}

// Catalogue scrubs text that should describe no patient (a clinic's price
// list, a fixed instruction), in case one slipped in. It keeps long numbers,
// which in a catalogue are product codes.
func Catalogue(s string) (string, Cleared) {
	r := Report{Method: "catalogue"}
	s = shapes(s, &r, false)
	return s, cleared(r, []byte(s))
}

func shapes(s string, r *Report, numbers bool) string {
	sub := func(re *regexp.Regexp, kind, with string) {
		s = re.ReplaceAllStringFunc(s, func(string) string { r.add(kind, 1); return with })
	}
	sub(email, "email", "[email]")
	sub(ghCard, "card_number", "[card number]")
	sub(ghPost, "address", "[address]")
	sub(intlTel, "phone", "[phone]")
	sub(ghTel, "phone", "[phone]")
	if numbers {
		s = longNum.ReplaceAllStringFunc(s, func(m string) string {
			if dateShaped.MatchString(m) {
				return m // a visit or service date; a known birth date is taken by Identity
			}
			r.add("number", 1)
			return "[number]"
		})
	}
	return s
}

func known(s string, id Identity, r *Report) string {
	// An email holds names; take it whole before the names are taken out.
	s = email.ReplaceAllStringFunc(s, func(string) string { r.add("email", 1); return "[email]" })
	replace := func(values []string, kind, with string, pattern func(string) string) {
		for _, v := range values {
			p := pattern(v)
			if p == "" {
				continue
			}
			re := regexp.MustCompile(p)
			s = re.ReplaceAllStringFunc(s, func(string) string { r.add(kind, 1); return with })
		}
	}
	replace(nameTokens(id.Names), "name", "[name]", func(v string) string {
		return `(?i)\b` + regexp.QuoteMeta(v) + `\b`
	})
	replace(id.Numbers, "number", "[number]", spaced)
	replace(id.Phones, "phone", "[phone]", func(v string) string {
		d := digits(v)
		if len(d) < 7 {
			return ""
		}
		if len(d) > 9 {
			d = d[len(d)-9:] // the local part, with or without +233 or 0
		}
		return spaced(d)
	})
	replace(id.Emails, "email", "[email]", func(v string) string { return `(?i)` + regexp.QuoteMeta(strings.TrimSpace(v)) })
	replace(id.Addresses, "address", "[address]", func(v string) string {
		if v = strings.TrimSpace(v); len(v) < 4 {
			return ""
		}
		return `(?i)` + strings.Join(strings.Fields(regexp.QuoteMeta(v)), `\s+`)
	})
	if b := id.BirthDate; b != nil {
		for _, f := range []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "02.01.2006", "2 January 2006", "2 Jan 2006", "January 2, 2006", "Jan 2, 2006", "02/01/06"} {
			v := b.Format(f)
			n := strings.Count(strings.ToLower(s), strings.ToLower(v))
			if n > 0 {
				s = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(v)).ReplaceAllString(s, "[date of birth]")
				r.add("birth_date", n)
			}
		}
	}
	return s
}

// nameTokens splits names into the words to remove, longest first so "Ama"
// does not eat the start of "Amankwah"; one-letter initials are left.
func nameTokens(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		for _, w := range strings.FieldsFunc(n, func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
			if lw := strings.ToLower(w); len([]rune(w)) >= 2 && !seen[lw] {
				seen[lw] = true
				out = append(out, w)
			}
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && len(out[j]) > len(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// spaced matches v's letters and digits with any spaces, dashes or slashes
// between them: "NHIS 1234-5678" matches "12345678".
func spaced(v string) string {
	var parts []string
	for _, r := range v {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			parts = append(parts, regexp.QuoteMeta(string(r)))
		}
	}
	if len(parts) < 4 {
		return ""
	}
	return `(?i)\b` + strings.Join(parts, `[\s/-]?`) + `\b`
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
