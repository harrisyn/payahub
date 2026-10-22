// Package xceli is the XCeli Solutions connector, ported from
// serenity-health/core-api-v2 backend/app/plugins/insurance/xceli.py.
//
// Payer quirks that code documents, kept here:
//   - Two servers: sign-in goes to a central auth server, every member and
//     claim call to the insurer's own API server (a payer field).
//   - Sign-in returns a JWT and a userDTO whose Id must be sent as UserID on
//     every write; the token's expiry is tracked and it is renewed before it lapses.
//   - Replies use an envelope {IsActionSuccessful, ActionMessage, ReturnValue,
//     ReturnData}; some calls (member lookup, lists) return data bare.
//   - ReturnValue is a status code (0 on success), never an id: the id a write
//     creates comes back in ReturnData. Reading ReturnValue first stored 0 as
//     a line id in production.
//   - Claims are built incrementally (createihspclaim, then one call per line
//     and per diagnosis) and only reach the insurer at submitclaim.
//   - A diagnosis is added with the ICD-10 code as ICDCode and the insurer's
//     numeric disease id as DiseaseID, then must be confirmed
//     (setdiagnosisconfirmstatus, Status 0) or submitclaim refuses the claim.
//   - IsPlanValidForHSP absent or null does not restrict; only false blocks.
//   - Whether a member needs an OTP comes from the member lookup (RequireOTP).
//   - InOutID is 1 for outpatient, 2 for inpatient.
package xceli

import (
	"context"
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

const Key = "xceli"

const name = "XCeli"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    name,
	Kind:    connector.KindPlatform,
	Country: "GH",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.ValidateOTP, connector.OpenClaim,
		connector.AddLine, connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis,
		connector.DeleteDiagnosis, connector.FinalizeClaim, connector.FetchClaim, connector.ListCatalog,
	},
	MemberOTPRequired: true,
	Credentials: []connector.CredentialField{
		{Name: "auth_base_url", Label: "Sign-in server", Default: "https://xcelihealthapi.xcelisolutions.com"},
		{Name: "username", Label: "Portal username", Required: true},
		{Name: "password", Label: "Portal password", Secret: true, Required: true},
		{Name: "provider_id", Label: "Provider ID (a number, e.g. 269)", Required: true},
		{Name: "provider_name", Label: "Provider name", Required: true},
	},
	// Each insurer on XCeli runs its own API server; sign-in is shared.
	PayerFields: []connector.CredentialField{
		{Name: "insurer_api_url", Label: "Insurer API URL", Required: true},
	},
	Submission: func() connector.SubmissionConfig {
		c := connector.DefaultSubmissionConfig()
		c.DiagnosisCodeSource = "payer_code"
		return c
	}(),
	Status: connector.StatusAvailable,
}

func init() { connector.Register(manifest, New) }

type XCeli struct {
	connector.Unsupported
	authURL, insurerURL string
	username, password  string
	providerID          int
	tokenKey            string
	client              *http.Client
}

// New builds the connector for one organisation's XCeli account and one insurer.
func New(a connector.Account) (connector.Connector, error) {
	creds := a.Credentials
	id, err := strconv.Atoi(strings.TrimSpace(creds["provider_id"]))
	if err != nil || id <= 0 {
		return nil, connector.Errorf(connector.InvalidCredentials, "XCeli provider ID must be a positive number, got %q", creds["provider_id"])
	}
	x := &XCeli{
		Unsupported: connector.Unsupported{Name: name},
		authURL:     strings.TrimRight(manifest.Credentials[0].Value(creds), "/"),
		insurerURL:  strings.TrimRight(a.PayerFields["insurer_api_url"], "/"),
		username:    creds["username"],
		password:    creds["password"],
		providerID:  id,
		client:      httpx.DefaultClient,
	}
	for _, u := range []string{x.authURL, x.insurerURL} {
		if p, err := url.Parse(u); err != nil || (p.Scheme != "https" && p.Scheme != "http") || p.Host == "" {
			return nil, connector.Errorf(connector.InvalidCredentials, "XCeli server address %q is not a URL", u)
		}
	}
	x.tokenKey = httpx.Key(Key, x.authURL, x.username, x.password)
	return x, nil
}

// WithClient lets tests point the connector at a fake server's client.
func (x *XCeli) WithClient(c *http.Client) *XCeli { x.client = c; return x }

