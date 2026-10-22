package xceli

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

// fake is an XCeli auth server and insurer API server in one, built from the
// request and reply shapes core-api-v2's xceli.py sends and reads.
type fake struct {
	t  *testing.T
	mu sync.Mutex

	signIns    int
	expireNext bool // answer the next insurer call with 401
	calls      []string
	bodies     map[string][]map[string]any

	nextID    int
	claims    map[string]*fakeClaim
	submitErr string
}

type fakeClaim struct {
	member    string
	lines     map[int]map[string]any
	diagnoses map[int]map[string]any
	confirmed map[int]bool
	submitted bool
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	f := &fake{t: t, bodies: map[string][]map[string]any{}, claims: map[string]*fakeClaim{}, nextID: 1000}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	f.calls = append(f.calls, r.Method+" "+path)
	var body map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	f.bodies[path] = append(f.bodies[path], body)
	reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	ok := func(data any) {
		// ReturnValue is a status code, the id is in ReturnData (xceli.py).
		reply(map[string]any{"IsActionSuccessful": true, "ActionMessage": "Successful", "ReturnValue": 0, "ReturnData": data})
	}
	fail := func(msg string) {
		reply(map[string]any{"IsActionSuccessful": false, "ActionMessage": msg, "ReturnValue": 1})
	}

	if path == "/api/hspaccount/signin" {
		f.signIns++
		if body["UserName"] != "clinic" || body["Password"] != "s3cret" {
			reply(map[string]any{"IsAuthSuccessful": false, "ErrorMessage": "Invalid credentials"})
			return
		}
		reply(map[string]any{
			"IsAuthSuccessful": true, "Token": fmt.Sprintf("jwt-%d", f.signIns), "Expiration": "2099-01-01T00:00:00Z",
			"userDTO": map[string]any{"Id": 77, "Email": "clinic@example.com",
				"Insurers": []any{map[string]any{"CompanyName": "GLICO Healthcare", "APIUrl": "https://glico.example"}}},
		})
		return
	}
	if r.Header.Get("Authorization") != fmt.Sprintf("Bearer jwt-%d", f.signIns) || f.expireNext {
		f.expireNext = false
		http.Error(w, "token expired", http.StatusUnauthorized)
		return
	}

	switch {
	case strings.HasPrefix(path, "/api/member/ihsp/"):
		parts := strings.Split(path, "/")
		member, provider := parts[4], parts[5]
		if provider != "269" {
			f.t.Errorf("member lookup sent provider %s", provider)
		}
		switch member {
		case "NOPE":
			http.Error(w, "Member not found", http.StatusNotFound)
		case "OTP-1":
			reply(map[string]any{"Status": "Active", "MembershipNo": "OTP-1", "FirstName": "Kofi", "Surname": "Asante",
				"RequireOTP": true, "PlanName": "Gold", "PlanId": 12})
		case "OUT-1":
			reply(map[string]any{"Status": "Active", "MembershipNo": "OUT-1", "IsPlanValidForHSP": false})
		case "OLD-1":
			reply(map[string]any{"Status": "Inactive", "MembershipNo": "OLD-1"})
		default:
			reply(map[string]any{"Status": "Active", "MembershipNo": member, "FirstName": "Ama", "Surname": "Mensah",
				"Dob": "1990-04-02T00:00:00", "Gender": "Female", "PlanName": "Gold", "PlanId": 12,
				"ExpiryDate": "2026-12-31T00:00:00", "IsPlanValidForHSP": nil, "RequireOTP": false})
		}
	case path == "/api/utilities/generateotp":
		ok(nil)
	case path == "/api/utilities/validateotp":
		if body["OTP"] != "4321" {
			fail("Invalid OTP")
			return
		}
		ok(nil)
	case path == "/api/claim/createihspclaim":
		number := fmt.Sprintf("XS9562604%05d", len(f.claims)+1)
		f.claims[number] = &fakeClaim{member: body["MembershipNo"].(string), lines: map[int]map[string]any{},
			diagnoses: map[int]map[string]any{}, confirmed: map[int]bool{}}
		ok(number)
	case path == "/api/claim/createhspclaimdetails":
		c := f.claims[body["ClaimsNo"].(string)]
		if c == nil || c.submitted {
			fail("Claim has already been submitted")
			return
		}
		if body["ProductId"].(float64) == 999 {
			fail("Product not found on tariff")
			return
		}
		f.nextID++
		c.lines[f.nextID] = body
		ok(f.nextID)
	case path == "/api/claim/updatehspclaimdetails":
		c := f.claims[body["ClaimsNo"].(string)]
		id := int(body["ClaimsDetailsId"].(float64))
		if _, exists := c.lines[id]; !exists {
			fail("Claim detail not found")
			return
		}
		c.lines[id] = body
		ok(id)
	case strings.HasPrefix(path, "/api/claim/deletehspclaimdetails/"):
		var id int
		fmt.Sscanf(path, "/api/claim/deletehspclaimdetails/%d", &id)
		for _, c := range f.claims {
			delete(c.lines, id)
		}
		w.WriteHeader(http.StatusNoContent)
	case path == "/api/claim/createihspclaimdisease":
		c := f.claims[body["ClaimsNo"].(string)]
		f.nextID++
		c.diagnoses[f.nextID] = body
		ok(f.nextID)
	case path == "/api/claim/setdiagnosisconfirmstatus":
		id := int(body["RowID"].(float64))
		for _, c := range f.claims {
			if _, ok := c.diagnoses[id]; ok {
				c.confirmed[id] = body["Status"].(float64) == 0
			}
		}
		ok(nil)
	case strings.HasPrefix(path, "/api/claim/deletehspclaimdisease/"):
		var id int
		fmt.Sscanf(path, "/api/claim/deletehspclaimdisease/%d", &id)
		for _, c := range f.claims {
			delete(c.diagnoses, id)
		}
		ok(nil)
	case path == "/api/claim/submitclaim":
		c := f.claims[body["ClaimsNo"].(string)]
		switch {
		case f.submitErr != "":
			fail(f.submitErr)
		case c.submitted:
			fail("Claim has already been submitted")
		default:
			for id := range c.diagnoses {
				if !c.confirmed[id] {
					fail("Please add a confirmed diagnosis")
					return
				}
			}
			c.submitted = true
			ok(nil)
		}
	case strings.HasPrefix(path, "/api/claim/gethspclaimdetails/"):
		c := f.claims[strings.TrimPrefix(path, "/api/claim/gethspclaimdetails/")]
		var out []map[string]any
		for id, l := range c.lines {
			out = append(out, map[string]any{"Id": id, "ProductId": l["ProductId"], "Qty": l["Qty"],
				"UnitPrice": l["Claimed"], "TariffName": "x"})
		}
		reply(out)
	case path == "/api/tariff/getallproducts":
		reply([]any{map[string]any{"ProductId": 722, "ProductName": "Consultation - GP", "TypeName": "Service"},
			map[string]any{"ProductId": 723, "ProductName": "Malaria RDT"}, map[string]any{"ProductName": "no id"}})
	case path == "/api/disease/getalldiseases":
		reply(map[string]any{"Diseases": []any{map[string]any{"Code": "B54", "Name": "Malaria, unspecified"}}})
	default:
		http.NotFound(w, r)
	}
}

