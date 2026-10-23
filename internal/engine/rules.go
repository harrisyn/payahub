package engine

import (
	"fmt"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/icd10"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityReview  Severity = "review"
)

type Issue struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Field    string   `json:"field"`
	Message  string   `json:"message"`
}

// Confidence thresholds are per use, never one global number (claims plan,
// guardrail 2). Starting values; tuned from payer outcomes.
var Thresholds = map[claim.FieldOrigin]float64{
	claim.OriginAIExtracted: 0.90,
	claim.OriginAIMapped:    0.95,
}

// Facts are what the rules need beyond the claim itself: today's date and
// the organisation's other claims that may be the same one.
type Facts struct {
	Today      time.Time
	Duplicates []claim.Duplicate
}

// DefaultFilingDays is the NHIS limit for submitting a claim (LI 1809).
const DefaultFilingDays = 60

// Check runs the payer-neutral edit rules. Payer-specific rules arrive later
// as data per connector, not as branches here.
func Check(c *claim.Claim, cfg connector.SubmissionConfig, f Facts) []Issue {
	var issues []Issue
	add := func(code string, sev Severity, field, msg string) {
		issues = append(issues, Issue{Code: code, Severity: sev, Field: field, Message: msg})
	}

	if len(c.Lines) == 0 {
		add("no_lines", SeverityError, "lines", "A claim needs at least one line.")
	}
	hasPrimary := false
	for _, d := range c.Diagnoses {
		hasPrimary = hasPrimary || d.Role == claim.Primary
	}
	if !hasPrimary {
		add("no_primary_diagnosis", SeverityError, "diagnoses", "Add a primary diagnosis.")
	}
	if e := c.Encounter.EndDate; e != nil && e.Before(c.Encounter.StartDate.Time) {
		add("dates_reversed", SeverityError, "encounter.end_date", "The encounter ends before it starts.")
	}
	issues = append(issues, filing(c, cfg, f.Today)...)
	issues = append(issues, duplicates(c, f.Duplicates)...)

	for i, l := range c.Lines {
		field := fmt.Sprintf("lines[%d].mapping", i)
		if !l.Mapping.Mapped() && cfg.UnmappedLinePolicy == "block" {
			msg := fmt.Sprintf("%q has no code for this payer.", l.Description)
			switch {
			case l.Item != "":
				msg = fmt.Sprintf("This payer has no code for %q (%s) yet.", l.Description, l.Item)
			case l.Code != nil:
				msg = fmt.Sprintf("%q: map your code %q to a standard item first.", l.Description, l.Code.Value)
			}
			add("line_unmapped", SeverityError, field, msg)
		}
		if l.Mapping != nil {
			if !c.Reviewed("needs_review", field) {
				issues = appendEvidence(issues, l.Mapping.Evidence, field)
			}
		}
	}
	for i, d := range c.Diagnoses {
		if icd10.Is(d.Code.System) {
			field := fmt.Sprintf("diagnoses[%d].code", i)
			switch _, st := icd10.Check(d.Code.Value); st {
			case icd10.Invalid:
				add("diagnosis_invalid", SeverityError, field, fmt.Sprintf("%q is not an ICD-10 code (like B50.9).", d.Code.Value))
			case icd10.Unknown:
				add("diagnosis_unknown", SeverityWarning, field, d.Code.Value+" is not in ICD-10; the payer may reject it.")
			}
		}
		field := fmt.Sprintf("diagnoses[%d].mapping", i)
		switch {
		case !d.Mapping.Mapped() && cfg.UnmappedDiagnosisPolicy == "block":
			add("diagnosis_unmapped", SeverityError, field, d.Code.Value+" has no code for this payer.")
		case !d.Mapping.Mapped() && cfg.DiagnosisCodeSource == "payer_code":
			add("diagnosis_may_be_rejected", SeverityWarning, field,
				d.Code.Value+" is sent as plain ICD-10; this payer expects its own codes.")
		}
		if d.Mapping != nil && !c.Reviewed("needs_review", field) {
			issues = appendEvidence(issues, d.Mapping.Evidence, field)
		}
	}
	return issues
}

