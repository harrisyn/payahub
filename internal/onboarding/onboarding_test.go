package onboarding

import "testing"

func TestRecommend(t *testing.T) {
	cases := []struct {
		name string
		p    Profile
		want Method
	}{
		{"emr", Profile{BillingSystem: "emr", ClaimsLeaveBy: "portal", MonthlyClaims: 900}, MethodREST},
		{"developers beat spreadsheets", Profile{BillingSystem: "spreadsheets", ClaimsLeaveBy: "email", HasDevelopers: true}, MethodREST},
		{"existing integration", Profile{BillingSystem: "paper", ClaimsLeaveBy: "integration"}, MethodREST},
		{"spreadsheets", Profile{BillingSystem: "spreadsheets", ClaimsLeaveBy: "email", MonthlyClaims: 400}, MethodBatch},
		{"paper, few claims", Profile{BillingSystem: "paper", ClaimsLeaveBy: "paper", MonthlyClaims: 40}, MethodWebForm},
		{"paper, many claims", Profile{BillingSystem: "paper", ClaimsLeaveBy: "portal", MonthlyClaims: 600}, MethodDocuments},
	}
	for _, c := range cases {
		r := Recommend(c.p)
		if r.Method != c.want {
			t.Errorf("%s: got %s, want %s", c.name, r.Method, c.want)
		}
		if r.Reason == "" {
			t.Errorf("%s: no reason", c.name)
		}
		if r.Available != (c.want == MethodREST) || (!r.Available && r.Fallback != MethodREST) {
			t.Errorf("%s: availability %v fallback %q", c.name, r.Available, r.Fallback)
		}
	}
}

func TestProfileCheck(t *testing.T) {
	if errs := (Profile{BillingSystem: "emr", ClaimsLeaveBy: "portal"}).Check(); len(errs) != 0 {
		t.Fatalf("valid profile rejected: %v", errs)
	}
	errs := (Profile{BillingSystem: "fax", ClaimsLeaveBy: "pigeon", MonthlyClaims: -1, Method: "telex"}).Check()
	if len(errs) != 4 {
		t.Fatalf("want 4 problems, got %v", errs)
	}
}