func account(srv *httptest.Server) connector.Account {
	return connector.Account{
		Credentials: map[string]string{"auth_base_url": srv.URL, "username": "clinic", "password": "s3cret",
			"provider_id": "269", "provider_name": "Ridge Clinic"},
		PayerID:     "glico",
		PayerFields: map[string]string{"insurer_api_url": srv.URL + "/"},
	}
}

func open(t *testing.T, srv *httptest.Server) *XCeli {
	t.Helper()
	httpx.SharedTokens.Drop(httpx.Key(Key, srv.URL, "clinic", "s3cret"))
	c, err := connector.New(Key, account(srv))
	if err != nil {
		t.Fatal(err)
	}
	return c.(*XCeli).WithClient(srv.Client())
}

func sampleClaim() *claim.Claim {
	payer := func(code string) *claim.CodeMapping {
		return &claim.CodeMapping{Target: &claim.Code{System: "payer:glico", Value: code}}
	}
	return &claim.Claim{ID: "clm_1", Input: claim.Input{
		PayerID: "glico", Currency: "GHS",
		Member:    claim.Member{MemberNumber: "EQ25834301945-0"},
		Provider:  claim.Provider{Name: "Ridge Clinic"},
		Encounter: claim.Encounter{Type: claim.Outpatient, StartDate: claim.NewDate(2026, 10, 20)},
		Lines: []claim.Line{
			{ID: "l1", Description: "Consultation", Quantity: 1, UnitPrice: 15000, Mapping: payer("722")},
			{ID: "l2", Description: "Malaria RDT", Quantity: 1, UnitPrice: 4000, Mapping: payer("723")},
			{ID: "l3", Description: "Malaria RDT repeat", Quantity: 1, UnitPrice: 5000, Mapping: payer("723")},
		},
		Diagnoses: []claim.Diagnosis{{Code: claim.Code{System: "icd-10", Value: "B54"}, Role: claim.Primary,
			Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "6165"}}}},
	}}
}

