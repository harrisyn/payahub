// Package medserve is the Medserve (Posh POS) connector, ported from
// serenity-health/core-api-v2 backend/app/plugins/insurance/medserve.py.
//
// Payer quirks that code documents, kept here:
//   - Every call goes to one endpoint, {base}/{insurer code}/pos/, with the
//     operation in an "op" field and the session's connid beside it. Writes
//     are form posts; reads are query strings; finalising a batch is a PUT.
//   - Sign-in (op=login, midtype=host, mid=host) returns a connid; it is
//     renewed after 25 minutes.
//   - A claim is a prescription batch (p.sba) opened for the beneficiary's
//     benId, class 1 outpatient or 2 inpatient. When the insurer requires
//     an OTP, the batch carries bauth (the member's code) and pauth (the
//     challenge p.bauth returned when the code was sent).
//   - A line is two calls: p.ap adds a prescription row (its catid comes
//     from the product's coverage string found by p.prodsearchw for the
//     member's package), then p.ad dispenses it with the line's total cost.
//     Changing a line is p.ad again with update=1; Medserve has no delete,
//     so a removed line is dispensed with quantity 0.
//   - The member's package id (pkgid) is the number before the "/" in the
//     plan string ("132/basic"); anything else is refused as wrong format.
//   - icdcode is an integer column: only Medserve's numeric diagnosis ids go
//     in it, never ICD-10 strings. Diagnoses travel on the lines; there is
//     no diagnosis call.
//   - p.ad may answer charge_patient=1: the insurer's cover is exhausted and
//     the shortfall is the patient's. The line cannot be claimed; an update
//     is put back to what it was before.
//   - p.ad may answer with approvals set: the line needs the insurer's
//     approval (p.adapreq), and the batch waits until p.qdapreq says approved.
//   - Before p.finalb closes a batch, every dispensation must be claimed
//     with p.bulkfind; "already finalised" on a resend means it was.
package medserve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/httpx"
)

const Key = "medserve"

const name = "Medserve"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    name,
	Kind:    connector.KindPlatform,
	Country: "GH",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.OpenClaim, connector.AddLine,
		connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis, connector.DeleteDiagnosis,
		connector.FinalizeClaim, connector.FetchClaim,
	},
	MemberOTPRequired: true,
	Credentials: []connector.CredentialField{
		{Name: "base_url", Label: "Medserve server", Required: true},
		{Name: "username", Label: "POS username", Required: true},
		{Name: "password", Label: "POS password", Secret: true, Required: true},
		{Name: "exsys", Label: "External system code", Default: "1"},
	},
	PayerFields: []connector.CredentialField{
		{Name: "insurer_code", Label: "Insurer code (the path segment, e.g. ghi)", Required: true},
		{Name: "insurer_id", Label: "Numeric insurer id"},
	},
	Submission: func() connector.SubmissionConfig {
		c := connector.DefaultSubmissionConfig()
		// Diagnoses ride on each line as Medserve's numeric ids.
		c.DiagnosisCodeSource = "payer_code"
		return c
	}(),
	Status: connector.StatusAvailable,
}

func init() { connector.Register(manifest, New) }

type Medserve struct {
	connector.Unsupported
	posURL             string
	username, password string
	exsys, insurerID   string
	tokenKey           string
	client             *http.Client

	member       *beneficiary // looked up once per claim
	memberNumber string
}

func New(a connector.Account) (connector.Connector, error) {
	creds := a.Credentials
	base := strings.TrimRight(creds["base_url"], "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, connector.Errorf(connector.InvalidCredentials, "Medserve server %q is not a URL", base)
	}
	code := strings.Trim(a.PayerFields["insurer_code"], "/ ")
	m := &Medserve{
		Unsupported: connector.Unsupported{Name: name},
		posURL:      base + "/" + url.PathEscape(code) + "/pos/",
		username:    creds["username"],
		password:    creds["password"],
		exsys:       manifest.Credentials[3].Value(creds),
		insurerID:   a.PayerFields["insurer_id"],
		client:      httpx.DefaultClient,
	}
	if m.insurerID == "" && isDigits(code) {
		m.insurerID = code
	}
	m.tokenKey = httpx.Key(Key, m.posURL, m.username, m.password)
	return m, nil
}

func (m *Medserve) WithClient(c *http.Client) *Medserve { m.client = c; return m }

func (m *Medserve) Manifest() connector.Manifest { return manifest }

// ---- transport ----

// reply is the Posh envelope: {"error": false | {...}, "result": {"status", "value"}}.
type reply struct {
	Error  json.RawMessage `json:"error"`
	Result struct {
		Status *int            `json:"status"`
		Value  json.RawMessage `json:"value"`
	} `json:"result"`
}

