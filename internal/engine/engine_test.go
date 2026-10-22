package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/sandbox"
)

var incremental = []connector.Verb{connector.OpenClaim, connector.AddLine, connector.UpdateLine, connector.FinalizeClaim}

func manifest(verbs []connector.Verb, mode, timing string) connector.Manifest {
	cfg := connector.DefaultSubmissionConfig()
	cfg.LineSubmissionMode, cfg.ClaimCreationTiming = mode, timing
	return connector.Manifest{Key: "t", Verbs: verbs, Submission: cfg}
}

func TestChoosePlan(t *testing.T) {
	whole := []connector.Verb{connector.SubmitWholeClaim}
	both := append([]connector.Verb{connector.SubmitWholeClaim}, incremental...)
	cases := []struct {
		name         string
		m            connector.Manifest
		mode, timing string
	}{
		{"incremental payer keeps realtime early", manifest(incremental, "realtime", "early"), "realtime", "early"},
		{"whole-claim-only payer clamps to bulk deferred", manifest(whole, "realtime", "early"), "bulk", "deferred"},
		{"deferred without whole-claim falls back to early", manifest(incremental, "bulk", "deferred"), "bulk", "early"},
		{"deferred with realtime is clamped", manifest(both, "realtime", "deferred"), "bulk", "deferred"},
	}
	for _, c := range cases {
		p := ChoosePlan(c.m, c.m.Submission)
		if p.LineMode != c.mode || p.Timing != c.timing {
			t.Errorf("%s: got %+v", c.name, p)
		}
	}
}

func TestResolveConfigOverrides(t *testing.T) {
	m := manifest(incremental, "realtime", "early")
	cfg := ResolveConfig(m, []byte(`{"unmapped_line_policy":"send_raw","nonsense":1}`))
	if cfg.UnmappedLinePolicy != "send_raw" || cfg.LineSubmissionMode != "realtime" {
		t.Errorf("got %+v", cfg)
	}
}

func goodClaim() *claim.Claim {
	target := &claim.Code{System: "payer:sandbox", Value: "C01"}
	c := &claim.Claim{
		ID: fmt.Sprintf("clm_test_%d", nextID()), State: claim.Draft, Environment: "sandbox",
		Input: claim.Input{
			PayerID: "sandbox", Currency: "GHS",
			Member:    claim.Member{MemberNumber: "M-1001"},
			Provider:  claim.Provider{Name: "Ridge Clinic"},
			Encounter: claim.Encounter{Type: claim.Outpatient, StartDate: claim.NewDate(2026, 10, 20)},
			Lines: []claim.Line{{ID: "ln1", Description: "Consultation", Quantity: 1, UnitPrice: 15000,
				Mapping: &claim.CodeMapping{Source: claim.Code{System: "local", Value: "CONS"}, Target: target}}},
			Diagnoses: []claim.Diagnosis{{Code: claim.Code{System: "icd-10", Value: "B54"}, Role: claim.Primary}},
		},
	}
	return c
}

var counter int

func nextID() int { counter++; return counter }

func TestValidateStates(t *testing.T) {
	m, _, _ := connector.Lookup(sandbox.Key)
	if v := Validate(goodClaim(), m, nil); v.State != claim.Ready || len(v.Issues) != 0 {
		t.Fatalf("clean claim: %+v", v)
	}

	c := goodClaim()
	c.Lines[0].Mapping = nil
	if v := Validate(c, m, nil); v.State != claim.Draft || v.Issues[0].Code != "line_unmapped" {
		t.Errorf("unmapped line should block: %+v", v)
	}
	if v := Validate(c, m, []byte(`{"unmapped_line_policy":"send_raw"}`)); v.State != claim.Ready {
		t.Errorf("send_raw override should allow it: %+v", v)
	}

	low := 0.7
	c = goodClaim()
	c.Lines[0].Mapping.Evidence = &claim.Evidence{Origin: claim.OriginAIMapped, Confidence: &low}
	if v := Validate(c, m, nil); v.State != claim.NeedsReview {
		t.Errorf("low-confidence AI mapping should need review: %+v", v)
	}
	c.Lines[0].Mapping.Evidence.ConfirmedBy = "usr_1"
	if v := Validate(c, m, nil); v.State != claim.Ready {
		t.Errorf("a confirmed mapping should pass: %+v", v)
	}
}

func TestSendBothPaths(t *testing.T) {
	conn, _ := sandbox.New(connector.Account{})
	for _, plan := range []Plan{{"realtime", "early"}, {"bulk", "deferred"}} {
		c := goodClaim()
		r, err := Send(context.Background(), conn, c, plan)
		if err != nil || !r.Acknowledged || r.LineReferences["ln1"] == "" {
			t.Errorf("plan %+v: receipt %+v err %v", plan, r, err)
		}
	}
}

func TestDecide(t *testing.T) {
	if o := Decide(connector.Receipt{Acknowledged: true}, nil); o.State != claim.Acknowledged || !o.Final {
		t.Errorf("ack: %+v", o)
	}
	if o := Decide(connector.Receipt{}, connector.Errorf(connector.PayerRefused, "no")); o.State != claim.Rejected || !o.Final {
		t.Errorf("refused: %+v", o)
	}
	if o := Decide(connector.Receipt{}, connector.Errorf(connector.Timeout, "slow")); o.State != claim.Submitted || o.Final {
		t.Errorf("timeout should retry: %+v", o)
	}
	if o := Decide(connector.Receipt{}, connector.Errorf(connector.InvalidCredentials, "bad")); o.State != claim.Ready || !o.Final {
		t.Errorf("credentials should return to ready: %+v", o)
	}
	if o := Decide(connector.Receipt{}, errors.New("boom")); o.Final {
		t.Errorf("unknown errors should retry: %+v", o)
	}
}

func BenchmarkValidate(b *testing.B) {
	m, _, _ := connector.Lookup(sandbox.Key)
	c := goodClaim()
	for i := 0; i < 19; i++ {
		c.Lines = append(c.Lines, c.Lines[0])
	}
	b.ReportAllocs()
	for b.Loop() {
		Validate(c, m, nil)
	}
}
