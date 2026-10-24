package claim

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Review is a person's confirmation of an issue that needs one (an AI value
// below its threshold, a possible duplicate). Confirmed issues stop holding
// the claim back; the record stays on the claim for audit.
type Review struct {
	Code  string    `json:"code"`
	Field string    `json:"field"`
	By    string    `json:"by"`
	Note  string    `json:"note,omitempty"`
	At    time.Time `json:"at"`
}

// Reviewed reports whether a person confirmed the issue code on field.
func (c *Claim) Reviewed(code, field string) bool {
	return slices.ContainsFunc(c.Reviews, func(r Review) bool { return r.Code == code && r.Field == field })
}

// Duplicate is another claim for the same member, payer and visit date.
// Exact means its lines are the same too.
type Duplicate struct {
	ID    string `json:"id"`
	State State  `json:"state"`
	Exact bool   `json:"exact"`
}

// LinesKey is the claim's lines reduced to what they bill (item or code,
// quantity, price), in a fixed order: two claims with the same key charge
// for the same things.
func LinesKey(lines []Line) string {
	parts := make([]string, 0, len(lines))
	for _, l := range lines {
		what := l.Item
		if what == "" && l.Code != nil {
			what = l.Code.System + ":" + l.Code.Value
		}
		if what == "" {
			what = strings.ToLower(strings.Join(strings.Fields(l.Description), " "))
		}
		qty := l.Quantity
		if qty == 0 {
			qty = 1
		}
		parts = append(parts, fmt.Sprintf("%s|%d|%d", what, qty, l.UnitPrice))
	}
	slices.Sort(parts)
	return strings.Join(parts, ";")
}
