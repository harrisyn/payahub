package engine

import (
	"context"
	"encoding/json"
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
	if v := Validate(goodClaim(), m, nil, Facts{}); v.State != claim.Ready || len(v.Issues) != 0 {
		t.Fatalf("clean claim: %+v", v)
	}

	c := goodClaim()
	c.Lines[0].Mapping = nil
	if v := Validate(c, m, nil, Facts{}); v.State != claim.Draft || v.Issues[0].Code != "line_unmapped" {
		t.Errorf("unmapped line should block: %+v", v)
	}
	if v := Validate(c, m, []byte(`{"unmapped_line_policy":"send_raw"}`), Facts{}); v.State != claim.Ready {
		t.Errorf("send_raw override should allow it: %+v", v)
	}

	low := 0.7
	c = goodClaim()
	c.Lines[0].Mapping.Evidence = &claim.Evidence{Origin: claim.OriginAIMapped, Confidence: &low}
	if v := Validate(c, m, nil, Facts{}); v.State != claim.NeedsReview {
		t.Errorf("low-confidence AI mapping should need review: %+v", v)
	}
	c.Lines[0].Mapping.Evidence.ConfirmedBy = "usr_1"
	if v := Validate(c, m, nil, Facts{}); v.State != claim.Ready {
		t.Errorf("a confirmed mapping should pass: %+v", v)
	}
}

func noJournal(context.Context, *Progress) error { return nil }

func TestSendBothPaths(t *testing.T) {
	conn, _ := sandbox.New(connector.Account{})
	cfg := connector.DefaultSubmissionConfig()
	for _, plan := range []Plan{{"realtime", "early"}, {"bulk", "deferred"}} {
		c := goodClaim()
		var p Progress
		r, err := Send(context.Background(), conn, Build(c, cfg), plan, &p, noJournal)
		if err != nil || !r.Acknowledged || r.PayerClaimNumber == "" {
			t.Errorf("plan %+v: receipt %+v err %v", plan, r, err)
		}
	}
}

func TestBuildGroupsByPayerCode(t *testing.T) {
	c := goodClaim()
	c.Lines = append(c.Lines,
		claim.Line{ID: "ln2", Description: "Consultation again", Quantity: 2, UnitPrice: 10000,
			Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "C01"}}},
		claim.Line{ID: "ln3", Description: "Malaria test", Quantity: 1, UnitPrice: 5000,
			Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "L07"}}},
	)
	c.Diagnoses = append([]claim.Diagnosis{{Code: claim.Code{System: "icd-10", Value: "R50"}, Role: claim.Secondary}}, c.Diagnoses...)
	c.Diagnoses = append(c.Diagnoses, claim.Diagnosis{Code: claim.Code{System: "icd-10", Value: "B54"}, Role: claim.Secondary})

	cfg := connector.DefaultSubmissionConfig()
	s := Build(c, cfg)
	if len(s.Lines) != 2 {
		t.Fatalf("outpatient lines should group by payer code: %+v", s.Lines)
	}
	g := s.Lines[0]
	if g.Key != "C01" || g.Quantity != 3 || g.Amount != 35000 || len(g.LineIDs) != 2 {
		t.Errorf("grouped line: %+v", g)
	}
	if got := g.UnitPrice("GHS"); got < 116.66 || got > 116.67 {
		t.Errorf("unit price should keep quantity x price = amount, got %v", got)
	}
	if len(s.Diagnoses) != 2 || s.Diagnoses[0].ICD != "B54" || !s.Diagnoses[0].Primary {
		t.Errorf("primary first, each code once: %+v", s.Diagnoses)
	}

	c.Encounter.Type = claim.Inpatient
	if s := Build(c, cfg); len(s.Lines) != 3 {
		t.Errorf("inpatient lines stay individual: %+v", s.Lines)
	}
	// An input change must change the output (claims plan, the regression assertion).
	c.Lines[2].Quantity = 4
	if a, b := Build(c, cfg), Build(goodClaim(), cfg); len(a.Lines) == len(b.Lines) && a.Lines[0].Amount == b.Lines[0].Amount {
		t.Errorf("different claims built the same submission")
	}
}

