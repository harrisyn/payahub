package medserve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/httpx"
	"github.com/harrisyn/payahub/internal/engine"
)

// fake is a Medserve POS endpoint, built from the request and reply shapes
// core-api-v2's medserve.py sends and reads.
type fake struct {
	t  *testing.T
	mu sync.Mutex

	logins    int
	staleNext bool // answer the next call as if the connid expired
	calls     []url.Values

	nextID  int
	batches map[string]*batch
	// per product code: what p.ad answers
	chargePatient map[string]bool
	approval      map[string]string // "" none, else the state p.qdapreq gives
	otpInsurer    bool
}

type batch struct {
	member, class, bauth, pauth string
	rows                        map[string]*row // by prescription id
	final                       bool
	claimed                     []map[string]any
}

type row struct {
	prod, cat, icd string
	qty            int64
	cost           string
	disp           string
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	f := &fake{t: t, batches: map[string]*batch{}, nextID: 500, chargePatient: map[string]bool{}, approval: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fake) calledOp(op string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, c := range f.calls {
		if c.Get("op") == op {
			out = append(out, c)
		}
	}
	return out
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/ghi/pos/" {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	v := r.Form
	f.calls = append(f.calls, v)
	op := v.Get("op")
	write := func(x any) { _ = json.NewEncoder(w).Encode(x) }
	ok := func(value any) {
		write(map[string]any{"error": false, "result": map[string]any{"status": 200, "value": value}})
	}
	fail := func(msg string) {
		write(map[string]any{"error": map[string]any{"msg": msg}, "result": map[string]any{"status": 400}})
	}

	if op == "login" {
		if r.Method != http.MethodPost || v.Get("user") != "pos" || v.Get("pass") != "s3cret" || v.Get("midtype") != "host" {
			fail("Invalid login")
			return
		}
		f.logins++
		ok([]any{map[string]any{"connId": fmt.Sprintf("conn-%d", f.logins)}})
		return
	}
	if v.Get("connid") != fmt.Sprintf("conn-%d", f.logins) || f.staleNext {
		f.staleNext = false
		fail("Invalid connection id, please login")
		return
	}
	want := map[string]string{"p.qbid": "GET", "qoinfo": "GET", "qbphoneusable": "GET", "qbeaddrusable": "GET",
		"p.prodsearchw": "GET", "p.qdapreq": "GET", "p.qp4d": "GET", "p.finalb": "PUT"}
	if m, ok := want[op]; ok && r.Method != m || !ok && r.Method != http.MethodPost {
		f.t.Errorf("%s sent as %s", op, r.Method)
	}

	switch op {
	case "p.qbid":
		switch id := v.Get("benid"); id {
		case "NOPE":
			ok([]any{})
		case "OLD-1":
			ok([]any{map[string]any{"benId": "88", "plan": "132/basic", "expired": "1", "status": "expired"}})
		default:
			ok([]any{map[string]any{"benId": "4411", "extbenid": id, "plan": "132/basic", "expired": "0", "status": "active",
				"surname": "Ama", "lastname": "Mensah", "dob": "1990-04-02", "sex": "F", "insurerid": "7",
				"expiryDate": "2026-12-31"}})
		}
	case "qoinfo":
		flag := "0"
		if f.otpInsurer {
			flag = "1"
		}
		write([]any{map[string]any{"name": "GHI"}, []any{map[string]any{"name": "SR.require-authcode", "value": flag}}})
	case "qbphoneusable":
		write([]any{[]any{map[string]any{"telno": "0244000000"}}})
	case "p.bauth":
		if v.Get("benid") != "4411" || v.Get("tel") != "0244000000" {
			f.t.Errorf("p.bauth %v", v)
		}
		ok(map[string]any{"code": "PA-77"})
	case "p.sba":
		f.nextID++
		code := fmt.Sprintf("B%d", f.nextID)
		f.batches[code] = &batch{member: v.Get("id"), class: v.Get("class"), bauth: v.Get("bauth"), pauth: v.Get("pauth"), rows: map[string]*row{}}
		ok([]any{map[string]any{"batchcode": code}})
	case "p.prodsearchw":
		if v.Get("pkgid") != "132" {
			f.t.Errorf("product search for package %q", v.Get("pkgid"))
		}
		ok([]any{[]any{
			map[string]any{"code": "PARA500", "name": "Paracetamol 500mg", "coverage": "op-drugs-acute/108/1"},
			map[string]any{"code": "FBC", "name": "Full blood count", "coverage": "op-lab/210/0\nop-lab-special/211/1"},
			map[string]any{"code": "COSM", "name": "Cosmetic", "coverage": "op-excl/300/0"},
		}})
	case "p.ap":
		b := f.batches[v.Get("id")]
		if b == nil || b.final {
			fail("Batch already finalised")
			return
		}
		for _, rw := range b.rows {
			if rw.prod == v.Get("prod") {
				fail("Product already added")
				return
			}
		}
		f.nextID++
		id := strconv.Itoa(f.nextID)
		b.rows[id] = &row{prod: v.Get("prod"), cat: v.Get("catid"), icd: v.Get("icdcode")}
		ok([]any{map[string]any{"id": id}})
	case "p.ad":
		var b *batch
		var rw *row
		for _, bb := range f.batches {
			if x := bb.rows[v.Get("id")]; x != nil {
				b, rw = bb, x
			}
		}
		if rw == nil {
			fail("Prescription not found")
			return
		}
		_ = b
		qty, _ := strconv.ParseInt(v.Get("qty"), 10, 64)
		if f.chargePatient[rw.prod] && qty > 0 && v.Get("cost") != rw.cost {
			// What Medserve does when the cap is hit: it applies, then says
			// the patient pays.
			rw.qty, rw.cost = qty, v.Get("cost")
			ok([]any{map[string]any{"id": "D" + v.Get("id"), "icost": "0.00", "bcost": v.Get("cost"), "charge_patient": "1", "remark": "Benefit cap reached"}})
			return
		}
		rw.qty, rw.cost, rw.disp = qty, v.Get("cost"), "D"+v.Get("id")
		reply := map[string]any{"id": rw.disp, "icost": v.Get("cost"), "charge_patient": "0", "approvals": ""}
		if f.approval[rw.prod] != "" {
			reply["approvals"] = "cap-rule-3"
		}
		ok([]any{reply})
	case "p.adapreq":
		ok([]any{map[string]any{"id": v.Get("id"), "status": "appr_state_pending"}})
	case "p.qdapreq":
		state := "pending"
		for _, bb := range f.batches {
			for _, rw := range bb.rows {
				if rw.disp == v.Get("id") {
					state = f.approval[rw.prod]
				}
			}
		}
		ok([]any{map[string]any{"status": "appr_state_" + state}})
	case "p.bulkfind":
		var entries []map[string]any
		if err := json.Unmarshal([]byte(v.Get("data")), &entries); err != nil {
			f.t.Errorf("bulkfind data %q", v.Get("data"))
		}
		for _, bb := range f.batches {
			for _, e := range entries {
				for _, rw := range bb.rows {
					if rw.disp == e["id"] {
						if bb.final {
							fail("Dispensation already finalised")
							return
						}
						bb.claimed = append(bb.claimed, e)
					}
				}
			}
		}
		ok([]any{})
	case "p.finalb":
		b := f.batches[v.Get("id")]
		if b.final {
			fail("Batch already finalised")
			return
		}
		b.final = true
		ok([]any{})
	case "p.qp4d":
		b := f.batches[v.Get("id")]
		var rows []any
		for id, rw := range b.rows {
			rows = append(rows, map[string]any{"presId": id, "productCode": rw.prod, "qty": rw.qty, "cost": rw.cost})
		}
		ok([]any{rows})
	default:
		fail("unknown op " + op)
	}
}

func account(srv *httptest.Server) connector.Account {
	return connector.Account{
		Credentials: map[string]string{"base_url": srv.URL, "username": "pos", "password": "s3cret"},
		PayerID:     "ghi",
		PayerFields: map[string]string{"insurer_code": "ghi", "insurer_id": "7"},
	}
}

func open(t *testing.T, srv *httptest.Server) *Medserve {
	t.Helper()
	httpx.SharedTokens.Drop(httpx.Key(Key, srv.URL+"/ghi/pos/", "pos", "s3cret"))
	c, err := connector.New(Key, account(srv))
	if err != nil {
		t.Fatal(err)
	}
	return c.(*Medserve).WithClient(srv.Client())
}

func sampleClaim() *claim.Claim {
	payer := func(code, name string) *claim.CodeMapping {
		return &claim.CodeMapping{Target: &claim.Code{System: "payer:ghi", Value: code, Display: name}}
	}
	return &claim.Claim{ID: "clm_1", Input: claim.Input{
		PayerID: "ghi", Currency: "GHS",
		Member:    claim.Member{MemberNumber: "GHI-0001"},
		Provider:  claim.Provider{Name: "Ridge Clinic"},
		Encounter: claim.Encounter{Type: claim.Outpatient, StartDate: claim.NewDate(2026, 10, 20)},
		Lines: []claim.Line{
			{ID: "l1", Description: "Paracetamol tabs", Quantity: 10, UnitPrice: 50, Mapping: payer("PARA500", "Paracetamol 500mg")},
			{ID: "l2", Description: "FBC", Quantity: 1, UnitPrice: 8000, Mapping: payer("FBC", "Full blood count")},
			{ID: "l3", Description: "Paracetamol tabs", Quantity: 10, UnitPrice: 50, Mapping: payer("PARA500", "Paracetamol 500mg")},
		},
		Diagnoses: []claim.Diagnosis{{Code: claim.Code{System: "icd-10", Value: "B54"}, Role: claim.Primary,
			Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "6165"}}}},
	}}
}