func (x *XCeli) Manifest() connector.Manifest { return manifest }

// ---- session ----

type signIn struct {
	IsAuthSuccessful bool   `json:"IsAuthSuccessful"`
	ErrorMessage     string `json:"ErrorMessage"`
	Token            string `json:"Token"`
	Expiration       string `json:"Expiration"`
	UserDTO          struct {
		ID       json.RawMessage `json:"Id"`
		Insurers []struct {
			CompanyName string `json:"CompanyName"`
			APIURL      string `json:"APIUrl"`
		} `json:"Insurers"`
	} `json:"userDTO"`
}

func (x *XCeli) session(ctx context.Context) (httpx.Token, error) {
	return httpx.SharedTokens.Get(ctx, x.tokenKey, func(ctx context.Context) (httpx.Token, error) {
		var r signIn
		_, err := httpx.JSON(ctx, x.client, http.MethodPost, x.authURL+"/api/hspaccount/signin", nil,
			map[string]string{"UserName": x.username, "Password": x.password}, &r)
		if err != nil {
			if s := httpx.Status(err); s != 0 && s < 500 {
				return httpx.Token{}, &connector.Error{Code: connector.InvalidCredentials, Message: err.Error(),
					UserMessage: "XCeli did not accept the username and password."}
			}
			return httpx.Token{}, httpx.Translate(name, err)
		}
		if !r.IsAuthSuccessful || r.Token == "" {
			return httpx.Token{}, &connector.Error{Code: connector.InvalidCredentials, Message: "XCeli sign-in rejected: " + r.ErrorMessage,
				UserMessage: "XCeli did not accept the username and password.", PayerMessage: r.ErrorMessage}
		}
		expires, err := time.Parse(time.RFC3339, strings.Replace(r.Expiration, "Z", "+00:00", 1))
		if err != nil {
			if expires, err = time.Parse("2006-01-02T15:04:05", r.Expiration); err != nil {
				expires = time.Now().Add(23 * time.Hour) // core-api-v2's fallback
			}
		}
		return httpx.Token{Value: r.Token, Expires: expires, Extra: map[string]string{"user_id": rawString(r.UserDTO.ID)}}, nil
	})
}

// call sends one request to the insurer's server with the session token,
// signing in again once if XCeli has dropped the session.
func (x *XCeli) call(ctx context.Context, method, path string, body, out any) ([]byte, httpx.Token, error) {
	for attempt := 0; ; attempt++ {
		tok, err := x.session(ctx)
		if err != nil {
			return nil, tok, err
		}
		h := http.Header{"Authorization": {"Bearer " + tok.Value}}
		raw, err := httpx.JSON(ctx, x.client, method, x.insurerURL+path, h, body, out)
		if httpx.Status(err) == http.StatusUnauthorized && attempt == 0 {
			httpx.SharedTokens.Drop(x.tokenKey)
			continue
		}
		return raw, tok, err
	}
}

// envelope is XCeli's reply to every write.
type envelope struct {
	IsActionSuccessful *bool           `json:"IsActionSuccessful"`
	ActionMessage      string          `json:"ActionMessage"`
	ReturnValue        json.RawMessage `json:"ReturnValue"`
	ReturnData         json.RawMessage `json:"ReturnData"`
	// Some writes name the created id directly.
	ClaimsNo        string          `json:"ClaimsNo"`
	ClaimNo         string          `json:"ClaimNo"`
	ClaimsDetailsID json.RawMessage `json:"ClaimsDetailsId"`
	RowID           json.RawMessage `json:"RowID"`
}

// write posts body and checks the envelope.
func (x *XCeli) write(ctx context.Context, method, path string, body any) (envelope, httpx.Token, error) {
	var env envelope
	_, tok, err := x.call(ctx, method, path, body, &env)
	if err != nil {
		return env, tok, translate(err)
	}
	if env.IsActionSuccessful != nil && !*env.IsActionSuccessful {
		return env, tok, refused(path, env.ActionMessage)
	}
	return env, tok, nil
}

