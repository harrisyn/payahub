// Package smartclaims is the SmartClaims (NMHA) connector, ported from
// serenity-health/core-api-v2 backend/app/plugins/insurance/smartclaims.py.
//
// Payer quirks that code documents, kept here:
//   - Sign-in is POST /token with the username and password in the query
//     string; the token goes in the Authorization header as is (no
//     "Bearer"). There is no documented expiry: it is renewed after 25
//     minutes, or on a 401.
//   - Asking for authorization (/members/getauthorization) makes the insurer
//     text the member an OTP itself and returns the attendance's claim check
//     code (ccc); there is no separate send-OTP call.
//   - /members/startattendance opens the claim with that OTP but never
//     returns a claim ID. Every /txn/* item call is keyed on the item's guid
//     (claimGUID); closing the claim (/claims/setstatus) needs the claim
//     check code and the attendance date as well, and a claim id that is
//     ours. So the payer claim number we keep is "ccc|guid|date".
//   - Lines are keyed on a refid we mint, stable for a claim and line, so an
//     add whose reply was lost and is sent again answers 409, and the line
//     is then updated in place instead.
//   - price is the line total, not the unit price. A code is sent only when
//     it is a real SmartClaims code (a mapped line); otherwise SmartClaims
//     matches on the description and refuses a code it does not know.
//   - Medicine lines need a prescription (dose, frequency, duration or
//     instructions); SmartClaims refuses one without (invalidPrescription).
//   - success:1 is not enough: an entry in warnings[] may carry a CGS rule
//     whose real outcome is an error (priceAdjustWarning). The rule sheet
//     decides the severity, and an added item that broke a rule is deleted
//     again before the error is reported. Validation failures also come back
//     as HTTP 400 with the same shape.
//   - Never surface SmartClaims' failedValidation: it dumps the whole internal
//     claim document, member details included.
//   - Services and medicines are listed at /hms/services and /hms/medicines
//     (the published /txn/* paths 404 on the live server), unpaginated with
//     limit=-1; "data" may be a list, an object keyed "0", "1", ..., or absent.
package smartclaims

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/httpx"
)

const Key = "smartclaims"

const name = "SmartClaims"

var manifest = connector.Manifest{
	Key:     Key,
	Name:    name,
	Kind:    connector.KindPlatform,
	Country: "GH",
	Channel: connector.ChannelAPI,
	Verbs: []connector.Verb{
		connector.VerifyMember, connector.SendOTP, connector.OpenClaim, connector.AddLine,
		connector.UpdateLine, connector.DeleteLine, connector.AddDiagnosis, connector.DeleteDiagnosis,
		connector.FinalizeClaim, connector.ListCatalog,
	},
	MemberOTPRequired: true,
	Credentials: []connector.CredentialField{
		{Name: "base_url", Label: "API base URL", Default: "https://claims.nmhapps.com"},
		{Name: "username", Label: "Username", Required: true},
		{Name: "password", Label: "Password", Secret: true, Required: true},
	},
	// HICD is the insurer's ID on SmartClaims (core-api-v2 reads it from the
	// payer's external reference), so it belongs to the payer, not the clinic.
	PayerFields: []connector.CredentialField{
		{Name: "hicd", Label: "Health insurance company ID (HICD)", Required: true},
	},
	Submission: func() connector.SubmissionConfig {
		c := connector.DefaultSubmissionConfig()
		// SmartClaims matches a line without a code on its description.
		c.UnmappedLinePolicy = "send_raw"
		return c
	}(),
	Status: connector.StatusAvailable,
}

func init() { connector.Register(manifest, New) }

type SmartClaims struct {
	connector.Unsupported
	base               string
	username, password string
	hicd               string
	tokenKey           string
	client             *http.Client

	mu       sync.Mutex
	warnings []string // non-fatal rule notes, returned on the receipt
}

func New(a connector.Account) (connector.Connector, error) {
	base := strings.TrimRight(manifest.Credentials[0].Value(a.Credentials), "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, connector.Errorf(connector.InvalidCredentials, "SmartClaims API base URL %q is not a URL", base)
	}
	hicd := strings.TrimSpace(a.PayerFields["hicd"])
	if hicd == "" {
		return nil, connector.Errorf(connector.Validation, "the payer has no SmartClaims HICD yet")
	}
	s := &SmartClaims{
		Unsupported: connector.Unsupported{Name: name},
		base:        base, username: a.Credentials["username"], password: a.Credentials["password"],
		hicd: hicd, client: httpx.DefaultClient,
	}
	s.tokenKey = httpx.Key(Key, base, s.username, s.password)
	return s, nil
}