// lossy wraps a connector so the first AddLine reaches the payer but its
// reply is lost, like a timeout after the payer committed.
type lossy struct {
	connector.Connector
	lost bool
}

func (l *lossy) AddLine(ctx context.Context, s *connector.Submission, n string, line connector.SendLine) (string, error) {
	ref, err := l.Connector.AddLine(ctx, s, n, line)
	if err == nil && !l.lost {
		l.lost = true
		return "", connector.Errorf(connector.Timeout, "reply lost")
	}
	return ref, err
}

func TestResumeAdoptsALostLineInsteadOfAddingItTwice(t *testing.T) {
	base, _ := sandbox.New(connector.Account{})
	conn := &lossy{Connector: base}
	c := goodClaim()
	c.Lines = append(c.Lines, claim.Line{ID: "ln2", Description: "Malaria test", Quantity: 1, UnitPrice: 5000,
		Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "L07"}}})
	s := Build(c, connector.DefaultSubmissionConfig())
	plan := Plan{"realtime", "early"}

	var saved []byte
	journal := func(_ context.Context, p *Progress) error {
		b, err := json.Marshal(p)
		saved = b
		return err
	}
	var p Progress
	if _, err := Send(context.Background(), conn, s, plan, &p, journal); !isCode(err, connector.Timeout) {
		t.Fatalf("first run should time out, got %v", err)
	}
	// A new worker starts from what was written down, not from memory.
	var resumed Progress
	if err := json.Unmarshal(saved, &resumed); err != nil {
		t.Fatal(err)
	}
	r, err := Send(context.Background(), conn, s, plan, &resumed, journal)
	if err != nil || !r.Acknowledged {
		t.Fatalf("resume: %+v %v", r, err)
	}
	if lines := sandbox.PayerLines(r.PayerClaimNumber); len(lines) != 2 {
		t.Errorf("payer should hold exactly 2 lines, has %+v", lines)
	}
}

func TestCorrectedClaimIsSyncedNotReopened(t *testing.T) {
	conn, _ := sandbox.New(connector.Account{})
	cfg := connector.DefaultSubmissionConfig()
	cfg.ItemGroupingOutpatient = "individual"
	c := goodClaim()
	c.Lines = append(c.Lines,
		claim.Line{ID: "ln2", Description: "Malaria test", Quantity: 1, UnitPrice: 5000,
			Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "L07"}}},
		claim.Line{ID: "ln3", Description: "Paracetamol", Quantity: 10, UnitPrice: 50,
			Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "M01"}}},
	)
	// The first send reaches the payer, which refuses it at finalize.
	var p Progress
	if _, err := Send(context.Background(), &stopBeforeFinal{conn}, Build(c, cfg), Plan{"realtime", "early"}, &p, noJournal); err == nil {
		t.Fatal("expected the refusal")
	}
	// The biller corrects the claim: drops the test, changes the quantity.
	c.Lines = []claim.Line{c.Lines[0], c.Lines[2]}
	c.Lines[1].Quantity = 20
	r, err := Send(context.Background(), conn, Build(c, cfg), Plan{"realtime", "early"}, &p, noJournal)
	if err != nil || r.PayerClaimNumber != p.PayerClaimNumber {
		t.Fatalf("resend: %+v %v", r, err)
	}
	lines := sandbox.PayerLines(r.PayerClaimNumber)
	if len(lines) != 2 || lines[1].Quantity != 20 || lines[1].Amount != 1000 {
		t.Errorf("payer copy should match the corrected claim: %+v", lines)
	}
}

type stopBeforeFinal struct{ connector.Connector }

func (stopBeforeFinal) FinalizeClaim(context.Context, *connector.Submission, string) (connector.Receipt, error) {
	return connector.Receipt{}, connector.Errorf(connector.PayerRefused, "benefit exhausted")
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
		Validate(c, m, nil, Facts{})
	}
}