var (
	reNotFound  = regexp.MustCompile(`(?i)not\s+found|does\s+not\s+exist|no\s+record|invalid\s+membership`)
	reOTP       = regexp.MustCompile(`(?i)\botp\b|one[- ]time|pin\b`)
	reClosed    = regexp.MustCompile(`(?i)already\s+(been\s+)?submitted|has\s+been\s+submitted|closed|vetted`)
	reInactive  = regexp.MustCompile(`(?i)inactive|expired|suspended|terminated`)
	reCode      = regexp.MustCompile(`(?i)product|tariff|item|disease|icd`)
	reConfirmDx = regexp.MustCompile(`(?i)confirm(ed)?\s+diagnos`)
)

// refused turns an envelope failure into the taxonomy once, here.
func refused(path, msg string) error {
	e := &connector.Error{Message: fmt.Sprintf("XCeli %s: %s", path, msg), PayerMessage: msg, UserMessage: "XCeli: " + msg}
	switch {
	case reOTP.MatchString(msg):
		e.Code = connector.OTPRequired
	case reClosed.MatchString(msg):
		e.Code = connector.ClaimClosed
	case strings.Contains(path, "/member/") && reNotFound.MatchString(msg):
		e.Code = connector.MemberNotFound
	case reInactive.MatchString(msg):
		e.Code = connector.MemberInactive
	case reConfirmDx.MatchString(msg):
		e.Code = connector.Validation
	case reCode.MatchString(msg) && reNotFound.MatchString(msg):
		e.Code = connector.CodeNotRecognised
	default:
		e.Code = connector.PayerRefused
	}
	return e
}

func translate(err error) error { return httpx.Translate(name, err) }

// ---- members ----

type memberReply struct {
	Status            string          `json:"Status"`
	MembershipNo      string          `json:"MembershipNo"`
	Fullname          string          `json:"Fullname"`
	FirstName         string          `json:"FirstName"`
	Surname           string          `json:"Surname"`
	OtherName         string          `json:"OtherName"`
	Dob               string          `json:"Dob"`
	Gender            string          `json:"Gender"`
	Mphone            string          `json:"Mphone"`
	PlanName          string          `json:"PlanName"`
	PlanID            json.RawMessage `json:"PlanId"`
	ExpiryDate        string          `json:"ExpiryDate"`
	PolicyEndDate     string          `json:"PolicyEndDate"`
	IsPlanValidForHSP *bool           `json:"IsPlanValidForHSP"`
	RequireOTP        bool            `json:"RequireOTP"`
}