func (s *SmartClaims) WithClient(c *http.Client) *SmartClaims { s.client = c; return s }

func (s *SmartClaims) Manifest() connector.Manifest { return manifest }

// ---- transport ----

func (s *SmartClaims) token(ctx context.Context) (httpx.Token, error) {
	return httpx.SharedTokens.Get(ctx, s.tokenKey, func(ctx context.Context) (httpx.Token, error) {
		q := url.Values{"username": {s.username}, "password": {s.password}}
		var r struct {
			Success any    `json:"success"`
			Token   string `json:"token"`
		}
		_, err := httpx.JSON(ctx, s.client, http.MethodPost, s.base+"/token?"+q.Encode(), nil, nil, &r)
		if err != nil {
			if st := httpx.Status(err); st != 0 && st < 500 {
				return httpx.Token{}, &connector.Error{Code: connector.InvalidCredentials, Message: err.Error(), Cause: err,
					UserMessage: "SmartClaims did not accept the username and password."}
			}
			return httpx.Token{}, httpx.Translate(name, err)
		}
		if r.Token == "" || !truthy(r.Success) {
			return httpx.Token{}, &connector.Error{Code: connector.InvalidCredentials, Message: "SmartClaims refused the sign-in",
				UserMessage: "SmartClaims did not accept the username and password."}
		}
		return httpx.Token{Value: r.Token, Expires: time.Now().Add(25 * time.Minute)}, nil
	})
}

// reply is the shape of /txn/* and /claims/* replies, 200 or 400.
type reply struct {
	Success any     `json:"success"`
	Failed  any     `json:"failed"`
	UserMsg string  `json:"user_msg"`
	RefID   string  `json:"refid"`
	Errors  []issue `json:"errors"`
	Warns   []issue `json:"warnings"`
}

type issue struct {
	Code string `json:"code"`
	Info string `json:"info"`
}

// call sends a request with the session token, signing in again once on a
// 401. A non-2xx reply is returned as *httpx.StatusError for the caller.
func (s *SmartClaims) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	u := s.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	for attempt := 0; ; attempt++ {
		tok, err := s.token(ctx)
		if err != nil {
			return err
		}
		_, err = httpx.JSON(ctx, s.client, method, u, http.Header{"Authorization": {tok.Value}}, body, out)
		if httpx.Status(err) == http.StatusUnauthorized && attempt == 0 {
			httpx.SharedTokens.Drop(s.tokenKey)
			continue
		}
		return err
	}
}