func send(t *testing.T, m connector.Connector, c *claim.Claim, p *engine.Progress) (connector.Receipt, error) {
	t.Helper()
	man := m.Manifest()
	return engine.Send(context.Background(), m, engine.Build(c, man.Submission), engine.ChoosePlan(man, man.Submission), p,
		func(context.Context, *engine.Progress) error { return nil })
}

func TestClaimGoesThroughEveryStep(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	c := sampleClaim()
	c.OTP = &claim.OTP{Code: "123456", Challenge: "PA-77"}
	r, err := send(t, m, c, &engine.Progress{})
	if err != nil || !r.Acknowledged {
		t.Fatalf("receipt %+v err %v", r, err)
	}
	b := f.batches[r.PayerClaimNumber]
	if b == nil || !b.final || b.member != "4411" || b.class != "1" || b.bauth != "123456" || b.pauth != "PA-77" {
		t.Fatalf("batch %+v (opened for the benId, outpatient, with the OTP)", b)
	}
	if f.logins != 1 {
		t.Errorf("one login for the claim, got %d", f.logins)
	}
	if n := len(f.calledOp("p.qbid")); n != 1 {
		t.Errorf("the member is looked up once per claim, got %d", n)
	}
	if len(b.rows) != 2 {
		t.Fatalf("grouped by product: want 2 rows, got %d", len(b.rows))
	}
	for _, rw := range b.rows {
		switch rw.prod {
		case "PARA500":
			if rw.qty != 20 || rw.cost != "10.00" || rw.cat != "108" || rw.icd != "6165" {
				t.Errorf("paracetamol row %+v (qty added up, cost is the line total, numeric diagnosis id)", rw)
			}
		case "FBC":
			if rw.cat != "211" {
				t.Errorf("FBC must take the covered category: %+v", rw)
			}
		}
	}
	if len(b.claimed) != 2 {
		t.Errorf("every dispensation is claimed before the batch closes: %v", b.claimed)
	}
	search := f.calledOp("p.prodsearchw")[0].Get("n")
	if search != "+Paracetamol*+500mg*" {
		t.Errorf("search string %q", search)
	}
}