func (r reply) failure() string {
	e := strings.TrimSpace(string(r.Error))
	if e != "" && e != "false" && e != "null" && e != "{}" {
		var obj map[string]any
		if json.Unmarshal(r.Error, &obj) == nil {
			for _, k := range []string{"message", "msg", "detail", "errorMessage", "error_message"} {
				if v, ok := obj[k].(string); ok && v != "" {
					return v
				}
			}
		}
		var str string
		if json.Unmarshal(r.Error, &str) == nil && str != "" {
			return str
		}
		return e
	}
	if r.Result.Status != nil && *r.Result.Status != 200 {
		return fmt.Sprintf("status %d", *r.Result.Status)
	}
	return ""
}

func (m *Medserve) session(ctx context.Context) (httpx.Token, error) {
	return httpx.SharedTokens.Get(ctx, m.tokenKey, func(ctx context.Context) (httpx.Token, error) {
		var r reply
		form := url.Values{"op": {"login"}, "user": {m.username}, "pass": {m.password}, "midtype": {"host"}, "mid": {"host"}}
		if _, err := httpx.Form(ctx, m.client, http.MethodPost, m.posURL, nil, form, &r); err != nil {
			if s := httpx.Status(err); s != 0 && s < 500 {
				return httpx.Token{}, &connector.Error{Code: connector.InvalidCredentials, Message: err.Error(), Cause: err,
					UserMessage: "Medserve did not accept the POS username and password."}
			}
			return httpx.Token{}, httpx.Translate(name, err)
		}
		var rows []struct {
			ConnID string `json:"connId"`
		}
		_ = json.Unmarshal(r.Result.Value, &rows)
		if msg := r.failure(); msg != "" || len(rows) == 0 || rows[0].ConnID == "" {
			return httpx.Token{}, &connector.Error{Code: connector.InvalidCredentials, Message: "Medserve sign-in failed: " + msg,
				UserMessage: "Medserve did not accept the POS username and password.", PayerMessage: msg}
		}
		return httpx.Token{Value: rows[0].ConnID, Expires: time.Now().Add(25 * time.Minute)}, nil
	})
}

var reSession = regexp.MustCompile(`(?i)conn(ection)?\s*id|session|not\s+logged|log\s*in\s+(first|required)`)

// call runs one operation. GET and DELETE send fields as a query string,
// POST and PUT as a form. out receives the decoded reply (a reply struct, or
// a raw list for the few operations that answer with one).
func (m *Medserve) call(ctx context.Context, method, op string, fields url.Values, out any) error {
	for attempt := 0; ; attempt++ {
		tok, err := m.session(ctx)
		if err != nil {
			return err
		}
		v := url.Values{"op": {op}, "connid": {tok.Value}}
		for k, vs := range fields {
			v[k] = vs
		}
		var raw []byte
		switch method {
		case http.MethodGet, http.MethodDelete:
			raw, err = httpx.Do(ctx, m.client, httpx.Request{Method: method, URL: m.posURL + "?" + v.Encode()})
			if err == nil && out != nil && len(strings.TrimSpace(string(raw))) > 0 {
				if jerr := json.Unmarshal(raw, out); jerr != nil {
					err = &connector.Error{Code: connector.Unknown, Message: "Medserve " + op + " reply is not JSON",
						UserMessage: "Medserve sent a reply we could not read.", PayerMessage: string(raw[:min(len(raw), 300)])}
				}
			}
		default:
			_, err = httpx.Form(ctx, m.client, method, m.posURL, nil, v, out)
		}
		if err != nil {
			if httpx.Status(err) == http.StatusUnauthorized && attempt == 0 {
				httpx.SharedTokens.Drop(m.tokenKey)
				continue
			}
			return httpx.Translate(name, err)
		}
		if r, ok := out.(*reply); ok && attempt == 0 && reSession.MatchString(r.failure()) {
			httpx.SharedTokens.Drop(m.tokenKey)
			*r = reply{}
			continue
		}
		return nil
	}
}

// op runs an operation that answers with the envelope and checks it.
func (m *Medserve) op(ctx context.Context, method, op string, fields url.Values) (reply, error) {
	var r reply
	if err := m.call(ctx, method, op, fields, &r); err != nil {
		return r, err
	}
	if msg := r.failure(); msg != "" {
		return r, refused(op, msg)
	}
	return r, nil
}

var (
	reOTP        = regexp.MustCompile(`(?i)\botp\b|auth\s*code|bauth|pauth|authenticat`)
	reNotCovered = regexp.MustCompile(`(?i)not\s+covered|exclu|benefit|limit|exhaust`)
	reFinalised  = regexp.MustCompile(`(?i)already\s+finali[sz]`)
	reNotFound   = regexp.MustCompile(`(?i)not\s+found|no\s+(such|record|beneficiary|member)|does\s+not\s+exist`)
	reFormat     = regexp.MustCompile(`(?i)wrong\s+format|too\s+long|invalid`)
	reExists     = regexp.MustCompile(`(?i)already\s+(exists|added|dispensed)|duplicate`)
)