func send(t *testing.T, x *XCeli, c *claim.Claim, p *engine.Progress) (connector.Receipt, error) {
	t.Helper()
	m := x.Manifest()
	return engine.Send(context.Background(), x, engine.Build(c, m.Submission), engine.ChoosePlan(m, m.Submission), p,
		func(context.Context, *engine.Progress) error { return nil })
}

func TestClaimGoesThroughEveryStep(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	var p engine.Progress
	r, err := send(t, x, sampleClaim(), &p)
	if err != nil || !r.Acknowledged || r.PayerClaimNumber != "XS956260400001" {
		t.Fatalf("receipt %+v err %v", r, err)
	}
	if f.signIns != 1 {
		t.Errorf("one sign-in for the whole claim, got %d", f.signIns)
	}
	open := f.bodies["/api/claim/createihspclaim"][0]
	if open["MembershipNo"] != "EQ25834301945-0" || open["DateOfAttendance"] != "2026-10-20" ||
		open["InOutID"] != 1.0 || open["UserID"] != 77.0 || open["ProviderID"] != 269.0 {
		t.Errorf("createihspclaim body %v", open)
	}
	lines := f.bodies["/api/claim/createhspclaimdetails"]
	if len(lines) != 2 {
		t.Fatalf("grouped by product: want 2 lines, got %v", lines)
	}
	rdt := lines[1]
	if rdt["ProductId"] != 723.0 || rdt["Qty"] != 2.0 || rdt["Claimed"] != 45.0 || rdt["UserID"] != 77.0 {
		t.Errorf("grouped RDT line %v (Claimed is the unit price in cedis)", rdt)
	}
	dx := f.bodies["/api/claim/createihspclaimdisease"][0]
	if dx["ICDCode"] != "B54" || dx["DiseaseID"] != 6165.0 || dx["UserID"] != 77.0 {
		t.Errorf("diagnosis body %v", dx)
	}
	if len(f.bodies["/api/claim/setdiagnosisconfirmstatus"]) != 1 {
		t.Errorf("the diagnosis must be confirmed before submitclaim")
	}
	sub := f.bodies["/api/claim/submitclaim"][0]
	if sub["ClaimsNo"] != r.PayerClaimNumber || sub["DischargeDate"] != "2026-10-20" || sub["ActualDateOfAttendance"] != "2026-10-20" {
		t.Errorf("submitclaim body %v", sub)
	}
	// Line ids came from ReturnData, never from ReturnValue (which is 0).
	for key, sent := range p.Lines {
		if sent.Ref == "" || sent.Ref == "0" {
			t.Errorf("line %s recorded ref %q", key, sent.Ref)
		}
	}
}

func TestInpatientAndDischarge(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	c := sampleClaim()
	c.Encounter.Type = claim.Inpatient
	d := claim.NewDate(2026, 10, 23)
	c.Encounter.DischargeDate = &d
	if _, err := send(t, x, c, &engine.Progress{}); err != nil {
		t.Fatal(err)
	}
	if got := f.bodies["/api/claim/createihspclaim"][0]["InOutID"]; got != 2.0 {
		t.Errorf("inpatient InOutID = %v", got)
	}
	if len(f.bodies["/api/claim/createhspclaimdetails"]) != 3 {
		t.Errorf("inpatient lines are sent individually")
	}
	if got := f.bodies["/api/claim/submitclaim"][0]["DischargeDate"]; got != "2026-10-23" {
		t.Errorf("discharge date = %v", got)
	}
}