func TestInpatientIsClassTwoAndLinesStaySeparate(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	c := sampleClaim()
	c.Lines = c.Lines[:2]
	c.Encounter.Type = claim.Inpatient
	r, err := send(t, m, c, &engine.Progress{})
	if err != nil {
		t.Fatal(err)
	}
	if f.batches[r.PayerClaimNumber].class != "2" {
		t.Errorf("inpatient class")
	}
}

func TestStaleConnectionLogsInAgainOnce(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	if _, err := m.VerifyMember(context.Background(), "GHI-0001", ""); err != nil {
		t.Fatal(err)
	}
	f.staleNext = true
	if _, err := m.VerifyMember(context.Background(), "GHI-0001", ""); err != nil {
		t.Fatal(err)
	}
	if f.logins != 2 {
		t.Errorf("want a second login after the stale connid, got %d", f.logins)
	}
}

func TestBadPasswordIsInvalidCredentials(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	a.Credentials["password"] = "wrong"
	c, _ := connector.New(Key, a)
	_, err := c.(*Medserve).WithClient(srv.Client()).VerifyMember(context.Background(), "GHI-0001", "")
	if connector.AsError(err).Code != connector.InvalidCredentials {
		t.Errorf("got %v", err)
	}
}

func TestMemberChecksAndOTP(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	ctx := context.Background()
	v, err := m.VerifyMember(ctx, "GHI-0001", "")
	if err != nil || !v.Eligible || v.PlanID != "132" || v.PlanName != "basic" || v.Member.FirstName != "Ama" ||
		v.Member.LastName != "Mensah" || v.Member.BirthDate.String() != "1990-04-02" || v.CoverEnds.String() != "2026-12-31" || v.OTPRequired {
		t.Errorf("active member: %+v %v", v, err)
	}
	if _, err := m.VerifyMember(ctx, "NOPE", ""); connector.AsError(err).Code != connector.MemberNotFound {
		t.Errorf("missing member: %v", err)
	}
	if v, _ := m.VerifyMember(ctx, "OLD-1", ""); v.Eligible || v.Reason != "inactive" {
		t.Errorf("expired member: %+v", v)
	}
	f.otpInsurer = true
	if v, _ := m.VerifyMember(ctx, "GHI-0001", ""); !v.OTPRequired {
		t.Errorf("insurer requires an auth code")
	}
	if ch, err := m.SendOTP(ctx, "GHI-0001"); err != nil || ch != "PA-77" {
		t.Errorf("send otp: %q %v", ch, err)
	}
}

