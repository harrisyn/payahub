// Package rxinsurance is the RX Insurance connector, ported from
// serenity-health/core-api-v2 backend/app/plugins/insurance/rx_insurance.py.
//
// Payer quirks that code documents, kept here:
//   - One endpoint takes every call as a JSON POST naming the METHOD, with
//     the API key, username, password, provider id, user id, attending
//     officer and insurance company code beside it. The reply is JSON,
//     sometimes after PHP notices, so the object is cut out of the body.
//   - Success is STATUSCODE 000/003/0/00, or a STATUSMSG that says so.
//     SUBMITCLAIM answers success with 012 ("Claims Submitted Sucessfully",
//     misspelt) and a resubmission with 013: both mean submitted.
//   - RX is slow: PROCESSCLAIMNO regularly takes more than 30 seconds, so
//     calls wait up to 60. PROCESSCLAIMNO is idempotent for a member and HMS
//     number: asked again, RX answers with the same claim, even an expired
//     one. A claim opened again after expiry therefore sends a distinct HMS
//     number, "<ref>-R<n>".
//   - Lines are keyed by the payer's item code (PROVIDERITEMID), so a claim
//     holds one line per code: lines are always grouped by code. QTY and
//     UNITPRICE are sent as text, the unit price in cedis.
//   - "no benefit" on ADDCLAIM: RX keeps the line with no benefit; the claim
//     goes on, with a warning. A line that needs authorization is refused
//     until it is authorised in RX's Claims Authorization screen.
//   - Diagnoses travel as ICDCODE, a comma list, on lines and on SUBMITCLAIM.
//     Inpatient claims need DATEOFADMISSION and DATEOFDISCHARGE.
//   - "Life Span of Claim has Expired" / "claim ... cannot be edited" mean the
//     open claim expired.
package rxinsurance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/httpx"
)

const Key = "rx_insurance"

const name = "RX Insurance"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    name,
	Kind:    connector.KindPlatform,
	Country: "GH",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.OpenClaim,
		connector.AddLine, connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis, connector.DeleteDiagnosis,
		connector.FinalizeClaim, connector.FetchClaim, connector.ListCatalog,
	},
	MemberOTPRequired: true,
	Credentials: []connector.CredentialField{
		{Name: "base_url", Label: "API URL (ends in restapi/index.php)", Required: true},
		{Name: "api_key", Label: "API key", Secret: true, Required: true},
		{Name: "username", Label: "Username", Required: true},
		{Name: "password", Label: "Password", Secret: true, Required: true},
		{Name: "provider_id", Label: "Provider ID", Required: true},
		{Name: "user_id", Label: "User ID", Required: true},
		{Name: "attending_officer", Label: "Attending officer", Default: "System User"},
	},
	// Sent as INSCOMPANYCODE on every call.
	PayerFields: []connector.CredentialField{
		{Name: "ins_company_code", Label: "Insurance company code", Required: true},
	},
	Submission: func() connector.SubmissionConfig {
		c := connector.DefaultSubmissionConfig()
		// A line is its item code on RX, inpatient as well.
		c.ItemGroupingInpatient = "grouped"
		return c
	}(),
	Status: connector.StatusAvailable,
}

func init() { connector.Register(manifest, New) }

// client waits up to 60 seconds: RX regularly takes more than 30.
var client = &http.Client{Timeout: 60 * time.Second, Transport: httpx.DefaultClient.Transport}

type RX struct {
	connector.Unsupported
	url    string
	auth   map[string]string
	client *http.Client

	mu       sync.Mutex
	warnings []string
}

func New(a connector.Account) (connector.Connector, error) {
	c := a.Credentials
	u, err := url.Parse(strings.TrimSpace(c["base_url"]))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, connector.Errorf(connector.InvalidCredentials, "RX API URL %q is not a URL", c["base_url"])
	}
	code := strings.TrimSpace(a.PayerFields["ins_company_code"])
	if code == "" {
		return nil, connector.Errorf(connector.Validation, "the payer has no RX insurance company code yet")
	}
	return &RX{
		Unsupported: connector.Unsupported{Name: name},
		url:         u.String(),
		auth: map[string]string{
			"APIKEY": c["api_key"], "USERNAME": c["username"], "PASSWORD": c["password"],
			"PROVIDERID": c["provider_id"], "USERID": c["user_id"],
			"ATTENDINGOFFICER": manifest.Credentials[6].Value(c), "INSCOMPANYCODE": code, "RESPONSETYPE": "JSON",
		},
		client: client,
	}, nil
}