func refused(op, msg string) error {
	e := &connector.Error{Message: fmt.Sprintf("Medserve %s: %s", op, msg), PayerMessage: msg, UserMessage: "Medserve: " + msg}
	switch {
	case reFinalised.MatchString(msg):
		e.Code = connector.ClaimClosed
	case reOTP.MatchString(msg):
		e.Code = connector.OTPRequired
	case op == "p.qbid" && reNotFound.MatchString(msg):
		e.Code = connector.MemberNotFound
	case reNotCovered.MatchString(msg):
		e.Code = connector.PayerRefused
	case reExists.MatchString(msg):
		e.Code = connector.LineAlreadyAdded
	case reFormat.MatchString(msg):
		e.Code = connector.Validation
	default:
		e.Code = connector.PayerRefused
	}
	return e
}

// ---- members ----

type beneficiary struct {
	BenID      string `json:"benId"`
	ExtBenID   string `json:"extbenid"`
	PolicyNo   string `json:"policyNo"`
	Surname    string `json:"surname"`
	Lastname   string `json:"lastname"`
	Middlename string `json:"middlename"`
	Dob        string `json:"dob"`
	Sex        string `json:"sex"`
	Plan       string `json:"plan"`
	Expired    any    `json:"expired"`
	Status     string `json:"status"`
	ExpiryDate string `json:"expiryDate"`
	InsurerID  any    `json:"insurerid"`
	Phone      string `json:"phone"`
}

// pkgID is the number before "/" in the plan string ("132/basic" -> "132").
func (b beneficiary) pkgID() string {
	id := strings.TrimSpace(strings.SplitN(b.Plan, "/", 2)[0])
	if isDigits(id) && len(id) <= 10 {
		return id
	}
	return ""
}

func (b beneficiary) planName() string {
	parts := strings.SplitN(b.Plan, "/", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(b.Plan)
}

func (b beneficiary) active() bool {
	return scalar(b.Expired) == "0" && strings.EqualFold(b.Status, "active")
}

func (m *Medserve) lookup(ctx context.Context, memberNumber string) (beneficiary, error) {
	r, err := m.op(ctx, http.MethodGet, "p.qbid", url.Values{"benid": {memberNumber}})
	if err != nil {
		return beneficiary{}, err
	}
	var rows []beneficiary
	_ = json.Unmarshal(r.Result.Value, &rows)
	if len(rows) == 0 {
		return beneficiary{}, &connector.Error{Code: connector.MemberNotFound, Message: "Medserve has no beneficiary " + memberNumber,
			UserMessage: fmt.Sprintf("Member %q was not found at the insurer.", memberNumber)}
	}
	return rows[0], nil
}

// otpRequired reads the insurer's settings (op=qoinfo): SR.require-authcode "1".
func (m *Medserve) otpRequired(ctx context.Context, insurerID string) bool {
	if insurerID == "" {
		return false
	}
	var raw []json.RawMessage
	if err := m.call(ctx, http.MethodGet, "qoinfo", url.Values{"i": {insurerID}}, &raw); err != nil || len(raw) < 2 {
		return false // core-api-v2 treats a failed settings check as not required
	}
	var settings []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	}
	_ = json.Unmarshal(raw[1], &settings)
	for _, s := range settings {
		if s.Name == "SR.require-authcode" {
			return strings.TrimSpace(scalar(s.Value)) == "1"
		}
	}
	return false
}

func (m *Medserve) VerifyMember(ctx context.Context, memberNumber, _ string) (connector.MemberVerification, error) {
	b, err := m.lookup(ctx, memberNumber)
	if err != nil {
		return connector.MemberVerification{}, err
	}
	first, last := splitName(b.Surname, b.Lastname)
	v := connector.MemberVerification{
		Eligible: b.active(),
		PlanName: b.planName(),
		PlanID:   b.pkgID(),
		Member: claim.Member{
			MemberNumber: firstNonEmpty(b.ExtBenID, memberNumber),
			PolicyNumber: firstNonEmpty(b.PolicyNo, memberNumber),
			FirstName:    first, LastName: last,
			Gender: strings.ToLower(b.Sex), Phone: b.Phone,
		},
		Message: "Policy validated successfully",
	}
	if d, ok := parseDate(b.Dob); ok {
		v.Member.BirthDate = &d
	}
	if d, ok := parseDate(b.ExpiryDate); ok {
		v.CoverEnds = &d
	}
	if !v.Eligible {
		v.Reason, v.Message = "inactive", "Member status: EXPIRED"
	}
	v.OTPRequired = m.otpRequired(ctx, firstNonEmpty(scalar(b.InsurerID), m.insurerID))
	return v, nil
}

