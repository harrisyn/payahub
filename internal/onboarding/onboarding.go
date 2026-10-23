// Package onboarding holds the rules that turn how an organisation works
// today into a recommended way of sending claims, plus the trial and plan
// terms. Rules, not a model: the same answers always give the same advice.
package onboarding

import (
	"fmt"
	"slices"
	"time"
)

// Trial terms. Both are open decisions in the plan; change them here.
const (
	TrialDays       = 14
	TrialLiveClaims = 50
)

type Method string

const (
	MethodREST      Method = "rest"
	MethodBatch     Method = "batch"
	MethodDocuments Method = "documents"
	MethodWebForm   Method = "web_form"
)

// Available says whether a method can be used today. REST ships first;
// the others are in build and the console says so.
func (m Method) Available() bool { return m == MethodREST }

// Profile is what the organisation tells us in onboarding.
type Profile struct {
	BillingSystem string   `json:"billing_system"`  // emr | practice_software | spreadsheets | paper
	ClaimsLeaveBy string   `json:"claims_leave_by"` // portal | email | paper | integration
	MonthlyClaims int      `json:"monthly_claims"`
	HasDevelopers bool     `json:"has_developers"`
	Payers        []string `json:"payers"`
	Method        Method   `json:"method,omitempty"` // the method they chose, if not the recommended one
}

var (
	billingSystems = []string{"emr", "practice_software", "spreadsheets", "paper"}
	leaveBy        = []string{"portal", "email", "paper", "integration"}
	methods        = []Method{MethodREST, MethodBatch, MethodDocuments, MethodWebForm}
)

// Check returns the problems with a profile, keyed by field.
func (p Profile) Check() map[string]string {
	errs := map[string]string{}
	if !slices.Contains(billingSystems, p.BillingSystem) {
		errs["billing_system"] = "must be one of emr, practice_software, spreadsheets, paper"
	}
	if !slices.Contains(leaveBy, p.ClaimsLeaveBy) {
		errs["claims_leave_by"] = "must be one of portal, email, paper, integration"
	}
	if p.MonthlyClaims < 0 {
		errs["monthly_claims"] = "must be zero or more"
	}
	if p.Method != "" && !slices.Contains(methods, p.Method) {
		errs["method"] = "must be one of rest, batch, documents, web_form"
	}
	return errs
}

type Recommendation struct {
	Method    Method `json:"method"`
	Reason    string `json:"reason"`
	Available bool   `json:"available"`
	// Fallback is offered when the recommended method is still in build.
	Fallback Method `json:"fallback,omitempty"`
}

// LowVolume is the monthly claim count under which a team with no billing
// system is better served by the web form than by uploading documents.
const LowVolume = 100

// Recommend applies the onboarding rules from the plan, in order.
func Recommend(p Profile) Recommendation {
	var r Recommendation
	switch {
	case p.HasDevelopers || p.ClaimsLeaveBy == "integration" ||
		p.BillingSystem == "emr" || p.BillingSystem == "practice_software":
		r = Recommendation{Method: MethodREST, Reason: restReason(p)}
	case p.BillingSystem == "spreadsheets":
		r = Recommendation{Method: MethodBatch,
			Reason: "You keep claims in spreadsheets, so you can upload the same file you already make each month."}
	case p.MonthlyClaims < LowVolume:
		r = Recommendation{Method: MethodWebForm,
			Reason: fmt.Sprintf("About %d claims a month on paper is quickest to type once into our form, which checks each claim as you go.", p.MonthlyClaims)}
	default:
		r = Recommendation{Method: MethodDocuments,
			Reason: fmt.Sprintf("About %d paper claims a month is a lot to retype; upload scans or PDFs and we draft each claim for you to check.", p.MonthlyClaims)}
	}
	r.Available = r.Method.Available()
	if !r.Available {
		r.Fallback = MethodREST
	}
	return r
}

func restReason(p Profile) string {
	switch {
	case p.BillingSystem == "emr" || p.BillingSystem == "practice_software":
		return "Your billing system can send each claim the moment it is ready, with no retyping, through one API for every insurer."
	case p.ClaimsLeaveBy == "integration":
		return "You already send claims from software; pointing it at one API replaces a separate integration per insurer."
	default:
		return "You have developers, so one API call per claim is the fastest and most reliable route to every insurer."
	}
}

type Plan string

const (
	PlanTrial      Plan = "trial"
	PlanFlat       Plan = "flat"
	PlanFlatVolume Plan = "flat_volume"
	PlanVolume     Plan = "volume"
	PlanEnterprise Plan = "enterprise"
	// PlanEnterpriseRequested is recorded when an organisation asks for
	// enterprise terms; the trial continues until the contract is signed.
	PlanEnterpriseRequested Plan = "enterprise_requested"
)

// Choosable are the plans an organisation may pick in the console.
// Enterprise is agreed offline, so choosing it records a request.
var Choosable = []Plan{PlanFlat, PlanFlatVolume, PlanVolume, PlanEnterprise}

func TrialEnds(from time.Time) time.Time { return from.AddDate(0, 0, TrialDays) }

// BillingCurrency is the currency plans are priced and wallets charged in.
const BillingCurrency = "GHS"

// Price is what a plan charges, in minor units of BillingCurrency. The web
// app shows the same figures (apps/web/lib/pricing.ts); a test keeps them equal.
type Price struct {
	MonthlyFee int64 `json:"monthly_fee"`
	PerClaim   int64 `json:"per_claim"` // for each claim a payer acknowledges
	Included   int   `json:"included"`  // acknowledged claims the fee covers (fair use on flat)
}

// Prices covers the plans charged from the wallet. Trial is free, and
// enterprise is invoiced offline, so neither is charged.
var Prices = map[Plan]Price{
	PlanFlat:       {MonthlyFee: 180000, Included: 1500},
	PlanFlatVolume: {MonthlyFee: 60000, PerClaim: 100},
	PlanVolume:     {PerClaim: 200},
}

// WalletFunded reports whether the plan is paid from the organisation's wallet.
func (p Plan) WalletFunded() bool { _, ok := Prices[p]; return ok }