// severity is the CGS rule sheet: the outcome of each rule, whichever list
// SmartClaims put it in, and the sheet's wording for it.
var rules = map[string]struct {
	err bool
	msg string
}{
	"diagnosisEntriesRequired":          {true, "Diagnosis is required on claim"},
	"inapplicablePharmacyService":       {true, "Walkin pharmacy service is unacceptable"},
	"inapplicableTypeOfClaim":           {true, "Type of Claim (IPD,OPD, etc) is not applicable"},
	"invalidClaimCheckCode":             {true, "The attendance code is unacceptable"},
	"invalidDateOfService":              {true, "The date of service for the claim is unacceptable"},
	"invalidDiagnosisEntryDiagnosis":    {true, "One or more errors with diagnosis are unacceptable"},
	"invalidDispensedQty":               {true, "The dispensed quantity for medicine is unacceptable"},
	"invalidInvestigationEntryDate":     {true, "The date of service for investigation is unacceptable"},
	"invalidInvestigationEntryGdrgCode": {true, "The service code for a investigaton is unacceptable"},
	"invalidMedicineEntries":            {true, "One ore more medicines are unacceptable"},
	"invalidMedicineEntryCode":          {true, "The medicine code is unacceptable"},
	"invalidMedicineEntryDate":          {true, "The date of service/dispensary for medicine is unacceptable"},
	"invalidMemberDob":                  {true, "The members date of birth is unacceptable"},
	"invalidMemberGender":               {true, "The members gender is unacceptable"},
	"invalidMemberNo":                   {true, "The member no is unacceptable"},
	"invalidOtherNames":                 {true, "The member's other names is unacceptable"},
	"invalidPrescription":               {true, "The prescription is unacceptable"},
	"invalidProcedureEntryDate":         {true, "The date for a service/procedure"},
	"invalidProcedureEntryDiagnosis":    {true, "The diagnosis for a service/procedure is unacceptable"},
	"invalidProcedureEntryGdrgCode":     {true, "The service code for a service/procedure is unacceptable"},
	"invalidReferralInfo":               {true, "The referral information for the claim is unacceptable"},
	"invalidSurname":                    {true, "The members surname is unacceptable"},
	"invalidTypeOfAttendance":           {true, "The Type of Attendance (Emergency, Chronic, etc) is unacceptable"},
	"invalidTypeOfService":              {true, "The type of service for the claim (IPD,OPD, etc) is unacceptable"},
	"investigationEntriesRequired":      {true, "Investigation entries are required for the claim (e.g Diagnostic only claim)"},
	"medicineEntriesRequired":           {true, "Medicine entries are required for the claim (e.g Pharmacy only claim)"},
	"utilizationLimit":                  {true, "One or more member utilization has been exceeded"},
	"priceAdjustWarning":                {true, "The price of a service or medicine was adjusted to agreed price"},
	"duplicateClaim":                    {false, "The claim may be a duplicate"},
	"polypharmacy":                      {false, "The claim may have multiple drugs that interact adversily"},
	"medicinePreAuthWarning":            {false, "The medicine code require preauthorization"},
	"genErr":                            {true, "Uncategorized error"},
	"genWarn":                           {false, "Uncategorized warning"},
}

// classify sorts a reply's issues by the rule sheet. SmartClaims' own words
// are preferred; the sheet's are the fallback. An unknown code keeps the
// list SmartClaims put it in.
func (r reply) classify() (errs, warns []issue) {
	for _, list := range []struct {
		entries []issue
		err     bool
	}{{r.Errors, true}, {r.Warns, false}} {
		for _, e := range list.entries {
			code := strings.TrimSpace(e.Code)
			msg := strings.TrimSpace(e.Info)
			isErr := list.err
			if rule, ok := rules[code]; ok {
				isErr = rule.err
				if msg == "" {
					msg = rule.msg
				}
			}
			if msg == "" {
				msg = firstNonEmpty(code, "SmartClaims validation issue")
			}
			if isErr {
				errs = append(errs, issue{Code: code, Info: msg})
			} else {
				warns = append(warns, issue{Code: code, Info: msg})
			}
		}
	}
	return errs, warns
}

func (r reply) failed() bool { return truthy(r.Failed) || isFalse(r.Success) }

var (
	reCodeRules  = regexp.MustCompile(`GdrgCode|MedicineEntryCode`)
	reMemberRule = regexp.MustCompile(`^invalidMember|^invalidSurname|^invalidOtherNames`)
	reClosed     = regexp.MustCompile(`(?i)already\s+(closed|submitted)|is\s+closed|not\s+open`)
	reOTP        = regexp.MustCompile(`(?i)\botp\b|one[\s-]*time|token`)
)

// errStoredDespiteRule marks a 200 reply whose item SmartClaims stored while
// reporting a rule breach: the caller takes a fresh item off again. A 400
// stores nothing.
var errStoredDespiteRule = errors.New("smartclaims: item stored despite a rule breach")

// ruleError is the connector error for rule breaches.
func ruleError(op string, errs []issue, status int) error {
	var msgs, codes []string
	for _, e := range errs {
		msgs = append(msgs, e.Info)
		codes = append(codes, e.Code)
	}
	msg := strings.Join(msgs, "; ")
	ce := &connector.Error{Code: connector.PayerRefused, Message: fmt.Sprintf("SmartClaims %s: %s (%s)", op, msg, strings.Join(codes, ",")),
		PayerMessage: msg, UserMessage: "SmartClaims: " + msg}
	for _, c := range codes {
		switch {
		case reCodeRules.MatchString(c):
			ce.Code = connector.CodeNotRecognised
		case c == "invalidPrescription" || c == "invalidDispensedQty" || strings.HasSuffix(c, "Date") || c == "invalidDateOfService":
			ce.Code = connector.Validation
		case reMemberRule.MatchString(c):
			ce.Code = connector.Validation
		case c == "invalidClaimCheckCode":
			ce.Code = connector.AuthorizationRequired
		}
	}
	if status >= 500 {
		ce.Code = connector.Unknown
	}
	if status == http.StatusOK {
		ce.Cause = errStoredDespiteRule
	}
	return ce
}