// SendOTP sends the member a code by phone, or by email when there is no
// usable phone (op=p.bauth takes one or the other), and returns the pauth
// challenge the batch must carry. An empty challenge means the provider is
// exempt from OTP for this insurer.
func (m *Medserve) SendOTP(ctx context.Context, memberNumber string) (string, error) {
	b, err := m.lookup(ctx, memberNumber)
	if err != nil {
		return "", err
	}
	contact := func(op, field string) string {
		var raw []json.RawMessage
		if m.call(ctx, http.MethodGet, op, url.Values{"b": {b.BenID}, "all": {"0"}}, &raw) != nil || len(raw) == 0 {
			return ""
		}
		var rows []map[string]any
		_ = json.Unmarshal(raw[0], &rows)
		for _, r := range rows {
			if v := scalar(r[field]); v != "" {
				return v
			}
		}
		return ""
	}
	form := url.Values{"benid": {b.BenID}}
	if tel := contact("qbphoneusable", "telno"); tel != "" {
		form.Set("tel", tel)
	} else if email := contact("qbeaddrusable", "email"); email != "" {
		form.Set("email", email)
	} else {
		return "", &connector.Error{Code: connector.Validation, Message: "Medserve has no usable phone or email for " + memberNumber,
			UserMessage: "The insurer has no phone number or email for this member, so no code can be sent."}
	}
	r, err := m.op(ctx, http.MethodPost, "p.bauth", form)
	if err != nil {
		return "", err
	}
	var v struct {
		Code any `json:"code"`
	}
	_ = json.Unmarshal(r.Result.Value, &v)
	return scalar(v.Code), nil
}

// ---- claims ----

// beneficiaryFor looks the claim's member up once per connector instance:
// the batch needs the benId and every line the package id.
func (m *Medserve) beneficiaryFor(ctx context.Context, c *claim.Claim) (beneficiary, error) {
	if m.member != nil && m.memberNumber == c.Member.MemberNumber {
		return *m.member, nil
	}
	b, err := m.lookup(ctx, c.Member.MemberNumber)
	if err != nil {
		return b, err
	}
	m.member, m.memberNumber = &b, c.Member.MemberNumber
	return b, nil
}

func class(c *claim.Claim) string {
	if c.Encounter.Type == claim.Inpatient {
		return "2"
	}
	return "1"
}

func (m *Medserve) OpenClaim(ctx context.Context, s *connector.Submission) (string, error) {
	c := s.Claim
	b, err := m.beneficiaryFor(ctx, c)
	if err != nil {
		return "", err
	}
	provider := c.Provider.Name
	form := url.Values{
		"id": {firstNonEmpty(b.BenID, c.Member.MemberNumber)}, "it": {"id"}, "d": {provider},
		"class": {class(c)}, "date": {c.Encounter.StartDate.String()}, "by": {provider}, "rem": {provider},
		"exsys": {m.exsys},
	}
	if c.OTP != nil && c.OTP.Code != "" {
		form.Set("bauth", c.OTP.Code)
		if c.OTP.Challenge != "" {
			form.Set("pauth", c.OTP.Challenge)
		}
	}
	r, err := m.op(ctx, http.MethodPost, "p.sba", form)
	if err != nil {
		return "", err
	}
	rec := firstRecord(r.Result.Value)
	number := firstNonEmpty(scalar(rec["batchcode"]), scalar(rec["batchCode"]), scalar(rec["id"]), scalar(rec["dispid"]))
	if number == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "Medserve opened a batch but sent no batch code",
			UserMessage: "Medserve did not return a batch number."}
	}
	return number, nil
}

// lineRef is what we keep of a Medserve line: the p.ap prescription row,
// the p.ad dispensation, the cost the insurer accepted (icost, minor
// units), the quantity and cost sent (to put an update back), and whether
// the line waits for the insurer's approval.
type lineRef struct {
	Pres, Disp    string
	ICost, Cost   int64
	Qty           int64
	NeedsApproval bool
}

func (r lineRef) String() string {
	a := "0"
	if r.NeedsApproval {
		a = "1"
	}
	return fmt.Sprintf("p=%s;d=%s;i=%d;c=%d;q=%d;a=%s", r.Pres, r.Disp, r.ICost, r.Cost, r.Qty, a)
}

// RefKey: a line is its prescription row.
func (m *Medserve) RefKey(ref string) string { return parseRef(ref).Pres }