// filing checks the visit date against today and the payer's deadline.
func filing(c *claim.Claim, cfg connector.SubmissionConfig, today time.Time) []Issue {
	if today.IsZero() || c.Encounter.StartDate.IsZero() {
		return nil
	}
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC) }
	today = day(today)
	last := day(c.Encounter.StartDate.Time)
	if e := c.Encounter.EndDate; e != nil && e.After(last) {
		last = day(e.Time)
	}
	if last.After(today.AddDate(0, 0, 1)) { // a day's grace for time zones
		return []Issue{{Code: "visit_in_future", Severity: SeverityError, Field: "encounter.start_date",
			Message: "The visit date is in the future."}}
	}
	days := cfg.FilingDays
	if days <= 0 {
		days = DefaultFilingDays
	}
	deadline := last.AddDate(0, 0, days)
	switch {
	case today.After(deadline):
		sev := SeverityWarning
		msg := fmt.Sprintf("Filed %d days after the visit; this payer accepts claims for %d days, so it may refuse it.", int(today.Sub(last).Hours()/24), days)
		if cfg.LateFilingPolicy == "block" {
			sev = SeverityError
			msg = fmt.Sprintf("Too late to file: this payer accepts claims for %d days after the visit (deadline %s).", days, deadline.Format("2 Jan 2006"))
		}
		return []Issue{{Code: "filing_deadline_passed", Severity: sev, Field: "encounter.start_date", Message: msg}}
	case !today.Before(deadline.AddDate(0, 0, -7)):
		return []Issue{{Code: "filing_deadline_near", Severity: SeverityWarning, Field: "encounter.start_date",
			Message: fmt.Sprintf("Send by %s: the payer's %d-day filing deadline.", deadline.Format("2 Jan 2006"), days)}}
	}
	return nil
}

// duplicates flags a claim that repeats another: the same lines need a
// person to confirm it is a separate visit; different lines only a warning.
func duplicates(c *claim.Claim, ds []claim.Duplicate) []Issue {
	var out []Issue
	for _, d := range ds {
		if d.Exact {
			if c.Reviewed("duplicate_claim", "claim:"+d.ID) {
				continue
			}
			out = append(out, Issue{Code: "duplicate_claim", Severity: SeverityReview, Field: "claim:" + d.ID,
				Message: fmt.Sprintf("Claim %s has the same member, insurer, visit date and lines (%s). Confirm this is a separate claim before sending.", d.ID, d.State)})
		} else {
			out = append(out, Issue{Code: "possible_duplicate", Severity: SeverityWarning, Field: "claim:" + d.ID,
				Message: fmt.Sprintf("Claim %s is for the same member, insurer and visit date, with different lines.", d.ID)})
		}
	}
	return out
}

func appendEvidence(issues []Issue, e *claim.Evidence, field string) []Issue {
	if e == nil || e.ConfirmedBy != "" {
		return issues
	}
	threshold, ok := Thresholds[e.Origin]
	if !ok || (e.Confidence != nil && *e.Confidence >= threshold) {
		return issues
	}
	return append(issues, Issue{
		Code: "needs_review", Severity: SeverityReview, Field: field,
		Message: fmt.Sprintf("AI-proposed value below the %.0f%% threshold; a person must confirm it.", threshold*100),
	})
}

type Validation struct {
	Issues []Issue     `json:"issues"`
	Plan   Plan        `json:"submission_plan"`
	State  claim.State `json:"state"`
}

// Validate runs the rules and picks the plan. State is where the claim lands:
// draft (errors), needs_review (AI fields below threshold) or ready.
func Validate(c *claim.Claim, m connector.Manifest, overrides []byte, f Facts) Validation {
	cfg := ResolveConfig(m, overrides)
	v := Validation{Issues: Check(c, cfg, f), Plan: ChoosePlan(m, cfg), State: claim.Ready}
	if v.Issues == nil {
		v.Issues = []Issue{}
	}
	for _, i := range v.Issues {
		if i.Severity == SeverityError {
			v.State = claim.Draft
			return v
		}
		if i.Severity == SeverityReview {
			v.State = claim.NeedsReview
		}
	}
	return v
}