// txn posts a /txn/* or /claims/* call and checks the reply the way
// core-api-v2 does: the HTTP status, the success flag, then the rule sheet.
// It returns the reply and its non-fatal warnings.
func (s *SmartClaims) txn(ctx context.Context, op, path string, body any) (reply, []issue, error) {
	var r reply
	err := s.call(ctx, http.MethodPost, path, nil, body, &r)
	var se *httpx.StatusError
	if errors.As(err, &se) {
		// 400s carry the same body as a 200 with errors; read it, never pass
		// its failedValidation dump on.
		var br reply
		if json.Unmarshal([]byte(se.Body), &br) == nil {
			if errs, _ := br.classify(); len(errs) > 0 {
				return br, nil, ruleError(op, errs, se.Status)
			}
			if msg := strings.TrimSpace(br.UserMsg); msg != "" {
				code := connector.PayerRefused
				switch {
				case se.Status == http.StatusConflict:
					code = connector.LineAlreadyAdded
				case se.Status == http.StatusNotFound:
					code = connector.LineNotFound
				case se.Status >= 500:
					code = connector.Unknown
				case reClosed.MatchString(msg):
					code = connector.ClaimClosed
				}
				return br, nil, &connector.Error{Code: code, Message: fmt.Sprintf("SmartClaims %s: HTTP %d: %s", op, se.Status, msg),
					PayerMessage: msg, UserMessage: "SmartClaims: " + msg, Cause: se}
			}
		}
		switch se.Status {
		case http.StatusConflict:
			return r, nil, &connector.Error{Code: connector.LineAlreadyAdded, Message: "SmartClaims " + op + ": already on the claim", Cause: se}
		case http.StatusNotFound:
			if strings.HasPrefix(path, "/txn/") && path != "/txn/additem" {
				return r, nil, &connector.Error{Code: connector.LineNotFound, Message: "SmartClaims " + op + ": not on the claim", Cause: se}
			}
		}
		// Unknown body: report the status only, not the body.
		se.Body = ""
		return r, nil, httpx.Translate(name, se)
	}
	if err != nil {
		return r, nil, err
	}
	if r.failed() {
		msg := firstNonEmpty(r.UserMsg, "the call was refused")
		code := connector.PayerRefused
		if reClosed.MatchString(msg) {
			code = connector.ClaimClosed
		}
		return r, nil, &connector.Error{Code: code, Message: "SmartClaims " + op + ": " + msg, PayerMessage: r.UserMsg,
			UserMessage: "SmartClaims: " + msg}
	}
	errs, warns := r.classify()
	if len(errs) > 0 {
		return r, warns, ruleError(op, errs, http.StatusOK)
	}
	return r, warns, nil
}

func (s *SmartClaims) note(warns []issue) {
	if len(warns) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range warns {
		s.warnings = append(s.warnings, "SmartClaims: "+w.Info)
	}
}

// ---- members ----

type memberInfo struct {
	ClientInfo struct {
		MemberNo    string `json:"memberno"`
		Surname     string `json:"surname"`
		OtherNames  string `json:"othernames"`
		OtherNames2 string `json:"otherNames"`
		DateOfBirth string `json:"dateofbirth"`
		DOB2        string `json:"dateOfBirth"`
		Gender      string `json:"gender"`
		Status      string `json:"status"`
		OTPPolicy   string `json:"otppolicy"`
		Policies    []struct {
			PolicyNo   any    `json:"policyno"`
			Plan       string `json:"policyplan"`
			PlanName   string `json:"policyplanName"`
			ExpiryDate string `json:"expirydate"`
		} `json:"policies"`
	} `json:"clientInfo"`
}

