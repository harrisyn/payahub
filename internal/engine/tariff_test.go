package engine

import (
	"slices"
	"testing"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
)

func ptr[T any](v T) *T { return &v }

func tariffOf(entries ...catalog.ListEntry) Tariff {
	by := map[string]catalog.ListEntry{}
	for _, e := range entries {
		by[e.Code] = e
	}
	return Tariff{Lookup: func(code string) (catalog.ListEntry, bool) { e, ok := by[code]; return e, ok }}
}

func line(desc, code string, qty, price int64) claim.Line {
	return claim.Line{Description: desc, Quantity: qty, UnitPrice: price,
		Mapping: &claim.CodeMapping{Target: &claim.Code{System: "payer:x", Value: code}}}
}

func issueList(is []Issue) []string {
	var out []string
	for _, i := range is {
		out = append(out, string(i.Severity)+":"+i.Code+"@"+i.Field)
	}
	return out
}

func TestTariffRules(t *testing.T) {
	list := tariffOf(
		catalog.ListEntry{Code: "C01", Name: "Consultation", Price: ptr[int64](15000)},
		catalog.ListEntry{Code: "ANC", Name: "Antenatal visit", Sex: "female", MinAge: ptr(12), MaxAge: ptr(55)},
		catalog.ListEntry{Code: "AMX", Name: "Amoxicillin 500 mg", Price: ptr[int64](300), MaxQuantity: ptr[int64](21)},
		catalog.ListEntry{Code: "MRI", Name: "MRI brain", NeedsAuthorization: true},
	)
	c := goodClaim()
	c.Member.Gender = "M"
	c.Member.BirthDate = ptr(claim.NewDate(1990, 11, 2)) // 35 at the visit on 2026-10-20
	c.Lines = []claim.Line{
		line("Consultation", "C01", 1, 18000),
		line("Antenatal visit", "ANC", 1, 5000),
		line("Amoxicillin", "AMX", 15, 300),
		line("Amoxicillin, second course", "AMX", 15, 300),
		line("MRI brain", "MRI", 1, 90000),
		line("Physio", "PHY9", 1, 4000),
	}
	got := issueList(tariff(c, list))
	want := []string{
		"warning:price_above_tariff@lines[0].unit_price",
		"error:line_not_for_sex@lines[1].mapping",
		"error:authorization_required@authorization_code",
		"warning:not_in_payer_list@lines[5].mapping",
		"error:quantity_over_limit@lines[2].quantity",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("issues:\n got %v\nwant %v", got, want)
	}

	// The payer's policy makes a price or an unlisted code stop the claim.
	list.Rules = catalog.PayerRules{PricePolicy: "block", UnlistedPolicy: "block"}
	got = issueList(tariff(c, list))
	if got[0] != "error:price_above_tariff@lines[0].unit_price" || got[3] != "error:not_in_payer_list@lines[5].mapping" {
		t.Fatalf("block policy: %v", got)
	}

	// Within every rule, nothing is said; a woman of 35 may have the visit.
	c.Member.Gender = "female"
	c.AuthorizationCode = "PA-778"
	c.Lines = []claim.Line{line("Consultation", "C01", 1, 15000), line("Antenatal visit", "ANC", 1, 5000),
		line("Amoxicillin", "AMX", 21, 300), line("MRI brain", "MRI", 1, 90000)}
	if got := tariff(c, list); len(got) != 0 {
		t.Fatalf("clean claim: %v", issueList(got))
	}

	// Too old for the code, and unknown sex or age only warns, once.
	c.Member.BirthDate = ptr(claim.NewDate(1960, 1, 1))
	if got := issueList(tariff(c, list)); !slices.Equal(got, []string{"error:line_not_for_age@lines[1].mapping"}) {
		t.Fatalf("age: %v", got)
	}
	c.Member.Gender, c.Member.BirthDate = "", nil
	c.Lines = append(c.Lines, line("Antenatal visit, again", "ANC", 1, 5000))
	if got := issueList(tariff(c, list)); !slices.Equal(got, []string{"warning:sex_unknown@member.gender", "warning:age_unknown@member.birth_date"}) {
		t.Fatalf("unknown patient: %v", got)
	}

	// No list loaded: nothing is checked.
	if got := tariff(c, Tariff{}); got != nil {
		t.Fatalf("no list: %v", got)
	}
}
