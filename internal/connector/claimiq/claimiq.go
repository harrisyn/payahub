// Package claimiq is the ClaimIQ connector, ported from
// serenity-health/core-api-v2 backend/app/plugins/insurance/claimiq.py.
//
// Payer quirks that code documents, kept here:
//   - Every path is under /v1 of the API root; the key goes in X-API-Key.
//     A base URL already ending in /v1 is taken as is.
//   - Filing a claim does not submit it. POST /claims (or the incremental
//     item and diagnosis calls) only queues it in ClaimIQ; nothing reaches
//     the payer until POST /batches runs. Claims go as a batch of one, with
//     an Idempotency-Key fixed by the claim, so a retry reuses the batch.
//   - POST /claims needs at least one item, so it cannot open an empty claim:
//     the incremental path opens one through POST /patients/check-in with
//     create_claim=true, naming the patient by membership_number (not
//     staff_id_number, which ClaimIQ checks against the employer's staff).
//   - Claims are addressed by their uuid "id"; "claim_number" is only for
//     people. The uuid is the payer claim number we keep.
//   - The patient is staff_id on a claim: ClaimIQ's own id for the member,
//     found with POST /patients/verify. A dependent matched there names its
//     principal in relates_to.
//   - An incrementally built claim is confirmed (POST /claims/{id}/confirm)
//     before its batch, after claim_data is set (PATCH, pre-batch only).
//   - Items carry unit_cost in major units, a category from ClaimIQ's list,
//     and tariff_code only when the line is mapped to a ClaimIQ tariff.
package claimiq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/connector/httpx"
)

const Key = "claimiq"

const name = "ClaimIQ"

var manifest = func() connector.Manifest {
	sub := connector.DefaultSubmissionConfig()
	sub.LineSubmissionMode = "bulk"
	sub.ClaimCreationTiming = "deferred"
	sub.UnmappedLinePolicy = "send_raw" // ClaimIQ takes items without a tariff code
	return connector.Manifest{
		Key:     Key,
		Name:    name,
		Kind:    connector.KindPlatform,
		Country: "GH",
		Channel: connector.ChannelAPI,
		Verbs: []connector.Verb{
			connector.VerifyMember, connector.SubmitWholeClaim,
			connector.OpenClaim, connector.AddLine, connector.UpdateLine, connector.DeleteLine,
			connector.AddDiagnosis, connector.DeleteDiagnosis, connector.FinalizeClaim, connector.FetchClaim,
		},
		Credentials: []connector.CredentialField{
			{Name: "base_url", Label: "API root", Default: "https://api.claimiq.app/api"},
			{Name: "api_key", Label: "API key", Secret: true, Required: true},
		},
		Submission: sub,
		Status:     connector.StatusAvailable,
	}
}()

func init() { connector.Register(manifest, New) }

type ClaimIQ struct {
	connector.Unsupported
	api    string
	key    string
	client *http.Client

	patient map[string]string // member number -> ClaimIQ patient id
}