func TestProductNotCoveredOrUnknown(t *testing.T) {
	_, srv := newFake(t)
	m := open(t, srv)
	c := sampleClaim()
	c.Lines[1].Mapping.Target.Value, c.Lines[1].Mapping.Target.Display = "COSM", "Cosmetic"
	_, err := send(t, m, c, &engine.Progress{})
	if ce := connector.AsError(err); ce.Code != connector.PayerRefused || !ce.Final() {
		t.Errorf("not covered: %v", err)
	}
	c = sampleClaim()
	c.Lines[1].Mapping.Target.Value = "NOSUCH"
	if _, err := send(t, m, c, &engine.Progress{}); connector.AsError(err).Code != connector.CodeNotRecognised {
		t.Errorf("unknown product: %v", err)
	}
}

func TestCoverExhaustedFailsAndLeavesNothingClaimed(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	f.chargePatient["FBC"] = true
	_, err := send(t, m, sampleClaim(), &engine.Progress{})
	ce := connector.AsError(err)
	if ce.Code != connector.PayerRefused || !ce.Final() || ce.PayerMessage != "Benefit cap reached" {
		t.Fatalf("got %v", err)
	}
	for _, b := range f.batches {
		for _, rw := range b.rows {
			if rw.prod == "FBC" && rw.qty != 0 {
				t.Errorf("the refused line is dispensed back to zero: %+v", rw)
			}
		}
	}
}

func TestApprovalWaitsThenFinalises(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	f.approval["FBC"] = "pending"
	c := sampleClaim()
	var p engine.Progress
	_, err := send(t, m, c, &p)
	if ce := connector.AsError(err); ce.Code != connector.AwaitingPayer || ce.Final() {
		t.Fatalf("want a retryable wait, got %v", err)
	}
	if len(f.calledOp("p.adapreq")) != 1 {
		t.Errorf("the approval is requested")
	}
	if _, err := send(t, m, c, &p); connector.AsError(err).Code != connector.AwaitingPayer {
		t.Errorf("still pending: %v", err)
	}
	f.approval["FBC"] = "approved"
	r, err := send(t, m, c, &p)
	if err != nil || !r.Acknowledged || !f.batches[r.PayerClaimNumber].final {
		t.Fatalf("after approval: %+v %v", r, err)
	}
	if n := len(f.calledOp("p.sba")); n != 1 {
		t.Errorf("one batch, got %d", n)
	}
	f.approval["FBC"] = "denied"
	c2 := sampleClaim()
	c2.ID = "clm_2"
	var p2 engine.Progress
	if _, err := send(t, m, c2, &p2); connector.AsError(err).Code != connector.AwaitingPayer {
		t.Fatalf("first pass waits: %v", err)
	}
	if _, err := send(t, m, c2, &p2); connector.AsError(err).Code != connector.PayerRefused {
		t.Errorf("denied approval is a refusal: %v", err)
	}
}