func (s *SmartClaims) memberInfo(ctx context.Context, memberNumber string) (memberInfo, error) {
	var m memberInfo
	err := s.call(ctx, http.MethodGet, "/members/getinfo", url.Values{"memberno": {memberNumber}, "hicd": {s.hicd}}, nil, &m)
	if st := httpx.Status(err); st == http.StatusNotFound || st == http.StatusBadRequest {
		return m, &connector.Error{Code: connector.MemberNotFound, Message: "SmartClaims has no member " + memberNumber, Cause: err,
			UserMessage: fmt.Sprintf("Member %q was not found at the insurer.", memberNumber)}
	}
	if err != nil {
		return m, httpx.Translate(name, err)
	}
	if m.ClientInfo.MemberNo == "" && m.ClientInfo.Status == "" {
		return m, &connector.Error{Code: connector.MemberNotFound, Message: "SmartClaims has no member " + memberNumber,
			UserMessage: fmt.Sprintf("Member %q was not found at the insurer.", memberNumber)}
	}
	return m, nil
}

// otpRequired reads otppolicy: required and conditional gate the claim,
// optional and notrequired do not; anything else gates it, to be safe.
func otpRequired(policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "optional", "notrequired":
		return false
	}
	return true
}

func (s *SmartClaims) VerifyMember(ctx context.Context, memberNumber, _ string) (connector.MemberVerification, error) {
	m, err := s.memberInfo(ctx, memberNumber)
	if err != nil {
		return connector.MemberVerification{}, err
	}
	ci := m.ClientInfo
	active := strings.EqualFold(ci.Status, "active")
	v := connector.MemberVerification{
		Eligible:    active,
		OTPRequired: otpRequired(ci.OTPPolicy),
		Member: claim.Member{
			MemberNumber: firstNonEmpty(ci.MemberNo, memberNumber),
			FirstName:    firstNonEmpty(ci.OtherNames, ci.OtherNames2),
			LastName:     ci.Surname,
			Gender:       strings.ToLower(ci.Gender),
		},
		Message: "Policy validated successfully",
	}
	if d, ok := parseDate(firstNonEmpty(ci.DateOfBirth, ci.DOB2)); ok {
		v.Member.BirthDate = &d
	}
	if len(ci.Policies) > 0 {
		p := ci.Policies[0]
		v.Member.PolicyNumber = scalar(p.PolicyNo)
		v.PlanName = firstNonEmpty(p.PlanName, p.Plan)
		v.PlanID = p.Plan
		if d, ok := parseDate(p.ExpiryDate); ok {
			v.CoverEnds = &d
		}
	}
	if !active {
		v.Reason, v.Message = "inactive", "Member status: "+firstNonEmpty(ci.Status, "unknown")
	}
	return v, nil
}

// SendOTP asks for authorization: the insurer texts the member a code and
// returns the attendance's claim check code, which is the challenge.
func (s *SmartClaims) SendOTP(ctx context.Context, memberNumber string) (string, error) {
	policy := "default" // SmartClaims picks the member's first active policy
	if m, err := s.memberInfo(ctx, memberNumber); err == nil && len(m.ClientInfo.Policies) > 0 {
		policy = firstNonEmpty(scalar(m.ClientInfo.Policies[0].PolicyNo), policy)
	}
	var r struct {
		reply
		ServiceInfo struct {
			CCC    string `json:"ccc"`
			OTPTel string `json:"otptel"`
		} `json:"serviceInfo"`
	}
	err := s.call(ctx, http.MethodPost, "/members/getauthorization", nil,
		map[string]string{"memberno": memberNumber, "policyno": policy, "hicd": s.hicd}, &r)
	if err != nil {
		return "", s.memberError("getauthorization", err)
	}
	if r.failed() {
		return "", &connector.Error{Code: connector.PayerRefused, Message: "SmartClaims getauthorization: " + r.UserMsg,
			PayerMessage: r.UserMsg, UserMessage: "SmartClaims: " + firstNonEmpty(r.UserMsg, "authorization was refused")}
	}
	if r.ServiceInfo.OTPTel == "" {
		return "", &connector.Error{Code: connector.Validation, Message: "SmartClaims getauthorization returned no OTP phone",
			UserMessage: "The insurer has no phone number for this member, so no code could be sent."}
	}
	return r.ServiceInfo.CCC, nil
}