func (x *RX) WithClient(c *http.Client) *RX { x.client = c; return x }

func (x *RX) Manifest() connector.Manifest { return manifest }

// ---- transport ----

type reply map[string]any

func (r reply) str(keys ...string) string {
	for _, k := range keys {
		if v := scalar(r[k]); v != "" {
			return v
		}
	}
	return ""
}

// call sends one METHOD. ok lists status codes that mean success for it
// beyond the usual ones.
func (x *RX) call(ctx context.Context, method string, fields map[string]string, ok ...string) (reply, error) {
	body := make(map[string]string, len(x.auth)+len(fields)+1)
	for k, v := range x.auth {
		body[k] = v
	}
	for k, v := range fields {
		body[k] = v
	}
	body["METHOD"] = method
	b, _ := json.Marshal(body)
	raw, err := httpx.Do(ctx, x.client, httpx.Request{Method: http.MethodPost, URL: x.url,
		Header: http.Header{"Content-Type": {"application/json"}}, Body: bytes.NewReader(b)})
	if err != nil {
		return nil, httpx.Translate(name, err)
	}
	var r reply
	if json.Unmarshal(raw, &r) != nil {
		// PHP notices before the JSON: take the outermost object.
		i, j := bytes.IndexByte(raw, '{'), bytes.LastIndexByte(raw, '}')
		if i < 0 || j <= i || json.Unmarshal(raw[i:j+1], &r) != nil {
			return nil, &connector.Error{Code: connector.Unknown, Message: "RX " + method + " reply is not JSON",
				UserMessage: "RX Insurance sent a reply we could not read.", PayerMessage: string(raw[:min(len(raw), 300)])}
		}
	}
	code, msg := r.str("STATUSCODE"), r.str("STATUSMSG", "STATUSDESC")
	for _, c := range append([]string{"000", "003", "0", "00"}, ok...) {
		if code == c {
			return r, nil
		}
	}
	if successMessage(msg) {
		return r, nil
	}
	return r, refused(method, code, msg)
}

func successMessage(msg string) bool {
	m := strings.ToLower(strings.TrimSpace(msg))
	switch m {
	case "results found", "ok", "success", "successful":
		return true
	}
	for _, f := range []string{"claims data has been added for the given item", "claims data has been updated for the given item",
		"claims data has been deleted for the given item"} {
		if strings.Contains(m, f) {
			return true
		}
	}
	return false
}

func expired(m string) bool {
	for _, p := range []string{"life span of claim has expired", "lifespan of claim has expired", "claim number has expired",
		"claim has expired", "expired claim"} {
		if strings.Contains(m, p) {
			return true
		}
	}
	return strings.Contains(m, "claim") && strings.Contains(m, "cannot be edited")
}

func memberNotFound(m string) bool {
	return strings.Contains(m, "no data found") || m == "no data" || m == "member not found" || m == "no record found"
}

var rePIN = regexp.MustCompile(`\b(pin|otp|validation token)\b`)

func refused(method, code, msg string) error {
	m := strings.ToLower(strings.TrimSpace(msg))
	e := &connector.Error{Code: connector.PayerRefused, Message: fmt.Sprintf("RX %s: %s (%s)", method, msg, code),
		PayerMessage: msg, UserMessage: "RX Insurance: " + firstNonEmpty(msg, "the request was refused")}
	switch {
	case expired(m):
		e.Code = connector.ClaimExpired
		e.UserMessage = "RX Insurance let the open claim expire. It will be opened again."
	case method == "MEMBERSTATUS" && memberNotFound(m):
		e.Code = connector.MemberNotFound
	case strings.Contains(m, "not found in database"):
		e.Code = connector.CodeNotRecognised
		e.UserMessage = "RX Insurance does not know this item: " + msg + ". Check the item's mapping."
	case strings.Contains(m, "authorization") && (strings.Contains(m, "require") || strings.Contains(m, "claims authorization")):
		e.Code = connector.AuthorizationRequired
		e.UserMessage = "RX Insurance needs this item authorised first: complete the request in RX's Claims Authorization, then send again."
	case rePIN.MatchString(m):
		e.Code = connector.OTPRequired
	case strings.Contains(m, "invalid api") || strings.Contains(m, "invalid user") || strings.Contains(m, "authentication"):
		e.Code = connector.InvalidCredentials
	case strings.Contains(m, "already submitted"):
		e.Code = connector.ClaimClosed
	}
	return e
}