func parseRef(s string) lineRef {
	var r lineRef
	for _, part := range strings.Split(s, ";") {
		k, v, _ := strings.Cut(part, "=")
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
		case "p":
			r.Pres = v
		case "d":
			r.Disp = v
		case "i":
			r.ICost = n
		case "c":
			r.Cost = n
		case "q":
			r.Qty = n
		case "a":
			r.NeedsApproval = v == "1"
		}
	}
	return r
}

// ourRef is our own tracking id for a line (presid, dispid): stable for a
// claim and line, so a resend is recognisable at Medserve.
func ourRef(c *claim.Claim, key string) string {
	sum := sha256.Sum256([]byte(c.ID + "\x00" + key))
	return "pa-" + hex.EncodeToString(sum[:8])
}

// diagnosisIDs are the claim's Medserve diagnosis ids, numbers only.
func diagnosisIDs(s *connector.Submission) string {
	var ids []string
	for _, d := range s.Diagnoses {
		for _, t := range strings.Split(d.PayerCode, ",") {
			if t = strings.TrimSpace(t); isDigits(t) {
				ids = append(ids, t)
			}
		}
	}
	return strings.Join(ids, ",")
}

func money(minor int64) string { return strconv.FormatFloat(float64(minor)/100, 'f', 2, 64) }

// category finds the line's coverage category (catid) for the member's
// package by searching Medserve's products for the line's item.
func (m *Medserve) category(ctx context.Context, pkg string, l connector.SendLine) (string, error) {
	var names []string
	for _, n := range []string{l.PayerName, l.Description} {
		if n = strings.TrimSpace(n); n != "" && (len(names) == 0 || names[0] != n) {
			names = append(names, n)
		}
	}
	for _, n := range names {
		r, err := m.op(ctx, http.MethodGet, "p.prodsearchw", url.Values{"n": {SearchString(n)}, "pkgid": {pkg}})
		if err != nil {
			return "", err
		}
		for _, p := range productRecords(r.Result.Value) {
			if strings.TrimSpace(scalar(p["code"])) != l.Code {
				continue
			}
			cat, covered := parseCoverage(scalar(p["coverage"]))
			if covered != nil && !*covered || cat == "" {
				return "", &connector.Error{Code: connector.PayerRefused,
					Message:     fmt.Sprintf("Medserve: product %s is not covered under the member's plan", l.Code),
					UserMessage: fmt.Sprintf("%q is not covered under the member's plan.", l.Description)}
			}
			return cat, nil
		}
	}
	return "", &connector.Error{Code: connector.CodeNotRecognised,
		Message:     fmt.Sprintf("Medserve product search found no product %s for %q", l.Code, names),
		UserMessage: fmt.Sprintf("Medserve does not list %q under code %s for this member's plan. Check the mapping.", l.Description, l.Code)}
}

func (m *Medserve) AddLine(ctx context.Context, s *connector.Submission, number string, l connector.SendLine) (string, error) {
	b, err := m.beneficiaryFor(ctx, s.Claim)
	if err != nil {
		return "", err
	}
	pkg := b.pkgID()
	if pkg == "" {
		return "", &connector.Error{Code: connector.Validation, Message: fmt.Sprintf("Medserve plan %q has no numeric package id", b.Plan),
			UserMessage: "Medserve returned no plan number for this member, so the line cannot be priced. Check the member's plan with the insurer."}
	}
	cat, err := m.category(ctx, pkg, l)
	if err != nil {
		return "", err
	}
	own := ourRef(s.Claim, l.Key)
	provider := s.Claim.Provider.Name
	r, err := m.op(ctx, http.MethodPost, "p.ap", url.Values{
		"id": {number}, "presid": {own}, "prod": {l.Code}, "name": {firstNonEmpty(l.PayerName, l.Description)},
		"qty": {strconv.FormatInt(l.Quantity, 10)}, "rem": {provider}, "no": {provider},
		"icdcode": {diagnosisIDs(s)}, "catid": {cat},
	})
	if ce, ok := err.(*connector.Error); ok && ce.Code == connector.LineAlreadyAdded {
		// The row is there from an earlier attempt: dispense it as an update.
		if existing := m.findRow(ctx, number, l.Code); existing != "" {
			return m.dispense(ctx, s, lineRef{Pres: existing}, l, pkg, true)
		}
	}
	if err != nil {
		return "", err
	}
	rec := firstRecord(r.Result.Value)
	pres := firstNonEmpty(scalar(rec["id"]), scalar(rec["prescriptionId"]))
	if !isDigits(pres) {
		return "", &connector.Error{Code: connector.Unknown, Message: "Medserve added a prescription row but sent no row id",
			UserMessage: "Medserve did not confirm the line."}
	}
	return m.dispense(ctx, s, lineRef{Pres: pres}, l, pkg, false)
}

