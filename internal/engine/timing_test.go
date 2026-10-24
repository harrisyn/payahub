package engine

import (
	"testing"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
)

func codes(v Validation) map[string]Severity {
	out := map[string]Severity{}
	for _, i := range v.Issues {
		out[i.Code] = i.Severity
	}
	return out
}

func TestFilingDeadline(t *testing.T) {
	m := manifest(incremental, "realtime", "early")
	c := goodClaim()
	visit := c.Encounter.StartDate.Time
	at := func(days int) Facts { return Facts{Today: visit.AddDate(0, 0, days)} }

	if got := codes(Validate(c, m, nil, at(10))); len(got) != 0 {
		t.Errorf("day 10: %v", got)
	}
	if got := codes(Validate(c, m, nil, at(55))); got["filing_deadline_near"] != SeverityWarning {
		t.Errorf("day 55: %v", got)
	}
	v := Validate(c, m, nil, at(61))
	if codes(v)["filing_deadline_passed"] != SeverityWarning || v.State != claim.Ready {
		t.Errorf("day 61 warns by default: %v %s", codes(v), v.State)
	}
	v = Validate(c, m, []byte(`{"late_filing_policy":"block"}`), at(61))
	if codes(v)["filing_deadline_passed"] != SeverityError || v.State != claim.Draft {
		t.Errorf("day 61 blocks when the payer says so: %v", codes(v))
	}
	if got := codes(Validate(c, m, []byte(`{"filing_days":90}`), at(61))); len(got) != 0 {
		t.Errorf("a 90-day payer at day 61: %v", got)
	}
	// An admission counts from discharge.
	end := claim.Date{Time: visit.AddDate(0, 0, 20)}
	c.Encounter.EndDate = &end
	if got := codes(Validate(c, m, nil, at(70))); len(got) != 0 {
		t.Errorf("discharged 50 days ago: %v", got)
	}
	if got := codes(Validate(c, m, nil, at(-3))); got["visit_in_future"] != SeverityError {
		t.Errorf("future visit: %v", got)
	}
}

func TestDuplicates(t *testing.T) {
	m := manifest(incremental, "realtime", "early")
	c := goodClaim()
	today := Facts{Today: c.Encounter.StartDate.Time.AddDate(0, 0, 1)}

	today.Duplicates = []claim.Duplicate{{ID: "clm_other", State: claim.Acknowledged}}
	v := Validate(c, m, nil, today)
	if codes(v)["possible_duplicate"] != SeverityWarning || v.State != claim.Ready {
		t.Errorf("different lines: %v %s", codes(v), v.State)
	}
	today.Duplicates[0].Exact = true
	v = Validate(c, m, nil, today)
	if codes(v)["duplicate_claim"] != SeverityReview || v.State != claim.NeedsReview {
		t.Errorf("same lines: %v %s", codes(v), v.State)
	}
	c.Reviews = append(c.Reviews, claim.Review{Code: "duplicate_claim", Field: "claim:clm_other", By: "ama@clinic"})
	if v = Validate(c, m, nil, today); v.State != claim.Ready {
		t.Errorf("after a person confirmed it: %v %s", codes(v), v.State)
	}
}

func TestLinesKey(t *testing.T) {
	a := []claim.Line{{Item: "SRV-0001", Quantity: 1, UnitPrice: 15000}, {Description: "Malaria  RDT", UnitPrice: 3500}}
	b := []claim.Line{{Description: "malaria rdt", Quantity: 1, UnitPrice: 3500}, {Item: "SRV-0001", Quantity: 1, UnitPrice: 15000}}
	if claim.LinesKey(a) != claim.LinesKey(b) {
		t.Error("the same lines in another order or spelling differ")
	}
	b[0].Quantity = 2
	if claim.LinesKey(a) == claim.LinesKey(b) {
		t.Error("a different quantity is the same claim")
	}
	_ = time.Now
}