func (x *RX) note(w string) {
	x.mu.Lock()
	x.warnings = append(x.warnings, w)
	x.mu.Unlock()
}

// ---- members ----

func (x *RX) VerifyMember(ctx context.Context, memberNumber, _ string) (connector.MemberVerification, error) {
	r, err := x.call(ctx, "MEMBERSTATUS", map[string]string{"MEMBERSHIPID": memberNumber})
	if err != nil {
		return connector.MemberVerification{}, err
	}
	status := strings.ToLower(r.str("MEMBERSTATUS"))
	plan := strings.ToLower(r.str("PLANSTATUS"))
	employer := strings.ToLower(r.str("EMPLOYERSTATUS"))
	v := connector.MemberVerification{
		Eligible:    status == "active" && plan == "active" && employer == "active",
		OTPRequired: strings.EqualFold(r.str("USEPIN"), "yes"),
		PlanName:    r.str("MEMBERPLAN"),
		PlanID:      r.str("MEMBERPLANID"),
		Member: claim.Member{
			MemberNumber: firstNonEmpty(r.str("MEMBERSHIPID"), memberNumber),
			PolicyNumber: firstNonEmpty(r.str("POLICYNO", "POLICYNUMBER"), memberNumber),
			FirstName:    r.str("FIRSTNAME"),
			LastName:     strings.TrimSpace(r.str("SURNAME") + " " + r.str("OTHERNAMES")),
			Gender:       strings.ToLower(r.str("SEX")),
			Phone:        r.str("TEL"),
		},
		Message: "Policy validated successfully",
	}
	if d, ok := parseDate(r.str("DATEOFBIRTH")); ok {
		v.Member.BirthDate = &d
	}
	if d, ok := parseDate(r.str("EXPIRYDATE")); ok {
		v.CoverEnds = &d
	}
	switch {
	case status != "active":
		v.Reason, v.Message = "inactive", "Member status: "+strings.ToUpper(firstNonEmpty(r.str("MEMBERSTATUS"), "unknown"))
	case plan != "active":
		v.Reason, v.Message = "plan_inactive", "Plan status not active: "+firstNonEmpty(r.str("PLANSTATUS"), "Unknown")
	case employer != "active":
		v.Reason, v.Message = "employer_inactive", "Employer status not active: "+firstNonEmpty(r.str("EMPLOYERSTATUS"), "Unknown")
	}
	return v, nil
}

// SendOTP has RX text the member a PIN (SENDSMS). There is no challenge.
func (x *RX) SendOTP(ctx context.Context, memberNumber string) (string, error) {
	_, err := x.call(ctx, "SENDSMS", map[string]string{"MEMBERSHIPID": memberNumber})
	return "", err
}

// ---- claims ----

func visitType(c *claim.Claim) string {
	if c.Encounter.Type == claim.Inpatient {
		return "In-Patient"
	}
	return "Out-Patient"
}

// hmsNo is our reference for the visit; a claim opened again after expiry
// gets "-R<n>" so RX issues a new claim rather than the expired one.
func hmsNo(s *connector.Submission) string {
	ref := firstNonEmpty(s.Claim.Encounter.Reference, s.Claim.ID)
	if s.Renewal > 0 {
		ref += "-R" + strconv.Itoa(s.Renewal)
	}
	return ref
}