func (x *XCeli) VerifyMember(ctx context.Context, memberNumber, otp string) (connector.MemberVerification, error) {
	var r memberReply
	path := fmt.Sprintf("/api/member/ihsp/%s/%d", url.PathEscape(memberNumber), x.providerID)
	raw, _, err := x.call(ctx, http.MethodGet, path, nil, &r)
	if err != nil {
		if httpx.Status(err) == http.StatusNotFound || (httpx.Status(err) != 0 && reNotFound.MatchString(err.Error())) {
			return connector.MemberVerification{}, &connector.Error{Code: connector.MemberNotFound,
				Message: "XCeli has no member " + memberNumber, UserMessage: fmt.Sprintf("Policy number %q was not found at the insurer.", memberNumber)}
		}
		return connector.MemberVerification{}, translate(err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 || r.MembershipNo == "" && r.Status == "" {
		return connector.MemberVerification{}, &connector.Error{Code: connector.MemberNotFound,
			Message: "XCeli returned no member " + memberNumber, UserMessage: fmt.Sprintf("Policy number %q was not found at the insurer.", memberNumber)}
	}

	active := strings.EqualFold(r.Status, "active")
	v := connector.MemberVerification{
		Eligible:    active && (r.IsPlanValidForHSP == nil || *r.IsPlanValidForHSP),
		PlanName:    r.PlanName,
		PlanID:      rawString(r.PlanID),
		OTPRequired: r.RequireOTP,
		Member: claim.Member{
			MemberNumber: firstNonEmpty(r.MembershipNo, memberNumber),
			PolicyNumber: memberNumber,
			FirstName:    r.FirstName,
			LastName:     r.Surname,
			Gender:       strings.ToLower(r.Gender),
			Phone:        r.Mphone,
		},
		Message: "Policy validated successfully",
	}
	if d, ok := parseDate(r.Dob); ok {
		v.Member.BirthDate = &d
	}
	if d, ok := parseDate(firstNonEmpty(r.ExpiryDate, r.PolicyEndDate)); ok {
		v.CoverEnds = &d
	}
	switch {
	case !active:
		v.Reason, v.Message = "inactive", "Member status: "+strings.ToUpper(firstNonEmpty(r.Status, "unknown"))
	case r.IsPlanValidForHSP != nil && !*r.IsPlanValidForHSP:
		v.Reason, v.Message = "not_covered_here", "The member is active but their plan does not cover this provider."
	}
	if v.Eligible && r.RequireOTP && otp != "" {
		if err := x.ValidateOTP(ctx, memberNumber, otp); err != nil {
			return v, err
		}
		v.OTPRequired = false
	}
	return v, nil
}

func (x *XCeli) SendOTP(ctx context.Context, memberNumber string) (string, error) {
	_, _, err := x.write(ctx, http.MethodPost, "/api/utilities/generateotp",
		map[string]any{"MembershipNumber": memberNumber, "ProviderId": x.providerID})
	return "", err
}

func (x *XCeli) ValidateOTP(ctx context.Context, memberNumber, otp string) error {
	_, _, err := x.write(ctx, http.MethodPost, "/api/utilities/validateotp",
		map[string]any{"MembershipNumber": memberNumber, "OTP": otp})
	if ce, ok := err.(*connector.Error); ok && ce.Code == connector.PayerRefused {
		ce.Code = connector.OTPRequired
		ce.UserMessage = "The insurer did not accept that code. Ask the member for the new one."
	}
	return err
}

// ---- claims ----

func inOutID(c *claim.Claim) int {
	if c.Encounter.Type == claim.Inpatient {
		return 2
	}
	return 1
}

func memberNo(c *claim.Claim) string { return c.Member.MemberNumber }

func (x *XCeli) OpenClaim(ctx context.Context, s *connector.Submission) (string, error) {
	c := s.Claim
	tok, err := x.session(ctx)
	if err != nil {
		return "", err
	}
	env, _, err := x.write(ctx, http.MethodPost, "/api/claim/createihspclaim", map[string]any{
		"MembershipNo":     memberNo(c),
		"DateOfAttendance": c.Encounter.StartDate.String(),
		"InOutID":          inOutID(c),
		"UserID":           userID(tok),
		"ProviderID":       x.providerID,
	})
	if err != nil {
		return "", err
	}
	// The claim number comes back under one of several names (xceli.py).
	number := firstNonEmpty(env.ClaimsNo, env.ClaimNo, rawString(env.ReturnData), env.ActionMessage)
	if number == "" || strings.ContainsAny(number, " \t") {
		return "", &connector.Error{Code: connector.Unknown, Message: "XCeli created a claim but sent no claim number",
			UserMessage: "XCeli did not return a claim number.", PayerMessage: env.ActionMessage}
	}
	return number, nil
}

func lineBody(s *connector.Submission, number string, l connector.SendLine, tok httpx.Token) (map[string]any, error) {
	product, err := strconv.Atoi(strings.TrimSpace(l.Code))
	if err != nil || product <= 0 {
		return nil, &connector.Error{Code: connector.CodeNotRecognised, Message: fmt.Sprintf("XCeli product ids are numbers; %q is not", l.Code),
			UserMessage: fmt.Sprintf("%q has no XCeli product id. Map it to the insurer's item first.", l.Description)}
	}
	return map[string]any{
		"ClaimsNo":  number,
		"ProductId": product,
		"Qty":       l.Quantity,
		"Claimed":   l.UnitPrice(s.Claim.Currency),
		"UserID":    userID(tok),
		"Comment":   l.Description,
	}, nil
}

func (x *XCeli) AddLine(ctx context.Context, s *connector.Submission, number string, l connector.SendLine) (string, error) {
	tok, err := x.session(ctx)
	if err != nil {
		return "", err
	}
	body, err := lineBody(s, number, l, tok)
	if err != nil {
		return "", err
	}
	env, _, err := x.write(ctx, http.MethodPost, "/api/claim/createhspclaimdetails", body)
	if err != nil {
		return "", err
	}
	ref := firstPositive(env.ClaimsDetailsID, env.ReturnData, env.ReturnValue)
	if ref == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "XCeli added a line but sent no line id",
			UserMessage: "XCeli did not confirm the line.", PayerMessage: env.ActionMessage}
	}
	return ref, nil
}

