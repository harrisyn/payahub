package smartclaims

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/httpx"
	"github.com/harrisyn/payahub/internal/engine"
)

// fake is a SmartClaims server, built from the request and reply shapes
// core-api-v2's smartclaims.py sends and reads.
type fake struct {
	t  *testing.T
	mu sync.Mutex

	tokens     int
	expireNext bool
	bodies     map[string][]map[string]any
	claims     map[string]*fakeClaim // by guid
	// per item code: a rule SmartClaims reports on additem with success 1
	ruleOnAdd map[string]string
	badCode   map[string]bool // answered with HTTP 400 and a failedValidation dump
}

type fakeClaim struct {
	ccc, member, service string
	items                map[string]map[string]any // by refid
	closed               bool
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	f := &fake{t: t, bodies: map[string][]map[string]any{}, claims: map[string]*fakeClaim{},
		ruleOnAdd: map[string]string{}, badCode: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies[path] = append(f.bodies[path], body)
	write := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	if path == "/token" {
		q := r.URL.Query()
		if r.Method != http.MethodPost || q.Get("username") != "ridge" || q.Get("password") != "s3cret" {
			write(401, map[string]any{"success": 0, "user_msg": "invalid credentials"})
			return
		}
		f.tokens++
		write(200, map[string]any{"success": 1, "token": fmt.Sprintf("tok-%d", f.tokens)})
		return
	}
	if r.Header.Get("Authorization") != fmt.Sprintf("tok-%d", f.tokens) || f.expireNext {
		f.expireNext = false
		write(401, map[string]any{"user_msg": "token expired"})
		return
	}
	switch path {
	case "/members/getinfo":
		q := r.URL.Query()
		if q.Get("hicd") != "NMH01" {
			f.t.Errorf("getinfo hicd %q", q.Get("hicd"))
		}
		switch q.Get("memberno") {
		case "NOPE":
			write(404, map[string]any{"success": 0, "user_msg": "member not found"})
		case "OLD-1":
			write(200, map[string]any{"clientInfo": map[string]any{"memberno": "OLD-1", "status": "Inactive", "otppolicy": "required"}})
		case "OPT-1":
			write(200, map[string]any{"clientInfo": map[string]any{"memberno": "OPT-1", "status": "Active", "otppolicy": "optional"}})
		default:
			write(200, map[string]any{"clientInfo": map[string]any{
				"memberno": q.Get("memberno"), "surname": "Mensah", "othernames": "Ama", "dateofbirth": "1990-04-02",
				"gender": "Female", "status": "Active", "otppolicy": "conditional",
				"policies": []any{map[string]any{"policyno": 295555, "policyplan": "GOLD", "policyplanName": "Gold Plan", "expirydate": "2026-12-31T00:00:00"}},
			}})
		}
	case "/members/getauthorization":
		if body["policyno"] != "295555" || body["hicd"] != "NMH01" {
			f.t.Errorf("getauthorization %v", body)
		}
		write(200, map[string]any{"success": 1, "serviceInfo": map[string]any{"ccc": "CCC-77", "otptel": "0244000000"}})
	case "/members/startattendance":
		if body["otp"] != "4321" {
			write(400, map[string]any{"success": 0, "user_msg": "Invalid OTP supplied"})
			return
		}
		guid := fmt.Sprintf("guid-%d", len(f.claims)+1)
		f.claims[guid] = &fakeClaim{ccc: "CCC-77", member: body["memberno"].(string), service: body["typeofservice"].(string), items: map[string]map[string]any{}}
		write(200, map[string]any{"success": 1, "attendanceDate": "2026-10-20T00:00:00",
			"claimData": map[string]any{"item": map[string]any{"claimCheckCode": "CCC-77", "guid": guid, "id": 991}}})
	case "/txn/additem", "/txn/updateitem":
		c := f.claims[body["claimGUID"].(string)]
		item := body["item"].(map[string]any)
		ref := item["refid"].(string)
		code, _ := item["code"].(string)
		if code == "" {
			code, _ = item["medicineCode"].(string)
		}
		if f.badCode[code] {
			write(400, map[string]any{"success": 0, "errors": []any{map[string]any{"code": "invalidProcedureEntryGdrgCode"}},
				"failedValidation": map[string]any{"memberInfo": map[string]any{"surname": "Mensah", "nhisno": "SECRET-NHIS"}}})
			return
		}
		if c.closed {
			write(200, map[string]any{"success": 0, "user_msg": "Claim is closed"})
			return
		}
		_, exists := c.items[ref]
		if path == "/txn/additem" && exists {
			write(409, map[string]any{"success": 0, "user_msg": "refid already exists on the claim"})
			return
		}
		if path == "/txn/updateitem" && !exists {
			write(404, map[string]any{"success": 0})
			return
		}
		item["itemType"] = body["itemType"]
		c.items[ref] = item
		out := map[string]any{"success": 1, "refid": ref, "itemType": body["itemType"], "errors": []any{}, "warnings": []any{}}
		if rule := f.ruleOnAdd[code]; rule != "" && path == "/txn/additem" {
			out["warnings"] = []any{map[string]any{"code": rule, "info": ""}}
		}
		write(200, out)
	case "/txn/deleteitem":
		c := f.claims[body["claimGUID"].(string)]
		if _, ok := c.items[body["refid"].(string)]; !ok {
			write(404, map[string]any{"success": 0, "user_msg": "item not found"})
			return
		}
		delete(c.items, body["refid"].(string))
		write(200, map[string]any{"success": 1, "refid": body["refid"]})
	case "/claims/setstatus":
		var c *fakeClaim
		for _, x := range f.claims {
			if x.ccc == body["attendancecode"] {
				c = x
			}
		}
		if c == nil || body["status"] != "CLOSED" || body["attendancedate"] != "2026-10-20" {
			f.t.Errorf("setstatus %v", body)
			write(400, map[string]any{"success": 0, "user_msg": "bad request"})
			return
		}
		if c.closed {
			write(200, map[string]any{"success": 0, "user_msg": "Claim already closed"})
			return
		}
		hasDx := false
		for _, it := range c.items {
			if it["itemType"] == "diagnosis" {
				hasDx = true
			}
		}
		if !hasDx {
			write(200, map[string]any{"success": 1, "errors": []any{map[string]any{"code": "diagnosisEntriesRequired"}}})
			return
		}
		c.closed = true
		write(200, map[string]any{"success": 1})
	case "/hms/services":
		if r.URL.Query().Get("limit") != "-1" {
			f.t.Errorf("services limit %q", r.URL.Query().Get("limit"))
		}
		write(200, map[string]any{"success": 1, "data": map[string]any{
			"0": map[string]any{"code": "OPDC06A", "description": "Consultation"},
			"1": map[string]any{"code": "LABS22D", "description": "Malaria RDT"},
		}})
	case "/hms/medicines":
		write(200, map[string]any{
			"0": map[string]any{"code": "PARACETTA1", "description": "Paracetamol 500mg tab"},
		})
	default:
		http.NotFound(w, r)
	}
}

func account(srv *httptest.Server) connector.Account {
	return connector.Account{
		Credentials: map[string]string{"base_url": srv.URL, "username": "ridge", "password": "s3cret"},
		PayerID:     "nmh", PayerFields: map[string]string{"hicd": "NMH01"},
	}
}

func open(t *testing.T, srv *httptest.Server) *SmartClaims {
	t.Helper()
	httpx.SharedTokens.Drop(httpx.Key(Key, srv.URL, "ridge", "s3cret"))
	c, err := connector.New(Key, account(srv))
	if err != nil {
		t.Fatal(err)
	}
	return c.(*SmartClaims).WithClient(srv.Client())
}

func sampleClaim() *claim.Claim {
	payer := func(code string) *claim.CodeMapping {
		return &claim.CodeMapping{Target: &claim.Code{System: "payer:nmh", Value: code}}
	}
	return &claim.Claim{ID: "clm_1", Input: claim.Input{
		PayerID: "nmh", Currency: "GHS",
		Member:    claim.Member{MemberNumber: "NMH-001", PolicyNumber: "295555"},
		Provider:  claim.Provider{Name: "Ridge Clinic"},
		Encounter: claim.Encounter{Type: claim.Outpatient, StartDate: claim.NewDate(2026, 10, 20), Reference: "VIS-0042"},
		OTP:       &claim.OTP{Code: "4321", Challenge: "CCC-77"},
		Lines: []claim.Line{
			{ID: "l1", Description: "Consultation", Quantity: 1, UnitPrice: 15000, Mapping: payer("OPDC06A")},
			{ID: "l2", Description: "Paracetamol 500mg", Category: claim.CategoryDrug, Quantity: 10, UnitPrice: 50,
				Mapping: payer("PARACETTA1"), Prescription: &claim.Prescription{Dose: "1 tab", Frequency: "tds", Duration: "5 days"}},
			{ID: "l3", Description: "Dressing pack", Quantity: 2, UnitPrice: 1250,
				Code: &claim.Code{System: "org", Value: "DRS-1"}},
		},
		Diagnoses: []claim.Diagnosis{{Code: claim.Code{System: "icd-10", Value: "B54"}, Role: claim.Primary,
			Description: "Malaria, unspecified", Status: claim.Confirmed}},
	}}
}

func send(t *testing.T, c connector.Connector, cl *claim.Claim, p *engine.Progress) (connector.Receipt, error) {
	t.Helper()
	m := c.Manifest()
	return engine.Send(context.Background(), c, engine.Build(cl, m.Submission), engine.ChoosePlan(m, m.Submission), p,
		func(context.Context, *engine.Progress) error { return nil })
}

func TestClaimGoesThroughEveryStep(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	r, err := send(t, s, sampleClaim(), &engine.Progress{})
	if err != nil || !r.Acknowledged || r.PayerClaimNumber != "CCC-77|guid-1|2026-10-20" {
		t.Fatalf("receipt %+v %v", r, err)
	}
	start := f.bodies["/members/startattendance"][0]
	if start["otp"] != "4321" || start["memberno"] != "NMH-001" || start["policyno"] != "295555" ||
		start["typeofservice"] != "OPD" || start["typeofattendance"] != "EAE" || start["hicd"] != "NMH01" {
		t.Errorf("startattendance %v", start)
	}
	c := f.claims["guid-1"]
	if !c.closed || len(c.items) != 4 {
		t.Fatalf("claim %+v", c)
	}
	var med, proc, raw, dx map[string]any
	for _, it := range c.items {
		switch {
		case it["itemType"] == "diagnosis":
			dx = it
		case it["medicineCode"] == "PARACETTA1":
			med = it
		case it["code"] == "OPDC06A":
			proc = it
		default:
			raw = it
		}
	}
	if med["itemType"] != "medicine" || med["dispensedQty"] != 10.0 || med["price"] != 5.0 || med["serviceDate"] != "2026-10-20" {
		t.Errorf("medicine %v (price is the line total)", med)
	}
	if rx := med["prescription"].(map[string]any); rx["dose"] != "1 tab" || rx["frequency"] != "tds" || rx["duration"] != "5 days" {
		t.Errorf("prescription %v", rx)
	}
	if proc["itemType"] != "procedure" || proc["qty"] != 1.0 || proc["price"] != 150.0 {
		t.Errorf("procedure %v", proc)
	}
	if _, has := raw["code"]; has || raw["description"] != "Dressing pack" || raw["price"] != 25.0 {
		t.Errorf("an unmapped line goes by description, without our code: %v", raw)
	}
	if dx["icd10"] != "B54" || dx["diagnosistype"] != "Final Diagnosis" || dx["diagnosis"] != "Malaria, unspecified" || dx["refid"] != "dx-B54" {
		t.Errorf("diagnosis %v", dx)
	}
	set := f.bodies["/claims/setstatus"][0]
	if set["claimid"] != "VIS-0042" || set["attendancecode"] != "CCC-77" || set["memberno"] != "NMH-001" {
		t.Errorf("setstatus %v", set)
	}
	if f.tokens != 1 {
		t.Errorf("one sign-in per claim, got %d", f.tokens)
	}
}

func TestNoOTPNoClaim(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	c := sampleClaim()
	c.OTP = nil
	if _, err := send(t, s, c, &engine.Progress{}); connector.AsError(err).Code != connector.OTPRequired {
		t.Errorf("no otp: %v", err)
	}
	if len(f.bodies["/members/startattendance"]) != 0 {
		t.Errorf("nothing is sent without the OTP")
	}
	c.OTP = &claim.OTP{Code: "0000"}
	if _, err := send(t, s, c, &engine.Progress{}); connector.AsError(err).Code != connector.OTPRequired {
		t.Errorf("wrong otp: %v", err)
	}
}

func TestMedicineWithoutPrescriptionIsCaughtBeforeSending(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	c := sampleClaim()
	c.Lines[1].Prescription = nil
	_, err := send(t, s, c, &engine.Progress{})
	if ce := connector.AsError(err); ce.Code != connector.Validation || !strings.Contains(ce.UserMessage, "Paracetamol") {
		t.Errorf("got %v", err)
	}
	for _, b := range f.bodies["/txn/additem"] {
		if b["itemType"] == "medicine" {
			t.Errorf("the medicine must not be sent")
		}
	}
}

func TestRuleInWarningsIsAnErrorAndTheItemIsTakenOff(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	f.ruleOnAdd["OPDC06A"] = "priceAdjustWarning"
	_, err := send(t, s, sampleClaim(), &engine.Progress{})
	ce := connector.AsError(err)
	if ce.Code != connector.PayerRefused || !strings.Contains(ce.UserMessage, "adjusted to agreed price") {
		t.Fatalf("got %v", err)
	}
	if n := len(f.claims["guid-1"].items); n != 0 {
		t.Errorf("the refused item must be deleted again; %d left", n)
	}
}

func TestWarningsReachTheReceipt(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	f.ruleOnAdd["OPDC06A"] = "duplicateClaim"
	r, err := send(t, s, sampleClaim(), &engine.Progress{})
	if err != nil || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "duplicate") {
		t.Errorf("receipt %+v %v", r, err)
	}
}

