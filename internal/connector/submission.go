package connector

import (
	"math"

	"github.com/harrisyn/payahub/internal/claim"
)

// Submission is a claim as a payer receives it. engine.Build makes it from a
// validated claim, deterministically: lines grouped and coded by the
// submission rules, diagnoses with the payer's codes. Connectors read only
// this and the claim's member, provider and encounter; nothing in it came
// from a model unconfirmed.
type Submission struct {
	Claim     *claim.Claim
	Lines     []SendLine
	Diagnoses []SendDiagnosis
	// Sent maps each line's Key to the payer's ref for it. engine.Send fills
	// it before FinalizeClaim, for payers that finalize line by line.
	Sent map[string]string
}

// SendLine is one line on the payer's claim. In grouped mode it carries every
// claim line with the same payer code; otherwise exactly one.
type SendLine struct {
	// Key is stable across retries and edits: the payer code when grouped,
	// otherwise "line:" + the claim line's ID.
	Key string `json:"key"`
	// Code is the payer's code, or the organisation's own code when the payer
	// accepts unmapped lines (Mapped false).
	Code        string             `json:"code"`
	Mapped      bool               `json:"mapped"`
	Description string             `json:"description"`
	// PayerName is the payer's own name for the item, when the mapping has it.
	PayerName string `json:"payer_name,omitempty"`
	Category    claim.LineCategory `json:"category"`
	Quantity    int64              `json:"quantity"`
	Amount      int64              `json:"amount"` // line total, minor units
	LineIDs     []string           `json:"line_ids"`
}

// SendDiagnosis is one diagnosis on the payer's claim.
type SendDiagnosis struct {
	ICD string `json:"icd"`
	// PayerCode is the payer's own code for it, when the payer has one.
	PayerCode string `json:"payer_code,omitempty"`
	Primary   bool   `json:"primary"`
}

// Key is the diagnosis's stable identity on the payer's claim.
func (d SendDiagnosis) Key() string { return d.ICD }

// minorDigits is the number of minor-unit digits of currencies that are not 2.
var minorDigits = map[string]int{"JPY": 0, "KRW": 0, "XOF": 0, "XAF": 0, "UGX": 0, "RWF": 0, "BIF": 0}

// Major converts minor units to the decimal amount payers' APIs take.
func Major(minor int64, currency string) float64 {
	d, ok := minorDigits[currency]
	if !ok {
		d = 2
	}
	return float64(minor) / math.Pow10(d)
}

// UnitPrice is the line's price per unit in major units. A grouped line whose
// members had different prices gets their average, so Quantity x UnitPrice
// still equals the amount claimed (core-api-v2 does the same).
func (l SendLine) UnitPrice(currency string) float64 {
	q := l.Quantity
	if q < 1 {
		q = 1
	}
	return Major(l.Amount, currency) / float64(q)
}

// PayerClaim is a claim as the payer holds it, for reconciling a retry and
// for following a claim after it is sent.
type PayerClaim struct {
	Number string      `json:"number"`
	Status PayerStatus `json:"status"`
	Lines  []PayerLine `json:"lines,omitempty"`
	// Message is the payer's own words about the claim's status, if any.
	Message string `json:"message,omitempty"`
}

type PayerLine struct {
	Ref      string `json:"ref"`
	Code     string `json:"code"`
	Quantity int64  `json:"quantity"`
	Amount   int64  `json:"amount"`
}

// PayerStatus is where a claim stands at the payer. Unknown means the payer
// does not say (its API lists lines but no status).
type PayerStatus string

const (
	PayerOpen      PayerStatus = "open"
	PayerSubmitted PayerStatus = "submitted"
	PayerApproved  PayerStatus = "approved"
	PayerQueried   PayerStatus = "queried"
	PayerRejected  PayerStatus = "rejected"
	PayerPaid      PayerStatus = "paid"
	PayerUnknown   PayerStatus = "unknown"
)

// CatalogKind names a payer code list a connector can download.
type CatalogKind string

const (
	CatalogItems     CatalogKind = "items"
	CatalogDiagnoses CatalogKind = "diagnoses"
)

// CatalogEntry is one code in a payer's list.
type CatalogEntry struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Group string `json:"group,omitempty"` // the payer's category, if it gives one
	// Price is the payer's tariff in minor units, when the list carries one.
	Price *int64 `json:"price,omitempty"`
}
