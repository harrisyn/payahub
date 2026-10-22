package claim

import (
	"encoding/json"
	"testing"
)

func TestTransitions(t *testing.T) {
	cases := []struct {
		from, to State
		ok       bool
	}{
		{Draft, Mapped, true},
		{Ready, Submitted, true},
		{Submitted, Acknowledged, true},
		{Submitted, Ready, true},
		{Draft, Submitted, false},
		{Paid, Draft, false},
		{Acknowledged, Withdrawn, true},
		{Approved, Withdrawn, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("%s -> %s: got %v want %v", c.from, c.to, got, c.ok)
		}
	}
	if _, err := Transition(Draft, Paid); err == nil {
		t.Error("expected an InvalidTransitionError")
	}
	if AdvanceToReady(Draft) != Ready || AdvanceToReady(NeedsReview) != Ready || AdvanceToReady(Ready) != Ready {
		t.Error("AdvanceToReady should reach ready from draft, needs_review and ready")
	}
}

func TestInputCheckAndNormalize(t *testing.T) {
	var in Input
	raw := `{"payer_id":"sandbox","member":{"member_number":"M1"},"provider":{"name":"Ridge"},
	         "encounter":{"start_date":"2026-10-20"},"lines":[{"description":"Consult","unit_price":15000}]}`
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatal(err)
	}
	n := 0
	in.Normalize(func() string { n++; return "ln_" + string(rune('a'+n)) })
	if errs := in.Check(); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if in.Currency != "GHS" || in.Lines[0].Quantity != 1 || in.Lines[0].ID == "" {
		t.Errorf("defaults not applied: %+v", in)
	}

	bad := Input{Currency: "cedi", Lines: []Line{{ID: "x", Quantity: 0, UnitPrice: -1}}}
	if errs := bad.Check(); len(errs) < 6 {
		t.Errorf("expected several field errors, got %v", errs)
	}
}

func TestDateJSON(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"2026-02-30"`), &d); err == nil {
		t.Error("expected an invalid date to fail")
	}
	if err := json.Unmarshal([]byte(`"2026-10-20"`), &d); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(d)
	if string(out) != `"2026-10-20"` {
		t.Errorf("got %s", out)
	}
}
