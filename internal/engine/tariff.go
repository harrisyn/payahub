package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
)

// Tariff is the payer's own list and its rules, as the checks need them.
// Lookup is nil (and nothing is checked) when the payer has no list loaded.
type Tariff struct {
	Lookup func(code string) (catalog.ListEntry, bool)
	Rules  catalog.PayerRules
}

// TariffFor reads the payer's list from the catalogue.
func TariffFor(payerID string) Tariff {
	p, _ := catalog.LookupPayer(payerID)
	t := Tariff{Rules: p.Rules}
	if catalog.HasList(payerID) {
		t.Lookup = func(code string) (catalog.ListEntry, bool) { return catalog.ListEntryFor(payerID, code) }
	}
	return t
}

func (t Tariff) severity(policy string) Severity {
	if policy == "block" {
		return SeverityError
	}
	return SeverityWarning
}

// tariff checks each line's payer code against the payer's own list: that it
// is listed, priced at most at the tariff, within the payer's quantity, and
// allowed for this patient's sex and age, with authorisation when the payer
// asks for it. What the payer states for a code stops the claim; a price or
// an unlisted code stops it only when the payer's policy says block.
func tariff(c *claim.Claim, t Tariff) []Issue {
	if t.Lookup == nil {
		return nil
	}
	var issues []Issue
	add := func(code string, sev Severity, field, msg string) {
		issues = append(issues, Issue{Code: code, Severity: sev, Field: field, Message: msg})
	}
	sex := patientSex(c.Member.Gender)
	age := -1
	if b := c.Member.BirthDate; b != nil && !c.Encounter.StartDate.IsZero() {
		age = ageOn(b.Time, c.Encounter.StartDate.Time)
	}
	quantity := map[string]int64{}
	first := map[string]int{}
	var order []string
	saidSex, saidAge := false, false
	for i, l := range c.Lines {
		if !l.Mapping.Mapped() {
			continue
		}
		code := l.Mapping.Target.Value
		e, ok := t.Lookup(code)
		if !ok {
			add("not_in_payer_list", t.severity(t.Rules.UnlistedPolicy), fmt.Sprintf("lines[%d].mapping", i),
				fmt.Sprintf("%q is sent as %s, which is not in this insurer's list; it may refuse the line.", l.Description, code))
			continue
		}
		if _, seen := first[code]; !seen {
			first[code] = i
			order = append(order, code)
		}
		quantity[code] += max(l.Quantity, 1)
		if e.Price != nil && l.UnitPrice > *e.Price {
			add("price_above_tariff", t.severity(t.Rules.PricePolicy), fmt.Sprintf("lines[%d].unit_price", i),
				fmt.Sprintf("%q is priced at %s; this insurer pays %s for %s.", l.Description,
					amount(l.UnitPrice, c.Currency), amount(*e.Price, c.Currency), code))
		}
		if e.Sex != "" {
			switch {
			case sex == "":
				if !saidSex {
					add("sex_unknown", SeverityWarning, "member.gender",
						fmt.Sprintf("This insurer allows %q only for %s patients, and the claim does not give the patient's sex.", l.Description, e.Sex))
					saidSex = true
				}
			case sex != e.Sex:
				add("line_not_for_sex", SeverityError, fmt.Sprintf("lines[%d].mapping", i),
					fmt.Sprintf("This insurer allows %q only for %s patients.", l.Description, e.Sex))
			}
		}
		if e.MinAge != nil || e.MaxAge != nil {
			switch {
			case age < 0:
				if !saidAge {
					add("age_unknown", SeverityWarning, "member.birth_date",
						fmt.Sprintf("This insurer allows %q only %s, and the claim has no birth date.", l.Description, ageRange(e)))
					saidAge = true
				}
			case (e.MinAge != nil && age < *e.MinAge) || (e.MaxAge != nil && age > *e.MaxAge):
				add("line_not_for_age", SeverityError, fmt.Sprintf("lines[%d].mapping", i),
					fmt.Sprintf("This insurer allows %q only %s; the patient was %d at the visit.", l.Description, ageRange(e), age))
			}
		}
		if e.NeedsAuthorization && strings.TrimSpace(c.AuthorizationCode) == "" {
			add("authorization_required", SeverityError, "authorization_code",
				fmt.Sprintf("This insurer needs prior authorisation for %q. Add the authorisation code it gave.", l.Description))
		}
	}
	for _, code := range order {
		e, _ := t.Lookup(code)
		if e.MaxQuantity != nil && quantity[code] > *e.MaxQuantity {
			i := first[code]
			add("quantity_over_limit", SeverityError, fmt.Sprintf("lines[%d].quantity", i),
				fmt.Sprintf("%q: %d on this claim; this insurer pays for at most %d.", c.Lines[i].Description, quantity[code], *e.MaxQuantity))
		}
	}
	return issues
}

// patientSex reads the claim's gender as the lists say it: female, male or
// unknown ("").
func patientSex(g string) string {
	switch strings.ToLower(strings.TrimSpace(g)) {
	case "f", "female", "woman":
		return "female"
	case "m", "male", "man":
		return "male"
	}
	return ""
}

// ageOn is the age in whole years on a day.
func ageOn(birth, on time.Time) int {
	y := on.Year() - birth.Year()
	if on.Month() < birth.Month() || (on.Month() == birth.Month() && on.Day() < birth.Day()) {
		y--
	}
	return max(y, 0)
}

func ageRange(e catalog.ListEntry) string {
	switch {
	case e.MinAge != nil && e.MaxAge != nil:
		return fmt.Sprintf("from %d to %d years old", *e.MinAge, *e.MaxAge)
	case e.MinAge != nil:
		return fmt.Sprintf("from %d years old", *e.MinAge)
	default:
		return fmt.Sprintf("up to %d years old", *e.MaxAge)
	}
}

// amount writes minor units as the currency and major units: GHS 150.00.
func amount(minor int64, currency string) string {
	return fmt.Sprintf("%s %d.%02d", currency, minor/100, minor%100)
}