func TestBadCodeNeverLeaksTheValidationDump(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	f.badCode["OPDC06A"] = true
	_, err := send(t, s, sampleClaim(), &engine.Progress{})
	ce := connector.AsError(err)
	if ce.Code != connector.CodeNotRecognised || !ce.Final() {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error()+ce.UserMessage+ce.PayerMessage, "SECRET-NHIS") {
		t.Errorf("failedValidation leaked: %v", err)
	}
}

// lossy loses the reply of the first line add after SmartClaims stored it.
type lossy struct {
	*SmartClaims
	lost bool
}

func (l *lossy) AddLine(ctx context.Context, s *connector.Submission, n string, line connector.SendLine) (string, error) {
	ref, err := l.SmartClaims.AddLine(ctx, s, n, line)
	if err == nil && !l.lost {
		l.lost = true
		return "", connector.Errorf(connector.Timeout, "reply lost")
	}
	return ref, err
}

func TestLostLineReplyIsSentAgainAsAnUpdate(t *testing.T) {
	f, srv := newFake(t)
	x := &lossy{SmartClaims: open(t, srv)}
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, x, c, &p); err == nil {
		t.Fatal("want the lost reply")
	}
	r, err := send(t, x, c, &p)
	if err != nil || !r.Acknowledged {
		t.Fatalf("retry: %+v %v", r, err)
	}
	if n := len(f.claims["guid-1"].items); n != 4 || len(f.bodies["/members/startattendance"]) != 1 {
		t.Errorf("one claim with 4 items, got %d", n)
	}
	if len(f.bodies["/txn/updateitem"]) != 1 {
		t.Errorf("the 409 is followed by an update")
	}
}

