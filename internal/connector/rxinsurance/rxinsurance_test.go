package rxinsurance

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
	"github.com/harrisyn/payahub/internal/engine"
)

// fake is RX's restapi/index.php, built from the request and reply shapes
// core-api-v2's rx_insurance.py sends and reads.
type fake struct {
	t  *testing.T
	mu sync.Mutex

	calls                []map[string]string
	claims               map[string]*fakeClaim // by claim number
	byHMS                map[string]string     // member|hmsno -> claim number
	noBenefit, needsAuth map[string]bool
	expireNext           bool // the next line call finds the claim expired
	noisy                bool // PHP notices before the JSON
}

type fakeClaim struct {
	hms       string
	lines     map[string]map[string]string
	submitted bool
	expired   bool
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	f := &fake{t: t, claims: map[string]*fakeClaim{}, byHMS: map[string]string{}, noBenefit: map[string]bool{}, needsAuth: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fake) of(method string) []map[string]string {
	var out []map[string]string
	for _, c := range f.calls {
		if c["METHOD"] == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var b map[string]string
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		f.t.Errorf("body is not a JSON object of strings: %v", err)
	}
	f.calls = append(f.calls, b)
	if f.noisy {
		fmt.Fprint(w, "<br />\n<b>Notice</b>:  Undefined index: X in /var/www/api.php on line 12<br />\n")
	}
	out := func(code, msg string, extra map[string]any) {
		m := map[string]any{"STATUSCODE": code, "STATUSMSG": msg}
		for k, v := range extra {
			m[k] = v
		}
		_ = json.NewEncoder(w).Encode(m)
	}
	if b["APIKEY"] != "key-1" || b["PASSWORD"] != "s3cret" || b["PROVIDERID"] != "493" || b["USERID"] != "12" {
		out("101", "Invalid API credentials", nil)
		return
	}
	if b["INSCOMPANYCODE"] != "RXH" || b["RESPONSETYPE"] != "JSON" || b["ATTENDINGOFFICER"] != "System User" {
		f.t.Errorf("%s context %v", b["METHOD"], b)
	}
	claimFor := func() *fakeClaim {
		c := f.claims[b["CLAIMNUMBER"]]
		if c == nil {
			out("020", "Claim number not found", nil)
		}
		return c
	}
	switch b["METHOD"] {
	case "MEMBERSTATUS":
		switch b["MEMBERSHIPID"] {
		case "NOPE":
			out("004", "No Data Found", nil)
		case "PLAN-0":
			out("000", "Results Found", map[string]any{"MEMBERSTATUS": "Active", "PLANSTATUS": "Suspended", "EMPLOYERSTATUS": "Active"})
		default:
			out("000", "Results Found", map[string]any{"MEMBERSTATUS": "Active", "PLANSTATUS": "Active", "EMPLOYERSTATUS": "Active",
				"USEPIN": "Yes", "MEMBERSHIPID": b["MEMBERSHIPID"], "FIRSTNAME": "Kwame ", "SURNAME": "Boateng", "OTHERNAMES": "",
				"DATEOFBIRTH": "1985-01-15", "SEX": "Male", "MEMBERPLAN": "Corporate Gold", "MEMBERPLANID": "CG1", "EXPIRYDATE": "2026-12-31", "TEL": "0200000000"})
		}
	case "SENDSMS":
		out("000", "SMS Sent", nil)
	case "PROCESSCLAIMNO":
		if b["PIN"] != "8642" {
			out("030", "Invalid PIN supplied", nil)
			return
		}
		key := b["MEMBERSHIPID"] + "|" + b["HMSNO"]
		if n, ok := f.byHMS[key]; ok {
			out("000", "Claim Number Generated", map[string]any{"CLAIMNUMBER": n})
			return
		}
		n := fmt.Sprintf("RX0049300000%04dP523", len(f.claims)+1)
		f.byHMS[key] = n
		f.claims[n] = &fakeClaim{hms: b["HMSNO"], lines: map[string]map[string]string{}}
		out("000", "Claim Number Generated", map[string]any{"CLAIMNUMBER": n})
	case "ADDCLAIM", "UPDATECLAIM", "DELETECLAIM":
		c := claimFor()
		if c == nil {
			return
		}
		if f.expireNext {
			f.expireNext = false
			c.expired = true
		}
		if c.expired {
			out("015", "Life Span of Claim has Expired", nil)
			return
		}
		item := b["PROVIDERITEMID"]
		switch b["METHOD"] {
		case "ADDCLAIM":
			if f.needsAuth[item] {
				out("016", "This item requires Claims Authorization", nil)
				return
			}
			if _, ok := c.lines[item]; ok {
				out("017", "Item already exists on claim", nil)
				return
			}
			if item == "BOGUS" {
				out("018", "Item BOGUS not found in database", nil)
				return
			}
			c.lines[item] = b
			if f.noBenefit[item] {
				out("019", "No Benefit for this item", nil)
				return
			}
			out("000", "Claims data has been added for the given item", nil)
		case "UPDATECLAIM":
			c.lines[item] = b
			out("000", "Claims data has been updated for the given item", nil)
		case "DELETECLAIM":
			delete(c.lines, item)
			out("000", "Claims data has been deleted for the given item", nil)
		}
	case "SUBMITCLAIM":
		c := claimFor()
		if c == nil {
			return
		}
		if c.submitted {
			out("013", "Claim already submitted", nil)
			return
		}
		c.submitted = true
		out("012", "Claims Submitted Sucessfully", map[string]any{"TOTALCLAIM": "160.00"})
	case "VIEWCLAIMSLOCAL":
		c := claimFor()
		if c == nil {
			return
		}
		var rows []any
		for code, l := range c.lines {
			rows = append(rows, map[string]any{"itemcode": code, "item": l["ITEMNAME"], "qty": l["QTY"], "price": l["UNITPRICE"]})
		}
		out("000", "Results Found", map[string]any{"RESULTS": rows})
	case "ITEMLIST":
		out("000", "Results Found", map[string]any{"ITEMLIST": []any{
			map[string]any{"PROVIDERITEMID": "ADM04415", "ITEMNAME": "Specialist Consultation", "PRICE": "120.00"},
			map[string]any{"ITEMCODE": "LAB001", "NAME": "FBC"},
		}})
	case "DIAGNOSISLIST":
		out("000", "Results Found", map[string]any{"RESULTS": []any{map[string]any{"icdcode": "B54", "diagnosis": "Malaria"}}})
	default:
		out("999", "Unknown method", nil)
	}
}

func account(srv *httptest.Server) connector.Account {
	return connector.Account{
		Credentials: map[string]string{"base_url": srv.URL + "/rxclaim_client/restapi/index.php", "api_key": "key-1",
			"username": "ridge", "password": "s3cret", "provider_id": "493", "user_id": "12"},
		PayerID: "rx", PayerFields: map[string]string{"ins_company_code": "RXH"},
	}
}

func open(t *testing.T, srv *httptest.Server) *RX {
	t.Helper()
	c, err := connector.New(Key, account(srv))
	if err != nil {
		t.Fatal(err)
	}
	return c.(*RX).WithClient(srv.Client())
}

func sampleClaim() *claim.Claim {
	payer := func(code string) *claim.CodeMapping {
		return &claim.CodeMapping{Target: &claim.Code{System: "payer:rx", Value: code}}
	}
	return &claim.Claim{ID: "clm_1", Input: claim.Input{
		PayerID: "rx", Currency: "GHS",
		Member:    claim.Member{MemberNumber: "RXH0005462-2"},
		Provider:  claim.Provider{Name: "Ridge Clinic"},
		Encounter: claim.Encounter{Type: claim.Outpatient, StartDate: claim.NewDate(2026, 10, 20), Reference: "11802390"},
		OTP:       &claim.OTP{Code: "8642"},
		Lines: []claim.Line{
			{ID: "l1", Description: "Specialist consultation", Quantity: 1, UnitPrice: 12000, Mapping: payer("ADM04415")},
			{ID: "l2", Description: "FBC", Quantity: 1, UnitPrice: 2000, Mapping: payer("LAB001")},
			{ID: "l3", Description: "FBC repeat", Quantity: 1, UnitPrice: 2000, Mapping: payer("LAB001")},
		},
		Diagnoses: []claim.Diagnosis{
			{Code: claim.Code{System: "icd-10", Value: "A03"}, Role: claim.Secondary},
			{Code: claim.Code{System: "icd-10", Value: "B54"}, Role: claim.Primary},
		},
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
	f.noisy = true
	x := open(t, srv)
	r, err := send(t, x, sampleClaim(), &engine.Progress{})
	if err != nil || !r.Acknowledged || r.PayerClaimNumber != "RX00493000000001P523" {
		t.Fatalf("receipt %+v %v", r, err)
	}
	open := f.of("PROCESSCLAIMNO")[0]
	if open["MEMBERSHIPID"] != "RXH0005462-2" || open["DATEOFCONSULTATION"] != "2026-10-20" || open["TYPEOFVISIT"] != "Out-Patient" ||
		open["HMSNO"] != "11802390" || open["PIN"] != "8642" {
		t.Errorf("PROCESSCLAIMNO %v", open)
	}
	c := f.claims[r.PayerClaimNumber]
	if len(c.lines) != 2 || !c.submitted {
		t.Fatalf("claim %+v", c)
	}
	lab := c.lines["LAB001"]
	if lab["QTY"] != "2" || lab["UNITPRICE"] != "20.00" || lab["ICDCODE"] != "B54,A03" || lab["ITEMNAME"] != "FBC" {
		t.Errorf("grouped line %v (unit price in cedis, primary diagnosis first)", lab)
	}
	sub := f.of("SUBMITCLAIM")[0]
	if sub["ICDCODE"] != "B54,A03" || sub["TYPEOFVISIT"] != "Out-Patient" || sub["DATEOFADMISSION"] != "" {
		t.Errorf("SUBMITCLAIM %v", sub)
	}
}

func TestInpatientIsGroupedAndCarriesItsDates(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	c := sampleClaim()
	c.Encounter.Type = claim.Inpatient
	d := claim.NewDate(2026, 10, 23)
	c.Encounter.DischargeDate = &d
	r, err := send(t, x, c, &engine.Progress{})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.claims[r.PayerClaimNumber].lines) != 2 {
		t.Errorf("one line per item code, inpatient too")
	}
	sub := f.of("SUBMITCLAIM")[0]
	if sub["TYPEOFVISIT"] != "In-Patient" || sub["DATEOFADMISSION"] != "2026-10-20" || sub["DATEOFDISCHARGE"] != "2026-10-23" {
		t.Errorf("inpatient SUBMITCLAIM %v", sub)
	}
}

func TestExpiredClaimIsOpenedAgainWithANewHMSNumber(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	f.expireNext = true
	var p engine.Progress
	r, err := send(t, x, sampleClaim(), &p)
	if err != nil || !r.Acknowledged {
		t.Fatalf("renewal: %+v %v", r, err)
	}
	opens := f.of("PROCESSCLAIMNO")
	if len(opens) != 2 || opens[1]["HMSNO"] != "11802390-R1" || p.Renewals != 1 {
		t.Errorf("opens %v renewals %d", opens, p.Renewals)
	}
	if r.PayerClaimNumber == "RX00493000000001P523" || !f.claims[r.PayerClaimNumber].submitted {
		t.Errorf("the new claim carries the submission: %s", r.PayerClaimNumber)
	}
}

func TestResubmitAndReopenAreIdempotent(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, x, c, &p); err != nil {
		t.Fatal(err)
	}
	if r, err := x.FinalizeClaim(context.Background(), engine.Build(c, manifest.Submission), p.PayerClaimNumber); err != nil || !r.Acknowledged {
		t.Errorf("013 is submitted: %+v %v", r, err)
	}
	// A lost PROCESSCLAIMNO reply: asked again, RX gives the same claim.
	n, err := x.OpenClaim(context.Background(), engine.Build(c, manifest.Submission))
	if err != nil || n != p.PayerClaimNumber || len(f.claims) != 1 {
		t.Errorf("reopen gave %s %v", n, err)
	}
}

func TestLineRefusalsAndWarnings(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	f.noBenefit["LAB001"] = true
	r, err := send(t, x, sampleClaim(), &engine.Progress{})
	if err != nil || len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "no benefit") {
		t.Errorf("no benefit is a warning: %+v %v", r, err)
	}

	f2, srv2 := newFake(t)
	x2 := open(t, srv2)
	f2.needsAuth["ADM04415"] = true
	_, err = send(t, x2, sampleClaim(), &engine.Progress{})
	if ce := connector.AsError(err); ce.Code != connector.AuthorizationRequired || !strings.Contains(ce.UserMessage, "Claims Authorization") {
		t.Errorf("authorization: %v", err)
	}

	c := sampleClaim()
	c.Lines[0].Mapping.Target.Value = "BOGUS"
	_, err = send(t, x2, c, &engine.Progress{})
	if ce := connector.AsError(err); ce.Code != connector.CodeNotRecognised || !ce.Final() {
		t.Errorf("unknown item: %v", err)
	}
}

