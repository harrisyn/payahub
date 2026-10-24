package assist

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/harrisyn/payahub/internal/catalog"
)

// The regression checks for matching: known clinic names and the standard
// item each is (or that nothing is), kept as data. Run them on word matching
// in every test run, and on a model before it is trusted with a change of
// model, provider or prompt.
//
//go:embed checks.json
var checksJSON []byte

// Case is one known answer. Cases in one Group differ only in strength or
// form, so each must get a different answer: a changed input must change
// the output.
type Case struct {
	Name  string           `json:"name"`
	Kind  catalog.ItemKind `json:"kind,omitempty"`
	Want  string           `json:"want"`
	Group string           `json:"group,omitempty"`
}

// Cases are the known answers.
func Cases() []Case {
	var f struct {
		Cases []Case `json:"cases"`
	}
	if err := json.Unmarshal(checksJSON, &f); err != nil {
		panic("assist: checks.json: " + err.Error())
	}
	return f.Cases
}

// Picker answers own items: a pick (Found or not) for each code it could
// answer, or an error when it could not answer at all.
type Picker func(ctx context.Context, own []Own) (map[string]Pick, error)

// CaseResult is how one case went.
type CaseResult struct {
	Case
	Got        string  `json:"got"`
	Confidence float64 `json:"confidence"`
	Answered   bool    `json:"answered"`
	Pass       bool    `json:"pass"`
}

// GroupResult says whether the cases of a group got as many answers as they
// have cases.
type GroupResult struct {
	Group string   `json:"group"`
	Cases []string `json:"cases"`
	Got   []string `json:"got"`
	Pass  bool     `json:"pass"`
}

// Report is one run of the checks.
type Report struct {
	Method       string        `json:"method"` // words | model
	Total        int           `json:"total"`
	Passed       int           `json:"passed"`
	Unanswered   int           `json:"unanswered"`
	Groups       int           `json:"groups"`
	GroupsPassed int           `json:"groups_passed"`
	Failures     []CaseResult  `json:"failures"`
	GroupFails   []GroupResult `json:"group_failures"`
	TookMS       int64         `json:"took_ms"`
	Err          string        `json:"error,omitempty"`
}

// OK reports whether every case and every group passed.
func (r Report) OK() bool {
	return r.Err == "" && r.Passed == r.Total && r.GroupsPassed == r.Groups
}

// RunChecks answers every case with pick and compares.
func RunChecks(ctx context.Context, method string, cases []Case, pick Picker) Report {
	start := time.Now()
	rep := Report{Method: method, Total: len(cases), Failures: []CaseResult{}, GroupFails: []GroupResult{}}
	own := make([]Own, len(cases))
	for i, c := range cases {
		own[i] = Own{Code: fmt.Sprintf("C%03d", i+1), Name: c.Name, Kind: c.Kind}
	}
	picks, err := pick(ctx, own)
	rep.TookMS = time.Since(start).Milliseconds()
	if err != nil {
		rep.Err = err.Error()
		return rep
	}
	results := make([]CaseResult, len(cases))
	groups := map[string][]int{}
	for i, c := range cases {
		r := CaseResult{Case: c}
		if p, ok := picks[own[i].Code]; ok {
			r.Answered, r.Confidence = true, p.Confidence
			if p.Found {
				r.Got = p.Item.ID
			}
		} else {
			rep.Unanswered++
		}
		r.Pass = r.Answered && r.Got == c.Want
		if r.Pass {
			rep.Passed++
		} else {
			rep.Failures = append(rep.Failures, r)
		}
		results[i] = r
		if c.Group != "" {
			groups[c.Group] = append(groups[c.Group], i)
		}
	}
	names := make([]string, 0, len(groups))
	for g := range groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		gr := GroupResult{Group: g, Pass: true}
		for _, i := range groups[g] {
			got := results[i].Got
			if got == "" || slices.Contains(gr.Got, got) {
				gr.Pass = false
			}
			gr.Cases = append(gr.Cases, cases[i].Name)
			gr.Got = append(gr.Got, got)
		}
		rep.Groups++
		if gr.Pass {
			rep.GroupsPassed++
		} else {
			rep.GroupFails = append(rep.GroupFails, gr)
		}
	}
	return rep
}

// WordPicker answers with the catalogue's word matching: its top suggestion
// when that is at least 0.5 and ahead of the next, else nothing.
func WordPicker(_ context.Context, own []Own) (map[string]Pick, error) {
	out := map[string]Pick{}
	for _, o := range own {
		sg := catalog.Suggest(o.Name, o.Kind, 2)
		p := Pick{Code: o.Code}
		if len(sg) > 0 && sg[0].Confidence >= 0.5 && (len(sg) == 1 || sg[1].Confidence < sg[0].Confidence) {
			p.Item, p.Found, p.Confidence = sg[0].Item, true, sg[0].Confidence
		}
		out[o.Code] = p
	}
	return out, nil
}

// ModelPicker answers with a model through c; the runs go to record.
func ModelPicker(c Completer, record func([]Run)) Picker {
	return func(ctx context.Context, own []Own) (map[string]Pick, error) {
		picks, runs := MatchItems(ctx, c, own)
		if record != nil {
			record(runs)
		}
		var failed error
		for _, r := range runs {
			if r.Err != nil {
				failed = r.Err
			}
		}
		if len(picks) == 0 && failed != nil {
			return nil, failed
		}
		return picks, nil
	}
}
