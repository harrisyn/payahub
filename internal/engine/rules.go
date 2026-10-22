package engine

import (
	"fmt"

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

// Check runs the payer-neutral edit rules. Payer-specific rules arrive later
// as data per connector, not as branches here.
func Check(c *claim.Claim, cfg connector.SubmissionConfig) []Issue {
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
			issues = appendEvidence(issues, l.Mapping.Evidence, field)
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
		if d.Mapping != nil {
			issues = appendEvidence(issues, d.Mapping.Evidence, field)
		}
	}
	return issues
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
func Validate(c *claim.Claim, m connector.Manifest, overrides []byte) Validation {
	cfg := ResolveConfig(m, overrides)
	v := Validation{Issues: Check(c, cfg), Plan: ChoosePlan(m, cfg), State: claim.Ready}
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
