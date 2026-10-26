package assist

import (
	"context"
	"strings"
	"testing"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/catalog"
)

func TestCasesNameRealItems(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Cases() {
		if seen[c.Name] {
			t.Errorf("%q twice", c.Name)
		}
		seen[c.Name] = true
		if _, ok := catalog.LookupItem(c.Want); c.Want != "" && !ok {
			t.Errorf("%q wants unknown item %s", c.Name, c.Want)
		}
	}
}

// Word matching keeps every known answer; a change to the catalogue or the
// matcher that loses one fails here.
func TestWordMatchingKeepsKnownAnswers(t *testing.T) {
	rep := RunChecks(context.Background(), "words", Cases(), WordPicker)
	for _, f := range rep.Failures {
		t.Errorf("%q: got %q (%.2f), want %q", f.Name, f.Got, f.Confidence, f.Want)
	}
	for _, g := range rep.GroupFails {
		t.Errorf("group %s: %v answered %v", g.Group, g.Cases, g.Got)
	}
	if !rep.OK() || rep.Total < 50 {
		t.Fatalf("report: %+v", rep)
	}
}

// answering builds a model that answers each clinic item it is asked about
// with answer(name).
func answering(answer func(name string) string) *fake {
	return &fake{fn: func(r ai.Request) any {
		var ms []map[string]any
		for _, line := range strings.Split(r.Parts[1].Text, "\n")[1:] {
			f := strings.Split(line, " | ")
			if len(f) < 2 {
				continue
			}
			ms = append(ms, map[string]any{"code": f[0], "item_id": answer(f[1]), "confidence": 0.9, "reason": "x"})
		}
		return map[string]any{"matches": ms}
	}}
}

func TestModelChecks(t *testing.T) {
	want := map[string]string{}
	for _, c := range Cases() {
		want[c.Name] = c.Want
	}
	var runs int
	good := RunChecks(context.Background(), "model", Cases(),
		ModelPicker(answering(func(n string) string { return want[n] }), func(r []Run) { runs += len(r) }))
	if !good.OK() || runs != 2 {
		t.Fatalf("a model with every answer right: %+v, %d runs", good, runs)
	}

	// A model (or a stale cache) that gives one answer whatever it is asked
	// fails the groups as well as the cases.
	same := RunChecks(context.Background(), "model", Cases(), ModelPicker(answering(func(string) string { return "MED-0005" }), nil))
	if same.OK() || same.GroupsPassed != 0 || same.Passed != 1 {
		t.Fatalf("a model that ignores its input: passed %d, groups %d of %d", same.Passed, same.GroupsPassed, same.Groups)
	}

	// A model that cannot be reached is an error, not a pass.
	down := RunChecks(context.Background(), "model", Cases(), ModelPicker(&fake{fn: func(ai.Request) any { return "not json" }}, nil))
	if down.OK() || down.Err == "" {
		t.Fatalf("unreachable model: %+v", down)
	}
}