// findRow returns the prescription row Medserve holds for a product on a batch.
func (m *Medserve) findRow(ctx context.Context, number, product string) string {
	pc, err := m.FetchClaim(ctx, number)
	if err != nil {
		return ""
	}
	for _, pl := range pc.Lines {
		if pl.Code == product {
			return parseRef(pl.Ref).Pres
		}
	}
	return ""
}

// dispense runs p.ad for a prescription row and returns the line's ref.
func (m *Medserve) dispense(ctx context.Context, s *connector.Submission, prev lineRef, l connector.SendLine, pkg string, update bool) (string, error) {
	own := ourRef(s.Claim, l.Key)
	form := url.Values{
		"id": {prev.Pres}, "exsys": {m.exsys}, "dispid": {own}, "pkgid": {pkg}, "prod": {l.Code},
		"name": {firstNonEmpty(l.PayerName, l.Description)}, "qty": {strconv.FormatInt(l.Quantity, 10)},
		"cost": {money(l.Amount)}, "icdcode": {diagnosisIDs(s)}, "date": {s.Claim.Encounter.StartDate.String()},
		"rem": {s.Claim.Provider.Name}, "update": {"0"},
	}
	if update {
		form.Set("update", "1")
	}
	r, err := m.op(ctx, http.MethodPost, "p.ad", form)
	if err != nil {
		return "", err
	}
	rec := firstRecord(r.Result.Value)
	ref := lineRef{
		Pres: prev.Pres, Disp: firstNonEmpty(scalar(rec["id"]), scalar(rec["dispid"]), scalar(rec["dispensationId"]), own),
		Cost: l.Amount, Qty: l.Quantity,
	}
	icost := minor(scalar(rec["icost"]))
	if icost <= 0 {
		icost = l.Amount
	}
	ref.ICost = icost

	// charge_patient: the insurer covers less than asked and the rest is the
	// patient's, so the line cannot be claimed as sent.
	bcost := max(minor(scalar(rec["bcost"])), minor(scalar(rec["totalbasecost"])))
	shortfall := max(bcost, l.Amount-max(minor(scalar(rec["icost"])), 0))
	if strings.TrimSpace(scalar(rec["charge_patient"])) == "1" && shortfall > 0 {
		if update && prev.Qty > 0 {
			back := form
			back.Set("qty", strconv.FormatInt(prev.Qty, 10))
			back.Set("cost", money(prev.Cost))
			_, _ = m.op(ctx, http.MethodPost, "p.ad", back)
		} else if !update {
			zero := form
			zero.Set("id", prev.Pres)
			zero.Set("qty", "0")
			zero.Set("cost", "0.00")
			zero.Set("update", "1")
			_, _ = m.op(ctx, http.MethodPost, "p.ad", zero)
		}
		remark := scalar(rec["remark"])
		msg := fmt.Sprintf("the insurer's cover is exhausted; the patient must pay %s", money(shortfall))
		if remark != "" {
			msg += " (" + remark + ")"
		}
		return "", &connector.Error{Code: connector.PayerRefused, Message: "Medserve p.ad: " + msg, PayerMessage: remark,
			UserMessage: fmt.Sprintf("%q: %s.", l.Description, msg)}
	}

	if approvals := strings.ToLower(strings.TrimSpace(scalar(rec["approvals"]))); approvals != "" &&
		approvals != "0" && approvals != "none" && approvals != "no" && approvals != "false" {
		ref.NeedsApproval = true
		_, aerr := m.op(ctx, http.MethodPost, "p.adapreq", url.Values{"id": {ref.Disp}})
		msg := "the line needs the insurer's approval before it can be claimed"
		if remark := scalar(rec["remark"]); remark != "" {
			msg += ": " + remark
		}
		if aerr != nil {
			msg += " (the approval request failed and will be retried: " + aerr.Error() + ")"
		}
		return ref.String(), &connector.Error{Code: connector.AwaitingPayer, Message: "Medserve: " + msg,
			UserMessage: fmt.Sprintf("%q: %s. We will check again.", l.Description, msg)}
	}
	return ref.String(), nil
}

func (m *Medserve) UpdateLine(ctx context.Context, s *connector.Submission, _ string, ref string, l connector.SendLine) (string, error) {
	b, err := m.beneficiaryFor(ctx, s.Claim)
	if err != nil {
		return "", err
	}
	prev := parseRef(ref)
	if !isDigits(prev.Pres) {
		return "", connector.Errorf(connector.LineNotFound, "no Medserve prescription row in %q", ref)
	}
	return m.dispense(ctx, s, prev, l, b.pkgID(), true)
}