func (x *RX) OpenClaim(ctx context.Context, s *connector.Submission) (string, error) {
	c := s.Claim
	f := map[string]string{
		"MEMBERSHIPID": c.Member.MemberNumber, "DATEOFCONSULTATION": c.Encounter.StartDate.String(),
		"TYPEOFVISIT": visitType(c), "HMSNO": hmsNo(s),
	}
	if c.OTP != nil && strings.TrimSpace(c.OTP.Code) != "" {
		f["PIN"] = strings.TrimSpace(c.OTP.Code)
	}
	r, err := x.call(ctx, "PROCESSCLAIMNO", f)
	if err != nil {
		return "", err
	}
	n := r.str("CLAIMNUMBER")
	if n == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "RX PROCESSCLAIMNO returned no claim number",
			UserMessage: "RX Insurance did not return a claim number."}
	}
	return n, nil
}

// icdCodes is the claim's diagnoses as RX takes them: the payer's codes when
// mapped, else ICD-10, comma-separated, primary first.
func icdCodes(s *connector.Submission) string {
	var codes []string
	for _, d := range s.Diagnoses {
		codes = append(codes, firstNonEmpty(d.PayerCode, d.ICD))
	}
	return strings.Join(codes, ",")
}

func price(l connector.SendLine, currency string) string {
	return strconv.FormatFloat(l.UnitPrice(currency), 'f', 2, 64)
}

func (x *RX) lineFields(s *connector.Submission, number string, l connector.SendLine) map[string]string {
	f := map[string]string{
		"CLAIMNUMBER": number, "MEMBERSHIPID": s.Claim.Member.MemberNumber, "PROVIDERITEMID": l.Code,
		"ITEMNAME": firstNonEmpty(l.PayerName, l.Description), "QTY": strconv.FormatInt(l.Quantity, 10),
		"UNITPRICE": price(l, s.Claim.Currency),
	}
	if icd := icdCodes(s); icd != "" {
		f["ICDCODE"] = icd
	}
	return f
}

func (x *RX) AddLine(ctx context.Context, s *connector.Submission, number string, l connector.SendLine) (string, error) {
	_, err := x.call(ctx, "ADDCLAIM", x.lineFields(s, number, l))
	if err != nil {
		ce := connector.AsError(err)
		if strings.Contains(strings.ToLower(ce.PayerMessage), "no benefit") {
			// RX keeps the line without benefit; the claim goes on.
			x.note(fmt.Sprintf("RX Insurance: %q has no benefit for this member (%s).", l.Description, ce.PayerMessage))
			return l.Code, nil
		}
		if strings.Contains(strings.ToLower(ce.PayerMessage), "already") && strings.Contains(strings.ToLower(ce.PayerMessage), "exist") {
			// An earlier attempt landed: bring it in line.
			return x.UpdateLine(ctx, s, number, l.Code, l)
		}
		return "", err
	}
	return l.Code, nil
}

func (x *RX) UpdateLine(ctx context.Context, s *connector.Submission, number, _ string, l connector.SendLine) (string, error) {
	if _, err := x.call(ctx, "UPDATECLAIM", x.lineFields(s, number, l)); err != nil {
		return "", err
	}
	return l.Code, nil
}

func (x *RX) DeleteLine(ctx context.Context, s *connector.Submission, number, ref string) error {
	_, err := x.call(ctx, "DELETECLAIM", map[string]string{
		"CLAIMNUMBER": number, "MEMBERSHIPID": s.Claim.Member.MemberNumber, "PROVIDERITEMID": ref,
	})
	return err
}

// Diagnoses travel as ICDCODE on lines and on SUBMITCLAIM.
func (x *RX) AddDiagnosis(_ context.Context, _ *connector.Submission, _ string, d connector.SendDiagnosis) (string, error) {
	return d.ICD, nil
}

func (x *RX) DeleteDiagnosis(context.Context, *connector.Submission, string, string) error {
	return nil
}