func (s *SmartClaims) memberError(op string, err error) error {
	var se *httpx.StatusError
	if errors.As(err, &se) && se.Status < 500 && se.Status != http.StatusUnauthorized && se.Status != http.StatusForbidden {
		var br reply
		_ = json.Unmarshal([]byte(se.Body), &br)
		msg := firstNonEmpty(br.UserMsg, fmt.Sprintf("HTTP %d", se.Status))
		code := connector.PayerRefused
		if reOTP.MatchString(msg) {
			code = connector.OTPRequired
		}
		return &connector.Error{Code: code, Message: "SmartClaims " + op + ": " + msg, PayerMessage: br.UserMsg,
			UserMessage: "SmartClaims: " + msg, Cause: se}
	}
	return httpx.Translate(name, err)
}

// ---- claims ----

// number is the payer claim we keep: the claim check code, the claim guid
// the /txn/* calls are keyed on, and the attendance date.
type number struct{ ccc, guid, date string }

func (n number) String() string { return n.ccc + "|" + n.guid + "|" + n.date }

func parseNumber(s string) (number, error) {
	p := strings.Split(s, "|")
	if len(p) != 3 || p[1] == "" {
		return number{}, connector.Errorf(connector.Validation, "%q is not a SmartClaims claim reference", s)
	}
	return number{ccc: p[0], guid: p[1], date: p[2]}, nil
}

func serviceType(c *claim.Claim) string {
	if c.Encounter.Type == claim.Inpatient {
		return "IPD"
	}
	return "OPD"
}

func (s *SmartClaims) OpenClaim(ctx context.Context, sub *connector.Submission) (string, error) {
	c := sub.Claim
	if c.OTP == nil || strings.TrimSpace(c.OTP.Code) == "" {
		return "", &connector.Error{Code: connector.OTPRequired, Message: "SmartClaims opens a claim only with the member's OTP",
			UserMessage: "SmartClaims needs the code texted to the member. Send it, then add the code to the claim."}
	}
	var r struct {
		reply
		AttendanceDate string `json:"attendanceDate"`
		ClaimData      struct {
			Item struct {
				ClaimCheckCode string `json:"claimCheckCode"`
				GUID           string `json:"guid"`
				ID             any    `json:"id"`
			} `json:"item"`
		} `json:"claimData"`
	}
	err := s.call(ctx, http.MethodPost, "/members/startattendance", nil, map[string]string{
		"otp": strings.TrimSpace(c.OTP.Code), "memberno": c.Member.MemberNumber,
		"policyno":      firstNonEmpty(c.Member.PolicyNumber, "default"),
		"typeofservice": serviceType(c), "typeofattendance": "EAE", "hicd": s.hicd,
	}, &r)
	if err != nil {
		return "", s.memberError("startattendance", err)
	}
	if r.failed() {
		msg := firstNonEmpty(r.UserMsg, "the attendance was refused")
		code := connector.PayerRefused
		if reOTP.MatchString(msg) {
			code = connector.OTPRequired
		}
		return "", &connector.Error{Code: code, Message: "SmartClaims startattendance: " + msg, PayerMessage: r.UserMsg, UserMessage: "SmartClaims: " + msg}
	}
	item := r.ClaimData.Item
	n := number{ccc: item.ClaimCheckCode, guid: firstNonEmpty(item.GUID, scalar(item.ID)), date: dateOnly(r.AttendanceDate)}
	if n.ccc == "" || n.guid == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "SmartClaims startattendance returned no claim check code or guid",
			UserMessage: "SmartClaims did not confirm the attendance."}
	}
	if n.date == "" {
		n.date = c.Encounter.StartDate.String()
	}
	return n.String(), nil
}

// refID is our id for a line, stable for a claim and line.
func refID(c *claim.Claim, key string) string {
	sum := sha256.Sum256([]byte(c.ID + "\x00" + key))
	return "pa-" + hex.EncodeToString(sum[:8])
}

// itemType is SmartClaims' bucket for a line.
func itemType(l connector.SendLine) string {
	switch l.Category {
	case claim.CategoryDrug:
		return "medicine"
	}
	return "procedure"
}