// DeleteLine dispenses the line with quantity 0: Medserve has no delete.
func (m *Medserve) DeleteLine(ctx context.Context, s *connector.Submission, _ string, ref string) error {
	b, err := m.beneficiaryFor(ctx, s.Claim)
	if err != nil {
		return err
	}
	prev := parseRef(ref)
	if !isDigits(prev.Pres) {
		return nil
	}
	_, err = m.op(ctx, http.MethodPost, "p.ad", url.Values{
		"id": {prev.Pres}, "exsys": {m.exsys}, "dispid": {firstNonEmpty(prev.Disp, prev.Pres)}, "pkgid": {b.pkgID()},
		"qty": {"0"}, "cost": {"0.00"}, "icdcode": {diagnosisIDs(s)}, "date": {s.Claim.Encounter.StartDate.String()},
		"rem": {s.Claim.Provider.Name}, "update": {"1"},
	})
	return err
}

// Diagnoses travel on the lines (icdcode); there is nothing to add or remove.
func (m *Medserve) AddDiagnosis(_ context.Context, _ *connector.Submission, _ string, d connector.SendDiagnosis) (string, error) {
	return d.ICD, nil
}

func (m *Medserve) DeleteDiagnosis(context.Context, *connector.Submission, string, string) error {
	return nil
}

func approvalState(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "appr_state_")
	switch s {
	case "approved", "accept", "accepted", "granted", "1", "yes", "ok":
		return "approved"
	case "denied", "declined", "rejected", "reject", "2":
		return "denied"
	}
	return "pending"
}

func (m *Medserve) FinalizeClaim(ctx context.Context, s *connector.Submission, number string) (connector.Receipt, error) {
	type entry struct {
		ID    string `json:"id"`
		ICost string `json:"icost"`
	}
	var entries []entry
	for _, l := range s.Lines {
		ref := parseRef(s.Sent[l.Key])
		if ref.Disp == "" && isDigits(ref.Pres) {
			// Adopted from Medserve's copy after a lost reply: dispense it to
			// learn its dispensation.
			b, err := m.beneficiaryFor(ctx, s.Claim)
			if err != nil {
				return connector.Receipt{}, err
			}
			raw, err := m.dispense(ctx, s, ref, l, b.pkgID(), true)
			if err != nil {
				return connector.Receipt{}, err
			}
			ref = parseRef(raw)
		}
		if ref.NeedsApproval {
			r, err := m.op(ctx, http.MethodGet, "p.qdapreq", url.Values{"id": {ref.Disp}})
			if err != nil {
				return connector.Receipt{}, err
			}
			rec := firstRecord(r.Result.Value)
			switch approvalState(firstNonEmpty(scalar(rec["status"]), scalar(rec["approvalstatus"]), scalar(rec["approval_status"]))) {
			case "denied":
				return connector.Receipt{}, &connector.Error{Code: connector.PayerRefused,
					Message: "Medserve denied approval for dispensation " + ref.Disp, UserMessage: fmt.Sprintf("The insurer did not approve %q.", l.Description)}
			case "pending":
				return connector.Receipt{}, &connector.Error{Code: connector.AwaitingPayer,
					Message: "Medserve approval pending for dispensation " + ref.Disp, UserMessage: fmt.Sprintf("%q is waiting for the insurer's approval. We will check again.", l.Description)}
			}
		}
		if ref.Disp != "" {
			entries = append(entries, entry{ID: ref.Disp, ICost: money(ref.ICost)})
		}
	}
	if len(entries) > 0 {
		data, _ := json.Marshal(entries)
		if _, err := m.op(ctx, http.MethodPost, "p.bulkfind", url.Values{"data": {string(data)}}); err != nil && !isCode(err, connector.ClaimClosed) {
			return connector.Receipt{}, err
		}
	}
	_, err := m.op(ctx, http.MethodPut, "p.finalb", url.Values{"id": {number}})
	if err != nil && !isCode(err, connector.ClaimClosed) {
		return connector.Receipt{}, err
	}
	return connector.Receipt{PayerClaimNumber: number, Acknowledged: true}, nil
}

// FetchClaim reads a batch's prescription rows (op=p.qp4d). Medserve gives
// no batch status.
func (m *Medserve) FetchClaim(ctx context.Context, number string) (*connector.PayerClaim, error) {
	r, err := m.op(ctx, http.MethodGet, "p.qp4d", url.Values{"id": {number}})
	if err != nil {
		return nil, err
	}
	var parts []json.RawMessage
	_ = json.Unmarshal(r.Result.Value, &parts)
	pc := &connector.PayerClaim{Number: number, Status: connector.PayerUnknown}
	if len(parts) == 0 {
		return pc, nil
	}
	var rows []map[string]any
	_ = json.Unmarshal(parts[0], &rows)
	for _, row := range rows {
		pres := scalar(row["presId"])
		if pres == "" {
			continue
		}
		qty, _ := strconv.ParseFloat(scalar(row["qty"]), 64)
		cost := minor(scalar(row["cost"]))
		pc.Lines = append(pc.Lines, connector.PayerLine{
			Ref:  lineRef{Pres: pres, Qty: int64(qty), Cost: cost}.String(),
			Code: scalar(row["productCode"]), Quantity: int64(qty), Amount: cost,
		})
	}
	return pc, nil
}