func TestExpiredTokenSignsInAgainOnce(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	if _, err := x.VerifyMember(context.Background(), "M-1", ""); err != nil {
		t.Fatal(err)
	}
	f.expireNext = true
	v, err := x.VerifyMember(context.Background(), "M-1", "")
	if err != nil || !v.Eligible {
		t.Fatalf("after expiry: %+v %v", v, err)
	}
	if f.signIns != 2 {
		t.Errorf("want a second sign-in after the 401, got %d", f.signIns)
	}
}

func TestBadPasswordIsInvalidCredentials(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	a.Credentials["password"] = "wrong"
	httpx.SharedTokens.Drop(httpx.Key(Key, srv.URL, "clinic", "wrong"))
	c, _ := connector.New(Key, a)
	_, err := c.(*XCeli).WithClient(srv.Client()).VerifyMember(context.Background(), "M-1", "")
	if connector.AsError(err).Code != connector.InvalidCredentials {
		t.Errorf("got %v", err)
	}
}

func TestMemberChecks(t *testing.T) {
	_, srv := newFake(t)
	x := open(t, srv)
	ctx := context.Background()

	v, err := x.VerifyMember(ctx, "EQ1", "")
	if err != nil || !v.Eligible || v.Member.FirstName != "Ama" || v.PlanName != "Gold" || v.PlanID != "12" ||
		v.CoverEnds.String() != "2026-12-31" || v.Member.BirthDate.String() != "1990-04-02" {
		t.Errorf("active member: %+v %v", v, err)
	}
	if _, err := x.VerifyMember(ctx, "NOPE", ""); connector.AsError(err).Code != connector.MemberNotFound {
		t.Errorf("missing member: %v", err)
	}
	if v, _ := x.VerifyMember(ctx, "OUT-1", ""); v.Eligible || v.Reason != "not_covered_here" {
		t.Errorf("plan not valid here must not be eligible: %+v", v)
	}
	if v, _ := x.VerifyMember(ctx, "OLD-1", ""); v.Eligible || v.Reason != "inactive" {
		t.Errorf("inactive: %+v", v)
	}
	if v, _ := x.VerifyMember(ctx, "OTP-1", ""); !v.Eligible || !v.OTPRequired {
		t.Errorf("otp member: %+v", v)
	}
	if err := x.SendOTP(ctx, "OTP-1"); err != nil {
		t.Errorf("send otp: %v", err)
	}
	if _, err := x.VerifyMember(ctx, "OTP-1", "0000"); connector.AsError(err).Code != connector.OTPRequired {
		t.Errorf("wrong otp: %v", err)
	}
	if v, err := x.VerifyMember(ctx, "OTP-1", "4321"); err != nil || v.OTPRequired {
		t.Errorf("right otp: %+v %v", v, err)
	}
}

func TestUnknownProductIsCodeNotRecognised(t *testing.T) {
	_, srv := newFake(t)
	x := open(t, srv)
	c := sampleClaim()
	c.Lines[0].Mapping.Target.Value = "999"
	_, err := send(t, x, c, &engine.Progress{})
	if ce := connector.AsError(err); ce.Code != connector.CodeNotRecognised || !ce.Final() {
		t.Errorf("got %v", err)
	}
	c.Lines[0].Mapping.Target.Value = "CONS-01"
	if _, err := send(t, x, c, &engine.Progress{}); connector.AsError(err).Code != connector.CodeNotRecognised {
		t.Errorf("a non-numeric product id is refused before calling XCeli: %v", err)
	}
}

// lossy loses the reply of the first line add after XCeli stored it.
type lossy struct {
	*XCeli
	lost bool
}

func (l *lossy) AddLine(ctx context.Context, s *connector.Submission, n string, line connector.SendLine) (string, error) {
	ref, err := l.XCeli.AddLine(ctx, s, n, line)
	if err == nil && !l.lost {
		l.lost = true
		return "", connector.Errorf(connector.Timeout, "reply lost")
	}
	return ref, err
}