func (x *XCeli) UpdateLine(ctx context.Context, s *connector.Submission, number, ref string, l connector.SendLine) (string, error) {
	id, err := strconv.Atoi(ref)
	if err != nil {
		return "", connector.Errorf(connector.LineNotFound, "XCeli line id %q is not a number", ref)
	}
	tok, err := x.session(ctx)
	if err != nil {
		return "", err
	}
	body, err := lineBody(s, number, l, tok)
	if err != nil {
		return "", err
	}
	body["ClaimsDetailsId"] = id
	_, _, err = x.write(ctx, http.MethodPost, "/api/claim/updatehspclaimdetails", body)
	return "", err
}

func (x *XCeli) DeleteLine(ctx context.Context, _ *connector.Submission, _, ref string) error {
	id, err := strconv.Atoi(ref)
	if err != nil {
		return connector.Errorf(connector.LineNotFound, "XCeli line id %q is not a number", ref)
	}
	_, _, err = x.write(ctx, http.MethodDelete, fmt.Sprintf("/api/claim/deletehspclaimdetails/%d", id), nil)
	if httpx.Status(err) == http.StatusNotFound {
		return connector.Errorf(connector.LineNotFound, "XCeli has no line %d", id)
	}
	return err
}

func (x *XCeli) AddDiagnosis(ctx context.Context, _ *connector.Submission, number string, d connector.SendDiagnosis) (string, error) {
	tok, err := x.session(ctx)
	if err != nil {
		return "", err
	}
	body := map[string]any{"ClaimsNo": number, "ICDCode": d.ICD, "UserID": userID(tok)}
	if id, err := strconv.Atoi(d.PayerCode); err == nil && id > 0 {
		body["DiseaseID"] = id
	}
	env, _, err := x.write(ctx, http.MethodPost, "/api/claim/createihspclaimdisease", body)
	if err != nil {
		return "", err
	}
	row := firstPositive(env.RowID, env.ReturnData, env.ReturnValue)
	if row == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "XCeli added a diagnosis but sent no row id",
			UserMessage: "XCeli did not confirm the diagnosis.", PayerMessage: env.ActionMessage}
	}
	rowID, _ := strconv.Atoi(row)
	if _, _, err := x.write(ctx, http.MethodPost, "/api/claim/setdiagnosisconfirmstatus",
		map[string]any{"RowID": rowID, "Status": 0}); err != nil {
		// An unconfirmed diagnosis blocks submitclaim; take it off so the
		// retry adds and confirms it cleanly instead of leaving a duplicate.
		_ = x.DeleteDiagnosis(ctx, nil, number, row)
		return "", err
	}
	return row, nil
}

func (x *XCeli) DeleteDiagnosis(ctx context.Context, _ *connector.Submission, _, ref string) error {
	id, err := strconv.Atoi(ref)
	if err != nil {
		return nil // nothing we could have added
	}
	_, _, err = x.write(ctx, http.MethodDelete, fmt.Sprintf("/api/claim/deletehspclaimdisease/%d", id), nil)
	if httpx.Status(err) == http.StatusNotFound {
		return nil
	}
	return err
}

func (x *XCeli) FinalizeClaim(ctx context.Context, s *connector.Submission, number string) (connector.Receipt, error) {
	c := s.Claim
	attended := c.Encounter.StartDate.String()
	discharged := attended
	if d := c.Encounter.DischargeDate; d != nil {
		discharged = d.String()
	} else if d := c.Encounter.EndDate; d != nil {
		discharged = d.String()
	}
	tok, err := x.session(ctx)
	if err != nil {
		return connector.Receipt{}, err
	}
	_, _, err = x.write(ctx, http.MethodPost, "/api/claim/submitclaim", map[string]any{
		"ClaimsNo":               number,
		"ActualDateOfAttendance": attended,
		"Comment":                "",
		"DischargeDate":          discharged,
		"MembershipNo":           memberNo(c),
		"DateOfAttendance":       attended,
		"InOutID":                inOutID(c),
		"UserID":                 userID(tok),
		"ProviderID":             x.providerID,
	})
	if ce, ok := err.(*connector.Error); ok && ce.Code == connector.ClaimClosed {
		// A resend after a lost reply: XCeli already has the claim submitted.
		return connector.Receipt{PayerClaimNumber: number, Acknowledged: true,
			Warnings: []string{"XCeli reported the claim as already submitted: " + ce.PayerMessage}}, nil
	}
	if err != nil {
		return connector.Receipt{}, err
	}
	return connector.Receipt{PayerClaimNumber: number, Acknowledged: true}, nil
}