// ---- helpers ----

func isCode(err error, code connector.ErrorCode) bool {
	ce, ok := err.(*connector.Error)
	return ok && ce.Code == code
}

// firstRecord finds the first object in a Posh value that looks like a
// result row (core-api-v2 _extract_first_result_record).
func firstRecord(raw json.RawMessage) map[string]any {
	keys := []string{"id", "dispid", "dispensationId", "batchcode", "batchCode", "icost", "needs_approval", "status", "code"}
	var walk func(v any) map[string]any
	walk = func(v any) map[string]any {
		switch t := v.(type) {
		case map[string]any:
			for _, k := range keys {
				if _, ok := t[k]; ok {
					return t
				}
			}
		case []any:
			for _, e := range t {
				if r := walk(e); r != nil {
					return r
				}
			}
		}
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return map[string]any{}
	}
	if r := walk(v); r != nil {
		return r
	}
	return map[string]any{}
}

func productRecords(raw json.RawMessage) []map[string]any {
	var values []any
	if json.Unmarshal(raw, &values) != nil {
		return nil
	}
	var out []map[string]any
	for _, v := range values {
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				if m, ok := e.(map[string]any); ok {
					out = append(out, m)
				}
			}
		case map[string]any:
			out = append(out, t)
		}
	}
	return out
}

// parseCoverage reads "op-drugs-acute/108/1" lines: the category id and
// whether it is covered (1) or not (0). A covered line wins.
func parseCoverage(coverage string) (string, *bool) {
	var firstCat string
	var firstCov *bool
	seen := false
	for _, line := range strings.Split(coverage, "\n") {
		parts := strings.Split(strings.TrimSpace(line), "/")
		if len(parts) < 3 {
			continue
		}
		cat := strings.TrimSpace(parts[len(parts)-2])
		var cov *bool
		switch strings.TrimSpace(parts[len(parts)-1]) {
		case "1":
			t := true
			cov = &t
		case "0":
			f := false
			cov = &f
		}
		if cov != nil && *cov {
			return cat, cov
		}
		if !seen {
			firstCat, firstCov, seen = cat, cov, true
		}
	}
	return firstCat, firstCov
}

var (
	searchSplit  = regexp.MustCompile(`[-@*'<>()#\s]+`)
	searchDelims = ",-@*'<>()#"
)

// SearchString builds p.prodsearchw's boolean-mode query from a product
// name (core-api-v2 _get_search_string): every word required and
// wildcarded, "," separating whole phrases, one-letter fragments dropped, so
// stray punctuation such as the ")" in "FULL BLOOD COUNT (FBC)" never
// reaches the query.
func SearchString(term string) string {
	term = strings.TrimSpace(term)
	if term == "" {
		return ""
	}
	if strings.Contains(term, ",") {
		var phrases []string
		for _, p := range strings.Split(term, ",") {
			p = strings.Trim(strings.TrimSpace(p), searchDelims)
			if len(p) > 1 {
				phrases = append(phrases, p)
			}
		}
		switch len(phrases) {
		case 0:
			return SearchString(strings.ReplaceAll(term, ",", " "))
		case 1:
			return "+" + phrases[0] + "*"
		}
		var b strings.Builder
		for _, p := range phrases {
			if strings.Contains(p, " ") {
				b.WriteString("+'" + p + "'*")
			} else {
				b.WriteString("+" + p + "*")
			}
		}
		return b.String()
	}
	var words []string
	for _, w := range searchSplit.Split(term, -1) {
		if len(w) > 1 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		if f := strings.Trim(term, searchDelims); f != "" {
			return "+" + f + "*"
		}
		return ""
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString("+" + w + "*")
	}
	return b.String()
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case json.Number:
		return t.String()
	}
	return ""
}

func minor(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	if f < 0 {
		return int64(f*100 - 0.5)
	}
	return int64(f*100 + 0.5)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// splitName: Medserve's surname field holds the first name and lastname the
// rest, as core-api-v2 reads them ("{surname} {lastname}").
func splitName(surname, lastname string) (string, string) {
	full := strings.Fields(strings.TrimSpace(surname + " " + lastname))
	switch len(full) {
	case 0:
		return "", ""
	case 1:
		return full[0], ""
	}
	return full[0], strings.Join(full[1:], " ")
}

func parseDate(s string) (claim.Date, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "T "); i >= 0 {
		s = s[:i]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return claim.Date{}, false
	}
	return claim.Date{Time: t}, true
}
