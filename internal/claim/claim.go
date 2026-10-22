// Package claim defines the canonical claim: one shape for every intake
// channel (REST, document, batch, web form) and every payer connector.
//
// Money is held in minor units (pesewas, cents) as int64 so totals are exact
// and cheap to compute. Fields an AI step proposed carry Evidence, so a
// reviewer or a payer audit can see where each value came from.
package claim

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Source string

const (
	SourceAPI      Source = "api"
	SourceDocument Source = "document"
	SourceBatch    Source = "batch"
	SourceForm     Source = "form"
)

// FieldOrigin says who set a value. Anything AI-set must be confirmed or be
// above its threshold before submission.
type FieldOrigin string

const (
	OriginSubmitted      FieldOrigin = "submitted"
	OriginAIExtracted    FieldOrigin = "ai_extracted"
	OriginAIMapped       FieldOrigin = "ai_mapped"
	OriginRuleDerived    FieldOrigin = "rule_derived"
	OriginHumanConfirmed FieldOrigin = "human_confirmed"
)

// Evidence records why a value is what it is. Required on every AI-proposed field.
type Evidence struct {
	Origin        FieldOrigin `json:"origin"`
	Confidence    *float64    `json:"confidence,omitempty"`
	ModelVersion  string      `json:"model_version,omitempty"`
	PromptVersion string      `json:"prompt_version,omitempty"`
	PromptHash    string      `json:"prompt_hash,omitempty"`
	// Assertion states the meaning in plain words ("office visit, established
	// patient, no modifier"): the plan's guard against confident wrong mappings.
	Assertion   string `json:"assertion,omitempty"`
	ConfirmedBy string `json:"confirmed_by,omitempty"`
}

// Code is a code in a named system: icd-10, cpt, nhia-gdrg, or a payer's own
// catalogue ("payer:<id>").
type Code struct {
	System  string `json:"system"`
	Value   string `json:"value"`
	Display string `json:"display,omitempty"`
}

type CodeMapping struct {
	Source   Code      `json:"source"`
	Target   *Code     `json:"target,omitempty"`
	Evidence *Evidence `json:"evidence,omitempty"`
}

func (m *CodeMapping) Mapped() bool { return m != nil && m.Target != nil && m.Target.Value != "" }

type Member struct {
	MemberNumber string `json:"member_number"`
	PolicyNumber string `json:"policy_number,omitempty"`
	FirstName    string `json:"first_name,omitempty"`
	LastName     string `json:"last_name,omitempty"`
	BirthDate    *Date  `json:"birth_date,omitempty"`
	Gender       string `json:"gender,omitempty"`
	Phone        string `json:"phone,omitempty"`
}

type Provider struct {
	Name           string `json:"name"`
	FacilityCode   string `json:"facility_code,omitempty"`
	PractitionerID string `json:"practitioner_id,omitempty"`
}

type EncounterType string

const (
	Outpatient EncounterType = "outpatient"
	Inpatient  EncounterType = "inpatient"
)

type Encounter struct {
	Type          EncounterType `json:"type,omitempty"`
	Reference     string        `json:"reference,omitempty"`
	StartDate     Date          `json:"start_date"`
	EndDate       *Date         `json:"end_date,omitempty"`
	AdmissionDate *Date         `json:"admission_date,omitempty"`
	DischargeDate *Date         `json:"discharge_date,omitempty"`
}

type LineCategory string

const (
	CategoryService    LineCategory = "service"
	CategoryProcedure  LineCategory = "procedure"
	CategoryDrug       LineCategory = "drug"
	CategoryConsumable LineCategory = "consumable"
)

type Line struct {
	ID          string       `json:"id"`
	Description string       `json:"description"`
	Category    LineCategory `json:"category,omitempty"`
	// Code is the line's code as sent: the organisation's own, or a standard
	// item ({"system": "payahub", "value": "MED-0001"}).
	Code *Code `json:"code,omitempty"`
	// Item is the standard item the line resolved to.
	Item        string       `json:"item,omitempty"`
	Mapping     *CodeMapping `json:"mapping,omitempty"`
	Quantity    int64        `json:"quantity"`
	UnitPrice   int64        `json:"unit_price"` // minor units
	ServiceDate *Date        `json:"service_date,omitempty"`
}

func (l Line) Total() int64 { return l.UnitPrice * l.Quantity }

type DiagnosisRole string