func TestCorrectionUpdatesAndDeletesInPlace(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	c := sampleClaim()
	c.Diagnoses = nil
	var p engine.Progress
	_, err := send(t, s, c, &p)
	if ce := connector.AsError(err); ce.Code != connector.PayerRefused || !strings.Contains(ce.UserMessage, "Diagnosis is required") {
		t.Fatalf("want the rule from setstatus, got %v", err)
	}
	c = sampleClaim()
	c.Lines = c.Lines[:2]
	c.Lines[0].Quantity = 2
	r, err := send(t, s, c, &p)
	if err != nil || !r.Acknowledged {
		t.Fatal(err)
	}
	held := f.claims["guid-1"]
	if len(held.items) != 3 || !held.closed || len(f.bodies["/members/startattendance"]) != 1 {
		t.Errorf("same claim, 2 lines and the diagnosis: %v", held.items)
	}
}

func TestResendAfterLostCloseIsAcknowledged(t *testing.T) {
	_, srv := newFake(t)
	s := open(t, srv)
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, s, c, &p); err != nil {
		t.Fatal(err)
	}
	if r, err := s.FinalizeClaim(context.Background(), engine.Build(c, manifest.Submission), p.PayerClaimNumber); err != nil || !r.Acknowledged {
		t.Errorf("already closed: %+v %v", r, err)
	}
}