func (x *RX) FinalizeClaim(ctx context.Context, s *connector.Submission, number string) (connector.Receipt, error) {
	c := s.Claim
	f := map[string]string{
		"CLAIMNUMBER": number, "TYPEOFVISIT": visitType(c), "MEMBERSHIPID": c.Member.MemberNumber,
	}
	if icd := icdCodes(s); icd != "" {
		f["ICDCODE"] = icd
	}
	if c.Encounter.Type == claim.Inpatient {
		admitted := c.Encounter.StartDate
		if c.Encounter.AdmissionDate != nil {
			admitted = *c.Encounter.AdmissionDate
		}
		f["DATEOFADMISSION"] = admitted.String()
		discharged := admitted
		switch {
		case c.Encounter.DischargeDate != nil:
			discharged = *c.Encounter.DischargeDate
		case c.Encounter.EndDate != nil:
			discharged = *c.Encounter.EndDate
		}
		f["DATEOFDISCHARGE"] = discharged.String()
	}
	if _, err := x.call(ctx, "SUBMITCLAIM", f, "012", "013"); err != nil && !isCode(err, connector.ClaimClosed) {
		return connector.Receipt{}, err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	return connector.Receipt{PayerClaimNumber: number, Acknowledged: true, Warnings: append([]string(nil), x.warnings...)}, nil
}

// FetchClaim reads the claim's lines (VIEWCLAIMSLOCAL). RX gives no status
// we can rely on.
func (x *RX) FetchClaim(ctx context.Context, number string) (*connector.PayerClaim, error) {
	r, err := x.call(ctx, "VIEWCLAIMSLOCAL", map[string]string{"CLAIMNUMBER": number})
	if err != nil {
		return nil, err
	}
	pc := &connector.PayerClaim{Number: number, Status: connector.PayerUnknown, Message: r.str("STATUSMSG")}
	rows, _ := r["RESULTS"].([]any)
	for _, row := range rows {
		e, ok := row.(map[string]any)
		if !ok {
			continue
		}
		code := scalar(e["itemcode"])
		if code == "" {
			continue
		}
		qty, _ := strconv.ParseFloat(scalar(e["qty"]), 64)
		unit, _ := strconv.ParseFloat(scalar(e["price"]), 64)
		pc.Lines = append(pc.Lines, connector.PayerLine{Ref: code, Code: code, Quantity: int64(qty),
			Amount: int64(unit*qty*100 + 0.5)})
	}
	return pc, nil
}

func (x *RX) ListCatalog(ctx context.Context, kind connector.CatalogKind) ([]connector.CatalogEntry, error) {
	method, listKeys, codeKeys, nameKeys := "ITEMLIST",
		[]string{"ITEMLIST", "ITEMS", "ITEM_DETAILS", "ITEMDETAILS", "ITEM"},
		[]string{"PROVIDERITEMID", "ITEMID", "ITEMCODE", "PRODUCTID", "SERVICEID", "CODE"},
		[]string{"ITEMNAME", "ITEM", "NAME", "DESCRIPTION", "PRODUCTNAME", "SERVICENAME"}
	if kind == connector.CatalogDiagnoses {
		method, listKeys, codeKeys, nameKeys = "DIAGNOSISLIST",
			[]string{"DIAGNOSISLIST", "RESULTS", "DIAGNOSIS", "DIAGNOSES"},
			[]string{"ICDCODE", "icdcode", "CODE", "code"},
			[]string{"DIAGNOSIS", "diagnosis", "DESCRIPTION", "description"}
	}
	r, err := x.call(ctx, method, nil)
	if err != nil {
		return nil, err
	}
	var rows []any
	for _, k := range listKeys {
		switch v := r[k].(type) {
		case []any:
			rows = v
		case map[string]any:
			rows = []any{v}
		}
		if rows != nil {
			break
		}
	}
	var out []connector.CatalogEntry
	for _, row := range rows {
		e, ok := row.(map[string]any)
		if !ok {
			continue
		}
		rr := reply(e)
		code := rr.str(codeKeys...)
		if code == "" {
			continue
		}
		entry := connector.CatalogEntry{Code: code, Name: rr.str(nameKeys...)}
		if p, err := strconv.ParseFloat(rr.str("PRICE", "UNITPRICE", "TARIFF"), 64); err == nil && p > 0 {
			minor := int64(p*100 + 0.5)
			entry.Price = &minor
		}
		out = append(out, entry)
	}
	return out, nil
}

// ---- helpers ----

func isCode(err error, code connector.ErrorCode) bool {
	return err != nil && connector.AsError(err).Code == code
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

func parseDate(s string) (claim.Date, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "T "); i >= 0 {
		s = s[:i]
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006", "02-01-2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return claim.Date{Time: t}, true
		}
	}
	return claim.Date{}, false
}