// lossy loses the reply of the first line add after Medserve stored it.
type lossy struct {
	*Medserve
	lost bool
}

func (l *lossy) AddLine(ctx context.Context, s *connector.Submission, n string, line connector.SendLine) (string, error) {
	ref, err := l.Medserve.AddLine(ctx, s, n, line)
	if err == nil && !l.lost {
		l.lost = true
		return "", connector.Errorf(connector.Timeout, "reply lost")
	}
	return ref, err
}

func TestLostLineReplyIsAdoptedOnRetry(t *testing.T) {
	f, srv := newFake(t)
	x := &lossy{Medserve: open(t, srv)}
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, x, c, &p); err == nil {
		t.Fatal("want the lost reply")
	}
	r, err := send(t, x, c, &p)
	if err != nil || !r.Acknowledged {
		t.Fatalf("retry: %+v %v", r, err)
	}
	b := f.batches[r.PayerClaimNumber]
	if len(b.rows) != 2 || len(b.claimed) != 2 || len(f.calledOp("p.sba")) != 1 {
		t.Errorf("one batch with two claimed rows, got rows=%d claimed=%v", len(b.rows), b.claimed)
	}
}

func TestCorrectionRemovesAndUpdatesLines(t *testing.T) {
	f, srv := newFake(t)
	m := open(t, srv)
	f.chargePatient["FBC"] = true
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, m, c, &p); err == nil {
		t.Fatal("want the cap refusal")
	}
	f.chargePatient["FBC"] = false
	c.Lines = []claim.Line{c.Lines[0]} // the biller drops the FBC and one paracetamol line
	c.Lines[0].Quantity = 4
	r, err := send(t, m, c, &p)
	if err != nil {
		t.Fatal(err)
	}
	b := f.batches[r.PayerClaimNumber]
	for _, rw := range b.rows {
		if rw.prod == "PARA500" && (rw.qty != 4 || rw.cost != "2.00") {
			t.Errorf("updated row %+v", rw)
		}
		if rw.prod == "FBC" && rw.qty != 0 {
			t.Errorf("removed row %+v", rw)
		}
	}
	if len(b.claimed) != 1 {
		t.Errorf("only the remaining line is claimed: %v", b.claimed)
	}
}

func TestResendAfterLostFinaliseIsAcknowledged(t *testing.T) {
	_, srv := newFake(t)
	m := open(t, srv)
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, m, c, &p); err != nil {
		t.Fatal(err)
	}
	s := engine.Build(c, m.Manifest().Submission)
	s.Sent = map[string]string{}
	for k, l := range p.Lines {
		s.Sent[k] = l.Ref
	}
	if r, err := m.FinalizeClaim(context.Background(), s, p.PayerClaimNumber); err != nil || !r.Acknowledged {
		t.Errorf("already finalised counts as acknowledged: %+v %v", r, err)
	}
}

func TestSearchString(t *testing.T) {
	for in, want := range map[string]string{
		"FULL BLOOD COUNT (FBC)":       "+FULL*+BLOOD*+COUNT*+FBC*",
		"Paracetamol, 500mg":           "+Paracetamol*+500mg*",
		"Amoxicillin, oral suspension": "+Amoxicillin*+'oral suspension'*",
		"X":                            "+X*",
		"":                             "",
	} {
		if got := SearchString(in); got != want {
			t.Errorf("SearchString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoverage(t *testing.T) {
	cat, cov := parseCoverage("op-lab/210/0\nop-lab-special/211/1")
	if cat != "211" || cov == nil || !*cov {
		t.Errorf("covered line wins: %s %v", cat, cov)
	}
	cat, cov = parseCoverage("op-excl/300/0")
	if cat != "300" || cov == nil || *cov {
		t.Errorf("not covered: %s %v", cat, cov)
	}
}