func New(a connector.Account) (connector.Connector, error) {
	base := strings.TrimRight(manifest.Credentials[0].Value(a.Credentials), "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, connector.Errorf(connector.InvalidCredentials, "ClaimIQ API root %q is not a URL", base)
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return &ClaimIQ{Unsupported: connector.Unsupported{Name: name}, api: base, key: a.Credentials["api_key"],
		client: httpx.DefaultClient, patient: map[string]string{}}, nil
}

func (q *ClaimIQ) WithClient(c *http.Client) *ClaimIQ { q.client = c; return q }

func (q *ClaimIQ) Manifest() connector.Manifest { return manifest }

// ---- transport ----

func (q *ClaimIQ) do(ctx context.Context, op, method, path string, body, out any, idem string) error {
	h := http.Header{"X-API-Key": {q.key}}
	if idem != "" {
		h.Set("Idempotency-Key", idem)
	}
	_, err := httpx.JSON(ctx, q.client, method, q.api+path, h, body, out)
	if err == nil {
		return nil
	}
	st := httpx.Status(err)
	if st == 0 || st == http.StatusUnauthorized || st == http.StatusForbidden || st >= 500 || st == http.StatusTooManyRequests {
		return httpx.Translate(name, err)
	}
	msg := errorMessage(err)
	ce := &connector.Error{Code: connector.PayerRefused, Message: fmt.Sprintf("ClaimIQ %s: HTTP %d: %s", op, st, msg),
		PayerMessage: msg, UserMessage: "ClaimIQ: " + msg, Cause: err}
	low := strings.ToLower(msg)
	switch {
	case st == http.StatusNotFound && (op == "update item" || op == "delete item" || op == "delete diagnosis"):
		ce.Code = connector.LineNotFound
	case st == http.StatusNotFound && op == "verify":
		ce.Code = connector.MemberNotFound
	case st == http.StatusConflict && strings.Contains(low, "batch"):
		ce.Code = connector.ClaimClosed
	case strings.Contains(low, "already") && (strings.Contains(low, "batched") || strings.Contains(low, "submitted") || strings.Contains(low, "confirmed")):
		ce.Code = connector.ClaimClosed
	case strings.Contains(low, "tariff"):
		ce.Code = connector.CodeNotRecognised
	case st == http.StatusBadRequest || st == http.StatusUnprocessableEntity:
		ce.Code = connector.Validation
	}
	return ce
}

// errorMessage reads ClaimIQ's error body: {"error": {"message"}} or
// {"message"} / {"detail"} / {"error": "..."}, else the status.
func errorMessage(err error) string {
	var se *httpx.StatusError
	if !errors.As(err, &se) {
		return err.Error()
	}
	var body map[string]any
	if json.Unmarshal([]byte(se.Body), &body) == nil {
		if e, ok := body["error"].(map[string]any); ok {
			if m, ok := e["message"].(string); ok && m != "" {
				return m
			}
		}
		for _, k := range []string{"message", "detail", "error"} {
			if m, ok := body[k].(string); ok && m != "" {
				return m
			}
		}
	}
	return fmt.Sprintf("HTTP %d", se.Status)
}

// ---- members ----

type patient struct {
	ID           string `json:"id"`
	MemberID     string `json:"member_id"`
	MemberType   string `json:"member_type"`
	MemberNumber string `json:"member_number"`
	Membership   string `json:"membership_number"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	OtherName    string `json:"other_name"`
	DateOfBirth  string `json:"date_of_birth"`
	Gender       string `json:"gender"`
	IsActive     *bool  `json:"is_active"`
}

func (q *ClaimIQ) find(ctx context.Context, memberNumber string) (*patient, error) {
	var r struct {
		Patients []patient `json:"patients"`
	}
	if err := q.do(ctx, "verify", http.MethodPost, "/patients/verify",
		map[string]any{"query": memberNumber, "include_dependents": true}, &r, ""); err != nil {
		return nil, err
	}
	if len(r.Patients) == 0 {
		return nil, nil
	}
	p := r.Patients[0]
	q.patient[memberNumber] = firstNonEmpty(p.MemberID, p.ID)
	return &p, nil
}

func (q *ClaimIQ) VerifyMember(ctx context.Context, memberNumber, _ string) (connector.MemberVerification, error) {
	p, err := q.find(ctx, memberNumber)
	if err != nil {
		return connector.MemberVerification{}, err
	}
	if p == nil {
		return connector.MemberVerification{}, &connector.Error{Code: connector.MemberNotFound,
			Message: "ClaimIQ found no patient " + memberNumber, UserMessage: fmt.Sprintf("Member %q was not found at the insurer.", memberNumber)}
	}
	active := p.IsActive == nil || *p.IsActive
	v := connector.MemberVerification{
		Eligible: active,
		Member: claim.Member{
			MemberNumber: firstNonEmpty(p.MemberNumber, p.Membership, memberNumber),
			PolicyNumber: firstNonEmpty(p.MemberNumber, memberNumber),
			FirstName:    p.FirstName, LastName: strings.TrimSpace(p.LastName + " " + p.OtherName),
			Gender: strings.ToLower(p.Gender),
		},
		Message: "Member found",
	}
	if t, err := time.Parse("2006-01-02", firstNonEmpty(p.DateOfBirth)); err == nil {
		d := claim.Date{Time: t}
		v.Member.BirthDate = &d
	}
	if !active {
		v.Reason, v.Message = "inactive", "Member found but currently inactive"
	}
	return v, nil
}

// staffID is ClaimIQ's id for the claim's patient.
func (q *ClaimIQ) staffID(ctx context.Context, memberNumber string) (string, error) {
	if id, ok := q.patient[memberNumber]; ok {
		return id, nil
	}
	p, err := q.find(ctx, memberNumber)
	if err != nil {
		return "", err
	}
	if p == nil || q.patient[memberNumber] == "" {
		return "", &connector.Error{Code: connector.MemberNotFound, Message: "ClaimIQ found no patient " + memberNumber,
			UserMessage: fmt.Sprintf("ClaimIQ does not know member %q, so the claim cannot be filed.", memberNumber)}
	}
	return q.patient[memberNumber], nil
}

// ---- claims ----

func category(c claim.LineCategory) string {
	switch c {
	case claim.CategoryDrug:
		return "drug"
	case claim.CategoryProcedure:
		return "procedure"
	case claim.CategoryConsumable:
		return "consumable"
	case claim.CategoryService, "": // the claim default
		return "service"
	}
	return "other"
}

func item(l connector.SendLine, currency string) map[string]any {
	it := map[string]any{
		"category": category(l.Category), "description": firstNonEmpty(l.PayerName, l.Description),
		"quantity": l.Quantity, "unit_cost": round2(l.UnitPrice(currency)),
	}
	if l.Mapped {
		it["tariff_code"] = l.Code
	}
	return it
}

func diagnosis(d connector.SendDiagnosis) map[string]any {
	return map[string]any{"diagnosis_text": firstNonEmpty(d.Description, d.ICD), "icd10_code": d.ICD, "is_primary": d.Primary}
}

// claimData is the visit as ClaimIQ wants it. A claim of medicines only with
// no diagnosis is a dispense; anything else is an episode.
func claimData(s *connector.Submission) map[string]any {
	c := s.Claim
	class := "episode"
	if len(s.Diagnoses) == 0 && len(s.Lines) > 0 {
		class = "dispense"
		for _, l := range s.Lines {
			if l.Category != claim.CategoryDrug {
				class = "episode"
				break
			}
		}
	}
	visit := "outpatient"
	if c.Encounter.Type == claim.Inpatient {
		visit = "inpatient"
	}
	return map[string]any{"service_class": class, "hospital_name": c.Provider.Name,
		"date_of_visit": c.Encounter.StartDate.String(), "visit_type": visit}
}

func idemKey(s *connector.Submission) string {
	k := "payahub-" + s.Claim.ID
	if s.Renewal > 0 {
		k += "-r" + strconv.Itoa(s.Renewal)
	}
	return k
}

func (q *ClaimIQ) batch(ctx context.Context, id string) error {
	err := q.do(ctx, "batch", http.MethodPost, "/batches", map[string]any{"claim_ids": []string{id}}, nil, "batch-"+id)
	if isCode(err, connector.ClaimClosed) {
		return nil // batched on an earlier attempt
	}
	return err
}

// SubmitWholeClaim files the claim and sends it as a batch of one. Both
// calls carry Idempotency-Keys fixed by the claim, so a retry after a lost
// reply gets the same claim and the same batch.
func (q *ClaimIQ) SubmitWholeClaim(ctx context.Context, s *connector.Submission) (connector.Receipt, error) {
	staff, err := q.staffID(ctx, s.Claim.Member.MemberNumber)
	if err != nil {
		return connector.Receipt{}, err
	}
	items := make([]map[string]any, 0, len(s.Lines))
	for _, l := range s.Lines {
		items = append(items, item(l, s.Claim.Currency))
	}
	dx := make([]map[string]any, 0, len(s.Diagnoses))
	for _, d := range s.Diagnoses {
		dx = append(dx, diagnosis(d))
	}
	body := map[string]any{"method": "manual", "staff_id": staff, "claim_data": claimData(s),
		"diagnoses": dx, "items": items, "external_ref": s.Claim.ID}
	if code := s.Claim.AuthorizationCode; code != "" {
		body["authorization_number"] = code
	}
	var created struct {
		ID          string `json:"id"`
		ClaimNumber string `json:"claim_number"`
	}
	if err := q.do(ctx, "file claim", http.MethodPost, "/claims", body, &created, idemKey(s)); err != nil {
		return connector.Receipt{}, err
	}
	if created.ID == "" {
		return connector.Receipt{}, &connector.Error{Code: connector.Unknown, Message: "ClaimIQ filed the claim but sent no id",
			UserMessage: "ClaimIQ did not confirm the claim."}
	}
	if err := q.batch(ctx, created.ID); err != nil {
		return connector.Receipt{PayerClaimNumber: created.ID}, err
	}
	return connector.Receipt{PayerClaimNumber: created.ID, Acknowledged: true}, nil
}

// OpenClaim opens an empty claim at check-in.
func (q *ClaimIQ) OpenClaim(ctx context.Context, s *connector.Submission) (string, error) {
	var r struct {
		Claim struct {
			ID string `json:"id"`
		} `json:"claim"`
	}
	visit := "outpatient"
	if s.Claim.Encounter.Type == claim.Inpatient {
		visit = "inpatient"
	}
	if err := q.do(ctx, "check in", http.MethodPost, "/patients/check-in", map[string]any{
		"manual": true, "membership_number": s.Claim.Member.MemberNumber, "visit_type": visit, "create_claim": true,
	}, &r, ""); err != nil {
		return "", err
	}
	if r.Claim.ID == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "ClaimIQ check-in returned no claim",
			UserMessage: "ClaimIQ checked the member in but opened no claim."}
	}
	return r.Claim.ID, nil
}

func (q *ClaimIQ) AddLine(ctx context.Context, s *connector.Submission, id string, l connector.SendLine) (string, error) {
	var r struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := q.do(ctx, "add item", http.MethodPost, "/claims/"+url.PathEscape(id)+"/items",
		map[string]any{"items": []any{item(l, s.Claim.Currency)}}, &r, ""); err != nil {
		return "", err
	}
	if len(r.Items) == 0 || r.Items[0].ID == "" {
		return "", &connector.Error{Code: connector.Unknown, Message: "ClaimIQ added an item but sent no id",
			UserMessage: "ClaimIQ did not confirm the line."}
	}
	return r.Items[0].ID, nil
}

func (q *ClaimIQ) UpdateLine(ctx context.Context, s *connector.Submission, id, ref string, l connector.SendLine) (string, error) {
	it := item(l, s.Claim.Currency)
	delete(it, "tariff_code")
	err := q.do(ctx, "update item", http.MethodPatch, "/claims/"+url.PathEscape(id)+"/items/"+url.PathEscape(ref), it, nil, "")
	if isCode(err, connector.LineNotFound) {
		return q.AddLine(ctx, s, id, l)
	}
	if err != nil {
		return "", err
	}
	return ref, nil
}

func (q *ClaimIQ) DeleteLine(ctx context.Context, _ *connector.Submission, id, ref string) error {
	err := q.do(ctx, "delete item", http.MethodDelete, "/claims/"+url.PathEscape(id)+"/items/"+url.PathEscape(ref), nil, nil, "")
	if isCode(err, connector.LineNotFound) {
		return nil
	}
	return err
}

func (q *ClaimIQ) AddDiagnosis(ctx context.Context, _ *connector.Submission, id string, d connector.SendDiagnosis) (string, error) {
	var r struct {
		Diagnoses []struct {
			ID string `json:"id"`
		} `json:"diagnoses"`
	}
	if err := q.do(ctx, "add diagnosis", http.MethodPost, "/claims/"+url.PathEscape(id)+"/diagnoses",
		map[string]any{"diagnoses": []any{diagnosis(d)}}, &r, ""); err != nil {
		return "", err
	}
	if len(r.Diagnoses) > 0 && r.Diagnoses[0].ID != "" {
		return r.Diagnoses[0].ID, nil
	}
	return d.ICD, nil
}

// DeleteDiagnosis edits the claim's diagnosis list to the ones still wanted
// (ClaimIQ has no single-diagnosis delete; PATCH /claims/{id} replaces the
// list before the batch).
func (q *ClaimIQ) DeleteDiagnosis(ctx context.Context, s *connector.Submission, id, _ string) error {
	dx := make([]map[string]any, 0, len(s.Diagnoses))
	for _, d := range s.Diagnoses {
		dx = append(dx, diagnosis(d))
	}
	return q.do(ctx, "edit claim", http.MethodPatch, "/claims/"+url.PathEscape(id), map[string]any{"diagnoses": dx}, nil, "")
}

// FinalizeClaim sets the visit details, confirms the claim and sends it as
// a batch of one.
func (q *ClaimIQ) FinalizeClaim(ctx context.Context, s *connector.Submission, id string) (connector.Receipt, error) {
	path := "/claims/" + url.PathEscape(id)
	err := q.do(ctx, "edit claim", http.MethodPatch, path, map[string]any{"claim_data": claimData(s)}, nil, "")
	if err != nil && !isCode(err, connector.ClaimClosed) {
		return connector.Receipt{}, err
	}
	if err := q.do(ctx, "confirm", http.MethodPost, path+"/confirm", nil, nil, ""); err != nil && !isCode(err, connector.ClaimClosed) {
		return connector.Receipt{}, err
	}
	if err := q.batch(ctx, id); err != nil {
		return connector.Receipt{PayerClaimNumber: id}, err
	}
	return connector.Receipt{PayerClaimNumber: id, Acknowledged: true}, nil
}

// statuses maps ClaimIQ's claim status to ours.
var statuses = map[string]connector.PayerStatus{
	"pending": connector.PayerOpen, "draft": connector.PayerOpen, "open": connector.PayerOpen, "unconfirmed": connector.PayerOpen,
	"confirmed": connector.PayerOpen,
	"batched":   connector.PayerSubmitted, "submitted": connector.PayerSubmitted, "in_review": connector.PayerSubmitted,
	"under_review": connector.PayerSubmitted, "vetting": connector.PayerSubmitted,
	"flagged": connector.PayerQueried, "queried": connector.PayerQueried, "justification_required": connector.PayerQueried,
	"approved": connector.PayerApproved, "partially_approved": connector.PayerApproved,
	"rejected": connector.PayerRejected, "denied": connector.PayerRejected,
	"paid": connector.PayerPaid,
}

func (q *ClaimIQ) FetchClaim(ctx context.Context, id string) (*connector.PayerClaim, error) {
	var r struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Items  []struct {
			ID         string `json:"id"`
			TariffCode string `json:"tariff_code"`
			Quantity   any    `json:"quantity"`
			Subtotal   any    `json:"subtotal"`
			UnitCost   any    `json:"unit_cost"`
		} `json:"items"`
	}
	if err := q.do(ctx, "get claim", http.MethodGet, "/claims/"+url.PathEscape(id), nil, &r, ""); err != nil {
		return nil, err
	}
	st, ok := statuses[strings.ToLower(strings.TrimSpace(r.Status))]
	if !ok {
		st = connector.PayerUnknown
	}
	pc := &connector.PayerClaim{Number: id, Status: st, Message: r.Status}
	for _, it := range r.Items {
		qty := num(it.Quantity)
		total := num(it.Subtotal)
		if total == 0 {
			total = num(it.UnitCost) * qty
		}
		pc.Lines = append(pc.Lines, connector.PayerLine{Ref: it.ID, Code: it.TariffCode, Quantity: int64(qty + 0.5),
			Amount: int64(total*100 + 0.5)})
	}
	return pc, nil
}

// ---- helpers ----

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return f
	}
	return 0
}

func isCode(err error, code connector.ErrorCode) bool {
	return err != nil && connector.AsError(err).Code == code
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}