func TestLostLineReplyIsAdoptedOnRetry(t *testing.T) {
	f, srv := newFake(t)
	x := &lossy{XCeli: open(t, srv)}
	c := sampleClaim()
	m := x.Manifest()
	s := engine.Build(c, m.Submission)
	var p engine.Progress
	nop := func(context.Context, *engine.Progress) error { return nil }
	if _, err := engine.Send(context.Background(), x, s, engine.ChoosePlan(m, m.Submission), &p, nop); err == nil {
		t.Fatal("want the lost reply")
	}
	r, err := engine.Send(context.Background(), x, s, engine.ChoosePlan(m, m.Submission), &p, nop)
	if err != nil || !r.Acknowledged {
		t.Fatalf("retry: %+v %v", r, err)
	}
	if n := len(f.claims[r.PayerClaimNumber].lines); n != 2 {
		t.Errorf("XCeli should hold 2 lines after the retry, holds %d", n)
	}
	if len(f.bodies["/api/claim/createihspclaim"]) != 1 {
		t.Errorf("the claim must be opened once")
	}
}

func TestCorrectionAfterRefusalUpdatesTheSameClaim(t *testing.T) {
	f, srv := newFake(t)
	x := open(t, srv)
	c := sampleClaim()
	var p engine.Progress
	f.submitErr = "Benefit limit exceeded"
	_, err := send(t, x, c, &p)
	if ce := connector.AsError(err); ce.Code != connector.PayerRefused || ce.PayerMessage != "Benefit limit exceeded" {
		t.Fatalf("want the refusal with XCeli's words, got %v", err)
	}
	f.submitErr = ""
	c.Lines = c.Lines[:1] // the biller drops the RDT lines
	c.Lines[0].Quantity = 2
	r, err := send(t, x, c, &p)
	if err != nil {
		t.Fatal(err)
	}
	held := f.claims[r.PayerClaimNumber]
	if len(held.lines) != 1 || len(f.bodies["/api/claim/createihspclaim"]) != 1 {
		t.Errorf("the same claim should now hold one line: %+v", held.lines)
	}
	for _, l := range held.lines {
		if l["Qty"] != 2.0 {
			t.Errorf("updated line %v", l)
		}
	}
}

func TestResendAfterLostSubmitReplyIsAcknowledged(t *testing.T) {
	_, srv := newFake(t)
	x := open(t, srv)
	c := sampleClaim()
	var p engine.Progress
	if _, err := send(t, x, c, &p); err != nil {
		t.Fatal(err)
	}
	// The submit reached XCeli but we never heard back; the worker runs again.
	r, err := x.FinalizeClaim(context.Background(), engine.Build(c, x.Manifest().Submission), p.PayerClaimNumber)
	if err != nil || !r.Acknowledged {
		t.Errorf("already submitted should count as acknowledged: %+v %v", r, err)
	}
}

func TestCatalogue(t *testing.T) {
	_, srv := newFake(t)
	x := open(t, srv)
	items, err := x.ListCatalog(context.Background(), connector.CatalogItems)
	if err != nil || len(items) != 2 || items[0].Code != "722" || items[0].Group != "Service" {
		t.Errorf("items %+v %v", items, err)
	}
	dx, err := x.ListCatalog(context.Background(), connector.CatalogDiagnoses)
	if err != nil || len(dx) != 1 || dx[0].Code != "B54" {
		t.Errorf("diagnoses %+v %v", dx, err)
	}
}

func TestAccountValidation(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	a.Credentials["provider_id"] = "abc"
	if _, err := connector.New(Key, a); connector.AsError(err).Code != connector.InvalidCredentials {
		t.Errorf("non-numeric provider id: %v", err)
	}
	a = account(srv)
	delete(a.PayerFields, "insurer_api_url")
	if _, err := connector.New(Key, a); err == nil {
		t.Errorf("an insurer without its API URL must not build")
	}
	a = account(srv)
	delete(a.Credentials, "auth_base_url")
	c, err := connector.New(Key, a)
	if err != nil || c.(*XCeli).authURL != "https://xcelihealthapi.xcelisolutions.com" {
		t.Errorf("default sign-in server: %v", err)
	}
}