func TestCorrectionAndLostLineReply(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	f.needsAuth["ADM04415"] = true
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, x, c, &p); err == nil {
		t.Fatal("want the authorization refusal")
	}
	f.needsAuth["ADM04415"] = false
	c.Lines = c.Lines[:2]
	r, err := send(t, x, c, &p)
	if err != nil {
		t.Fatal(err)
	}
	held := f.claims[r.PayerClaimNumber]
	if len(held.lines) != 2 || held.lines["LAB001"]["QTY"] != "1" || len(f.claims) != 1 {
		t.Errorf("same claim, corrected: %v", held.lines)
	}
}

func TestMembers(t *testing.T) {
	_, srv := newFake(t)
	x := open(t, srv)
	ctx := context.Background()
	v, err := x.VerifyMember(ctx, "RXH0005462-2", "")
	if err != nil || !v.Eligible || !v.OTPRequired || v.Member.FirstName != "Kwame" || v.PlanName != "Corporate Gold" ||
		v.CoverEnds.String() != "2026-12-31" || v.Member.BirthDate.String() != "1985-01-15" {
		t.Errorf("member %+v %v", v, err)
	}
	if v, _ := x.VerifyMember(ctx, "PLAN-0", ""); v.Eligible || v.Reason != "plan_inactive" {
		t.Errorf("suspended plan %+v", v)
	}
	if _, err := x.VerifyMember(ctx, "NOPE", ""); connector.AsError(err).Code != connector.MemberNotFound {
		t.Errorf("missing %v", err)
	}
	if _, err := x.SendOTP(ctx, "RXH0005462-2"); err != nil {
		t.Errorf("sms %v", err)
	}
	c := sampleClaim()
	c.OTP.Code = "0000"
	if _, err := x.OpenClaim(ctx, engine.Build(c, manifest.Submission)); connector.AsError(err).Code != connector.OTPRequired {
		t.Errorf("wrong pin: %v", err)
	}
}

func TestBadKeyAndCatalogue(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	a.Credentials["api_key"] = "wrong"
	c, _ := connector.New(Key, a)
	if _, err := c.(*RX).WithClient(srv.Client()).VerifyMember(context.Background(), "X", ""); connector.AsError(err).Code != connector.InvalidCredentials {
		t.Errorf("bad key: %v", err)
	}
	x := open(t, srv)
	items, err := x.ListCatalog(context.Background(), connector.CatalogItems)
	if err != nil || len(items) != 2 || items[0].Code != "ADM04415" || *items[0].Price != 12000 || items[1].Code != "LAB001" {
		t.Errorf("items %+v %v", items, err)
	}
	dx, err := x.ListCatalog(context.Background(), connector.CatalogDiagnoses)
	if err != nil || len(dx) != 1 || dx[0].Name != "Malaria" {
		t.Errorf("diagnoses %+v %v", dx, err)
	}
}