func (s *SmartClaims) item(sub *connector.Submission, n number, l connector.SendLine, ref string) (map[string]any, string, error) {
	date := n.date
	if sub.Claim.Encounter.Type == claim.Inpatient && l.ServiceDate != nil {
		// OPD needs every service date to be the attendance date; IPD may differ.
		date = l.ServiceDate.String()
	}
	price := connector.Major(l.Amount, sub.Claim.Currency)
	t := itemType(l)
	if t == "medicine" {
		if l.Prescription.Empty() {
			return nil, t, &connector.Error{Code: connector.Validation, Message: "SmartClaims needs a prescription for medicine " + l.Code,
				UserMessage: fmt.Sprintf("Add the dose, frequency or duration for %q: SmartClaims needs a prescription for every medicine.", l.Description)}
		}
		p := l.Prescription
		item := map[string]any{
			"refid": ref, "description": firstNonEmpty(l.PayerName, l.Description), "price": price,
			"dispensedQty": l.Quantity, "serviceDate": date,
			"prescription": map[string]string{"dose": p.Dose, "frequency": p.Frequency, "duration": p.Duration, "unparsed": p.Instructions},
		}
		if l.Mapped {
			item["medicineCode"] = l.Code
		}
		return item, t, nil
	}
	item := map[string]any{
		"refid": ref, "description": firstNonEmpty(l.PayerName, l.Description), "serviceDate": date,
		"price": price, "qty": l.Quantity, "toothno": "",
	}
	if l.Mapped {
		item["code"] = l.Code
	}
	return item, t, nil
}

func (s *SmartClaims) AddLine(ctx context.Context, sub *connector.Submission, num string, l connector.SendLine) (string, error) {
	n, err := parseNumber(num)
	if err != nil {
		return "", err
	}
	ref := refID(sub.Claim, l.Key)
	item, t, err := s.item(sub, n, l, ref)
	if err != nil {
		return "", err
	}
	r, warns, err := s.txn(ctx, "additem", "/txn/additem", map[string]any{"claimGUID": n.guid, "itemType": t, "item": item})
	if isCode(err, connector.LineAlreadyAdded) {
		// An earlier attempt landed: make it match.
		return s.UpdateLine(ctx, sub, num, ref, l)
	}
	if err != nil {
		if isRuleBreach(err) {
			// The item was stored despite the rule; take it off again.
			_, _, _ = s.txn(ctx, "deleteitem", "/txn/deleteitem", map[string]string{"claimGUID": n.guid, "refid": firstNonEmpty(r.RefID, ref)})
		}
		return "", err
	}
	s.note(warns)
	return firstNonEmpty(r.RefID, ref), nil
}

func isRuleBreach(err error) bool { return errors.Is(err, errStoredDespiteRule) }

// UpdateLine changes the line in place. It is never rolled back on a rule
// breach: the line existed before the call.
func (s *SmartClaims) UpdateLine(ctx context.Context, sub *connector.Submission, num, ref string, l connector.SendLine) (string, error) {
	n, err := parseNumber(num)
	if err != nil {
		return "", err
	}
	item, t, err := s.item(sub, n, l, ref)
	if err != nil {
		return "", err
	}
	r, warns, err := s.txn(ctx, "updateitem", "/txn/updateitem", map[string]any{"claimGUID": n.guid, "itemType": t, "item": item})
	if err != nil {
		return "", err
	}
	s.note(warns)
	return firstNonEmpty(r.RefID, ref), nil
}

func (s *SmartClaims) DeleteLine(ctx context.Context, _ *connector.Submission, num, ref string) error {
	n, err := parseNumber(num)
	if err != nil {
		return err
	}
	_, _, err = s.txn(ctx, "deleteitem", "/txn/deleteitem", map[string]string{"claimGUID": n.guid, "refid": ref})
	return err
}

func (s *SmartClaims) AddDiagnosis(ctx context.Context, sub *connector.Submission, num string, d connector.SendDiagnosis) (string, error) {
	n, err := parseNumber(num)
	if err != nil {
		return "", err
	}
	ref := "dx-" + strings.ReplaceAll(strings.TrimSpace(d.ICD), " ", "-")
	kind := "Provisional Diagnosis"
	if d.Confirmed {
		kind = "Final Diagnosis"
	}
	item := map[string]any{"refid": ref, "icd10": d.ICD, "diagnosis": d.Description, "diagnosistype": kind}
	r, warns, err := s.txn(ctx, "additem", "/txn/additem", map[string]any{"claimGUID": n.guid, "itemType": "diagnosis", "item": item})
	if isCode(err, connector.LineAlreadyAdded) {
		return ref, nil
	}
	if err != nil {
		if isRuleBreach(err) {
			_, _, _ = s.txn(ctx, "deleteitem", "/txn/deleteitem", map[string]string{"claimGUID": n.guid, "refid": firstNonEmpty(r.RefID, ref)})
		}
		return "", err
	}
	s.note(warns)
	return firstNonEmpty(r.RefID, ref), nil
}