const (
	Primary   DiagnosisRole = "primary"
	Secondary DiagnosisRole = "secondary"
)

type Diagnosis struct {
	Code    Code          `json:"code"`
	Role    DiagnosisRole `json:"role,omitempty"`
	Mapping *CodeMapping  `json:"mapping,omitempty"`
}

type Provenance struct {
	Source        Source    `json:"source"`
	ReceivedAt    time.Time `json:"received_at"`
	PayloadSHA256 string    `json:"payload_sha256,omitempty"`
	ArtifactURI   string    `json:"artifact_uri,omitempty"`
}

// Input is what an integrator sends. Everything else on a Claim is set by Payahub.
type Input struct {
	PayerID           string      `json:"payer_id"`
	Currency          string      `json:"currency"`
	Member            Member      `json:"member"`
	Provider          Provider    `json:"provider"`
	Encounter         Encounter   `json:"encounter"`
	Lines             []Line      `json:"lines"`
	Diagnoses         []Diagnosis `json:"diagnoses"`
	AuthorizationCode string      `json:"authorization_code,omitempty"`
	// OTP is the code the member received, with the challenge the payer
	// returned when it was sent (some payers need both on the claim).
	OTP *OTP `json:"otp,omitempty"`
}

type OTP struct {
	Code      string `json:"code"`
	Challenge string `json:"challenge,omitempty"`
}

type Claim struct {
	ID               string     `json:"id"`
	OrganizationID   string     `json:"organization_id"`
	Environment      string     `json:"environment"`
	State            State      `json:"state"`
	PayerClaimNumber string     `json:"payer_claim_number,omitempty"`
	Total            int64      `json:"total"`
	Provenance       Provenance `json:"provenance"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Input
}

// ComputeTotal refreshes the cached total from the lines.
func (c *Claim) ComputeTotal() {
	var t int64
	for _, l := range c.Lines {
		t += l.Total()
	}
	c.Total = t
}

// FieldError is a structural problem with an Input, reported against its JSON path.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Normalize fills defaults: currency, encounter type, line ids, categories, quantities.
func (in *Input) Normalize(newID func() string) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "GHS"
	}
	if in.Encounter.Type == "" {
		in.Encounter.Type = Outpatient
	}
	for i := range in.Lines {
		l := &in.Lines[i]
		if l.ID == "" {
			l.ID = newID()
		}
		if l.Category == "" {
			l.Category = CategoryService
		}
		if l.Quantity == 0 {
			l.Quantity = 1
		}
	}
	for i := range in.Diagnoses {
		if in.Diagnoses[i].Role == "" {
			in.Diagnoses[i].Role = Secondary
		}
	}
}

// Check reports structural errors. Business rules (mapping, payer edits) live in the engine.
func (in *Input) Check() []FieldError {
	var errs []FieldError
	add := func(field, msg string) { errs = append(errs, FieldError{Field: field, Message: msg}) }

	if strings.TrimSpace(in.PayerID) == "" {
		add("payer_id", "is required")
	}
	if !currencyRe.MatchString(in.Currency) {
		add("currency", "must be a three-letter ISO 4217 code")
	}
	if strings.TrimSpace(in.Member.MemberNumber) == "" {
		add("member.member_number", "is required")
	}
	if strings.TrimSpace(in.Provider.Name) == "" {
		add("provider.name", "is required")
	}
	if in.Encounter.StartDate.IsZero() {
		add("encounter.start_date", "is required")
	}
	if in.Encounter.Type != Outpatient && in.Encounter.Type != Inpatient {
		add("encounter.type", "must be outpatient or inpatient")
	}
	seen := make(map[string]bool, len(in.Lines))
	for i, l := range in.Lines {
		p := fmt.Sprintf("lines[%d]", i)
		if strings.TrimSpace(l.Description) == "" {
			add(p+".description", "is required")
		}
		if l.Quantity < 1 {
			add(p+".quantity", "must be at least 1")
		}
		if l.UnitPrice < 0 {
			add(p+".unit_price", "must not be negative")
		}
		if seen[l.ID] {
			add(p+".id", "duplicates another line")
		}
		seen[l.ID] = true
	}
	for i, d := range in.Diagnoses {
		if strings.TrimSpace(d.Code.Value) == "" || strings.TrimSpace(d.Code.System) == "" {
			add(fmt.Sprintf("diagnoses[%d].code", i), "needs a system and a value")
		}
	}
	return errs
}