type claimLine struct {
	ID        json.RawMessage `json:"Id"`
	ProductID json.RawMessage `json:"ProductId"`
	Qty       float64         `json:"Qty"`
	UnitPrice *float64        `json:"UnitPrice"`
	Claimed   *float64        `json:"Claimed"`
}

// FetchClaim lists the lines XCeli holds on a claim. XCeli's API gives no
// claim status, so Status is unknown.
func (x *XCeli) FetchClaim(ctx context.Context, number string) (*connector.PayerClaim, error) {
	var lines []claimLine
	_, _, err := x.call(ctx, http.MethodGet, "/api/claim/gethspclaimdetails/"+url.PathEscape(number), nil, &lines)
	if err != nil {
		return nil, translate(err)
	}
	pc := &connector.PayerClaim{Number: number, Status: connector.PayerUnknown}
	for _, l := range lines {
		qty := int64(l.Qty)
		var amount int64
		switch {
		case l.UnitPrice != nil:
			amount = int64(*l.UnitPrice*float64(qty)*100 + 0.5)
		case l.Claimed != nil:
			amount = int64(*l.Claimed*100 + 0.5)
		}
		pc.Lines = append(pc.Lines, connector.PayerLine{
			Ref: rawString(l.ID), Code: rawString(l.ProductID), Quantity: qty, Amount: amount,
		})
	}
	return pc, nil
}

// ListCatalog downloads the insurer's products or diseases.
func (x *XCeli) ListCatalog(ctx context.Context, kind connector.CatalogKind) ([]connector.CatalogEntry, error) {
	path := "/api/tariff/getallproducts"
	codeKeys := []string{"ProductId", "TariffId", "Id", "Code", "id", "code"}
	nameKeys := []string{"ProductName", "TariffName", "Name", "Description", "name", "description"}
	wrapKeys := []string{"Products", "Data"}
	if kind == connector.CatalogDiagnoses {
		path = "/api/disease/getalldiseases"
		codeKeys = []string{"Code", "IcdCode", "ICDCode", "icdCode", "code"}
		nameKeys = []string{"Name", "DiseaseName", "Description", "name", "description"}
		wrapKeys = []string{"Diseases", "Data"}
	}
	raw, _, err := x.call(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, translate(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		var wrapped map[string]json.RawMessage
		if json.Unmarshal(raw, &wrapped) == nil {
			for _, k := range wrapKeys {
				if json.Unmarshal(wrapped[k], &rows) == nil && rows != nil {
					break
				}
			}
		}
	}
	out := make([]connector.CatalogEntry, 0, len(rows))
	for _, row := range rows {
		code := pick(row, codeKeys)
		if code == "" {
			continue
		}
		e := connector.CatalogEntry{Code: code, Name: pick(row, nameKeys), Group: pick(row, []string{"TypeName", "Category"})}
		if p := pick(row, []string{"Price", "UnitPrice", "Tariff", "Amount"}); p != "" {
			if f, err := strconv.ParseFloat(p, 64); err == nil {
				minor := int64(f*100 + 0.5)
				e.Price = &minor
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// ---- helpers ----

func userID(t httpx.Token) any {
	id := t.Extra["user_id"]
	if n, err := strconv.Atoi(id); err == nil {
		return n
	}
	return id
}

// rawString reads a JSON scalar (string or number) as a string.
func rawString(r json.RawMessage) string {
	s := strings.TrimSpace(string(r))
	if s == "" || s == "null" {
		return ""
	}
	var str string
	if json.Unmarshal(r, &str) == nil {
		return strings.TrimSpace(str)
	}
	var f float64
	if json.Unmarshal(r, &f) == nil {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return ""
}

// firstPositive returns the first value that is a positive integer.
func firstPositive(rs ...json.RawMessage) string {
	for _, r := range rs {
		s := rawString(r)
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return s
		}
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

func pick(row map[string]any, keys []string) string {
	for _, k := range keys {
		switch v := row[k].(type) {
		case string:
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return ""
}

// parseDate reads XCeli's "2026-12-31T00:00:00" or "2026-12-31".
func parseDate(s string) (claim.Date, bool) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, 'T'); i >= 0 {
		s = s[:i]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return claim.Date{}, false
	}
	return claim.Date{Time: t}, true
}