func (s *SmartClaims) DeleteDiagnosis(ctx context.Context, sub *connector.Submission, num, ref string) error {
	err := s.DeleteLine(ctx, sub, num, ref)
	if isCode(err, connector.LineNotFound) {
		return nil
	}
	return err
}

// FinalizeClaim closes the attendance (setstatus CLOSED). Our claim id is the
// encounter reference when there is one, else the claim's own id.
func (s *SmartClaims) FinalizeClaim(ctx context.Context, sub *connector.Submission, num string) (connector.Receipt, error) {
	n, err := parseNumber(num)
	if err != nil {
		return connector.Receipt{}, err
	}
	c := sub.Claim
	_, warns, err := s.txn(ctx, "setstatus", "/claims/setstatus", map[string]string{
		"claimid": firstNonEmpty(c.Encounter.Reference, c.ID), "memberno": c.Member.MemberNumber,
		"attendancedate": n.date, "attendancecode": n.ccc, "hicd": s.hicd, "status": "CLOSED",
	})
	if err != nil && !isCode(err, connector.ClaimClosed) {
		return connector.Receipt{}, err
	}
	s.note(warns)
	s.mu.Lock()
	defer s.mu.Unlock()
	return connector.Receipt{PayerClaimNumber: num, Acknowledged: true, Warnings: append([]string(nil), s.warnings...)}, nil
}

// ListCatalog downloads every service (procedures and investigations) or
// medicine, unpaginated.
func (s *SmartClaims) ListCatalog(ctx context.Context, kind connector.CatalogKind) ([]connector.CatalogEntry, error) {
	if kind != connector.CatalogItems {
		return nil, connector.Errorf(connector.NotSupported, "SmartClaims has no %s list", kind)
	}
	var out []connector.CatalogEntry
	seen := map[string]bool{}
	for _, list := range []struct{ path, group string }{{"/hms/services", "service"}, {"/hms/medicines", "medicine"}} {
		var raw json.RawMessage
		if err := s.call(ctx, http.MethodGet, list.path, url.Values{"limit": {"-1"}}, nil, &raw); err != nil {
			return nil, httpx.Translate(name, err)
		}
		for _, e := range catalogEntries(raw) {
			code := scalar(e["code"])
			if code == "" || seen[list.group+code] {
				continue
			}
			seen[list.group+code] = true
			out = append(out, connector.CatalogEntry{Code: code, Name: firstNonEmpty(scalar(e["description"]), scalar(e["name"])), Group: list.group})
		}
	}
	return out, nil
}

// catalogEntries accepts a bare list, {"data": list or index-keyed object},
// or an index-keyed object at the top level.
func catalogEntries(raw json.RawMessage) []map[string]any {
	var list []map[string]any
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	if data, ok := obj["data"]; ok {
		return catalogEntries(data)
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	// "0", "1", ... "10": by length, then text, is numeric order.
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b)) })
	for _, k := range keys {
		var e map[string]any
		if json.Unmarshal(obj[k], &e) == nil && e["code"] != nil {
			list = append(list, e)
		}
	}
	return list
}

// ---- helpers ----

func isCode(err error, code connector.ErrorCode) bool {
	var ce *connector.Error
	return err != nil && errors.As(err, &ce) && ce.Code == code
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	}
	return false
}

func isFalse(v any) bool {
	switch t := v.(type) {
	case bool:
		return !t
	case float64:
		return t == 0
	case string:
		return t == "0" || strings.EqualFold(t, "false")
	}
	return false
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return fmt.Sprintf("%.0f", t)
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

func dateOnly(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "T "); i >= 0 {
		s = s[:i]
	}
	return s
}

func parseDate(s string) (claim.Date, bool) {
	t, err := time.Parse("2006-01-02", dateOnly(s))
	if err != nil {
		return claim.Date{}, false
	}
	return claim.Date{Time: t}, true
}