func TestMemberChecksAndOTP(t *testing.T) {
	f, srv := newFake(t)
	s := open(t, srv)
	ctx := context.Background()
	v, err := s.VerifyMember(ctx, "NMH-001", "")
	if err != nil || !v.Eligible || !v.OTPRequired || v.Member.FirstName != "Ama" || v.Member.LastName != "Mensah" ||
		v.Member.PolicyNumber != "295555" || v.PlanName != "Gold Plan" || v.CoverEnds.String() != "2026-12-31" ||
		v.Member.BirthDate.String() != "1990-04-02" {
		t.Errorf("member %+v %v", v, err)
	}
	if v, _ := s.VerifyMember(ctx, "OPT-1", ""); v.OTPRequired {
		t.Errorf("optional otp policy")
	}
	if v, _ := s.VerifyMember(ctx, "OLD-1", ""); v.Eligible || v.Reason != "inactive" {
		t.Errorf("inactive %+v", v)
	}
	if _, err := s.VerifyMember(ctx, "NOPE", ""); connector.AsError(err).Code != connector.MemberNotFound {
		t.Errorf("missing %v", err)
	}
	if ch, err := s.SendOTP(ctx, "NMH-001"); err != nil || ch != "CCC-77" {
		t.Errorf("otp %q %v", ch, err)
	}
	f.expireNext = true
	if _, err := s.VerifyMember(ctx, "NMH-001", ""); err != nil || f.tokens != 2 {
		t.Errorf("a 401 signs in again once: %v tokens=%d", err, f.tokens)
	}
}

func TestBadPassword(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	a.Credentials["password"] = "nope"
	c, _ := connector.New(Key, a)
	if _, err := c.(*SmartClaims).WithClient(srv.Client()).VerifyMember(context.Background(), "NMH-001", ""); connector.AsError(err).Code != connector.InvalidCredentials {
		t.Errorf("got %v", err)
	}
}

func TestCatalogue(t *testing.T) {
	_, srv := newFake(t)
	s := open(t, srv)
	items, err := s.ListCatalog(context.Background(), connector.CatalogItems)
	if err != nil || len(items) != 3 || items[0].Code != "OPDC06A" || items[2].Group != "medicine" {
		t.Errorf("items %+v %v", items, err)
	}
}

func TestAccount(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	delete(a.PayerFields, "hicd")
	if _, err := connector.New(Key, a); err == nil {
		t.Errorf("a payer without its HICD must not build")
	}
	a = account(srv)
	delete(a.Credentials, "base_url")
	if c, err := connector.New(Key, a); err != nil || c.(*SmartClaims).base != "https://claims.nmhapps.com" {
		t.Errorf("default server: %v", err)
	}
}
