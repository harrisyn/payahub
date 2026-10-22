package claimiq

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

// fake is ClaimIQ's /v1 API, built from the request and reply shapes
// core-api-v2's claimiq.py sends and reads.
type fake struct {
	t  *testing.T
	mu sync.Mutex

	claims        map[string]*fakeClaim
	byIdem        map[string]string
	batches       map[string][]string // idempotency key -> claim ids
	nextID        int
	calls         []string
	bodies        map[string][]map[string]any
	failBatchOnce bool
}

type fakeClaim struct {
	staff, status string
	data          map[string]any
	items         map[string]map[string]any
	diagnoses     []any
	confirmed     bool
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	f := &fake{t: t, claims: map[string]*fakeClaim{}, byIdem: map[string]string{}, batches: map[string][]string{},
		bodies: map[string][]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fake) id(prefix string) string { f.nextID++; return fmt.Sprintf("%s-%d", prefix, f.nextID) }

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	write := func(st int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st)
		_ = json.NewEncoder(w).Encode(v)
	}
	if r.Header.Get("X-API-Key") != "k-1" {
		write(401, map[string]any{"error": map[string]any{"message": "invalid api key"}})
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/v1")
	route := r.Method + " " + p
	f.calls = append(f.calls, route)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.bodies[route] = append(f.bodies[route], body)
	parts := strings.Split(strings.Trim(p, "/"), "/")

	switch {
	case route == "POST /patients/verify":
		switch body["query"] {
		case "NOPE":
			write(200, map[string]any{"patients": []any{}, "staff": []any{}})
		case "OLD-1":
			write(200, map[string]any{"patients": []any{map[string]any{"member_id": "p-old", "member_number": "OLD-1", "is_active": false}}})
		default:
			write(200, map[string]any{"patients": []any{map[string]any{"member_id": "p-sena", "member_type": "principal",
				"member_number": body["query"], "first_name": "Sena", "last_name": "Sela", "date_of_birth": "1985-09-03",
				"gender": "female", "is_active": true}}})
		}
	case route == "POST /claims":
		key := r.Header.Get("Idempotency-Key")
		if id, ok := f.byIdem[key]; ok && key != "" {
			write(201, map[string]any{"id": id, "claim_number": "CLQ-" + id, "status": "PENDING"})
			return
		}
		items, _ := body["items"].([]any)
		if len(items) == 0 {
			write(400, map[string]any{"message": "items must contain at least 1 elements"})
			return
		}
		id := f.id("claim")
		c := &fakeClaim{staff: body["staff_id"].(string), status: "pending", data: body["claim_data"].(map[string]any), items: map[string]map[string]any{}}
		for _, it := range items {
			c.items[f.id("item")] = it.(map[string]any)
		}
		c.diagnoses, _ = body["diagnoses"].([]any)
		f.claims[id] = c
		f.byIdem[key] = id
		write(201, map[string]any{"id": id, "claim_number": "CLQ-" + id, "status": "PENDING"})
	case route == "POST /patients/check-in":
		if body["create_claim"] != true || body["membership_number"] == nil || body["manual"] != true {
			f.t.Errorf("check-in %v", body)
		}
		id := f.id("claim")
		f.claims[id] = &fakeClaim{status: "pending", items: map[string]map[string]any{}}
		write(200, map[string]any{"claim": map[string]any{"id": id, "claim_number": "CLQ-" + id, "status": "PENDING"}})
	case route == "POST /batches":
		if f.failBatchOnce {
			f.failBatchOnce = false
			write(503, map[string]any{"message": "try later"})
			return
		}
		key := r.Header.Get("Idempotency-Key")
		var ids []string
		for _, x := range body["claim_ids"].([]any) {
			ids = append(ids, x.(string))
		}
		if prev, ok := f.batches[key]; ok {
			write(200, map[string]any{"id": "batch-" + key, "claim_count": len(prev)})
			return
		}
		f.batches[key] = ids
		for _, id := range ids {
			f.claims[id].status = "batched"
		}
		write(201, map[string]any{"id": "batch-" + key, "batch_number": "B-1", "claim_count": len(ids)})
	case len(parts) >= 2 && parts[0] == "claims":
		c := f.claims[parts[1]]
		if c == nil {
			write(404, map[string]any{"message": "claim not found"})
			return
		}
		switch {
		case r.Method == "GET" && len(parts) == 2:
			var items []any
			for id, it := range c.items {
				items = append(items, map[string]any{"id": id, "tariff_code": it["tariff_code"], "quantity": fmt.Sprint(it["quantity"]),
					"unit_cost": fmt.Sprint(it["unit_cost"]), "subtotal": fmt.Sprintf("%.2f", it["quantity"].(float64)*it["unit_cost"].(float64))})
			}
			write(200, map[string]any{"id": parts[1], "status": c.status, "items": items, "diagnoses": c.diagnoses})
		case r.Method == "PATCH" && len(parts) == 2:
			if c.status == "batched" {
				write(409, map[string]any{"message": "claim already batched"})
				return
			}
			if d, ok := body["claim_data"].(map[string]any); ok {
				c.data = d
			}
			if d, ok := body["diagnoses"].([]any); ok {
				c.diagnoses = d
			}
			write(200, map[string]any{"id": parts[1]})
		case r.Method == "POST" && len(parts) == 3 && parts[2] == "items":
			var out []any
			for _, it := range body["items"].([]any) {
				id := f.id("item")
				c.items[id] = it.(map[string]any)
				out = append(out, map[string]any{"id": id, "tariff_code": it.(map[string]any)["tariff_code"]})
			}
			write(201, map[string]any{"claim_id": parts[1], "items": out})
		case len(parts) == 4 && parts[2] == "items":
			it, ok := c.items[parts[3]]
			if !ok {
				write(404, map[string]any{"message": "item not found"})
				return
			}
			if r.Method == "DELETE" {
				delete(c.items, parts[3])
				w.WriteHeader(204)
				return
			}
			for k, v := range body {
				it[k] = v
			}
			write(200, map[string]any{"claim_id": parts[1], "item": it})
		case r.Method == "POST" && len(parts) == 3 && parts[2] == "diagnoses":
			c.diagnoses = append(c.diagnoses, body["diagnoses"].([]any)...)
			write(201, map[string]any{"diagnoses": []any{map[string]any{"id": f.id("dx")}}})
		case r.Method == "POST" && len(parts) == 3 && parts[2] == "confirm":
			c.confirmed = true
			write(200, map[string]any{"id": parts[1], "status": "confirmed"})
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func account(srv *httptest.Server) connector.Account {
	return connector.Account{Credentials: map[string]string{"base_url": srv.URL + "/api", "api_key": "k-1"}, PayerID: "ciq"}
}

func open(t *testing.T, srv *httptest.Server) *ClaimIQ {
	t.Helper()
	c, err := connector.New(Key, account(srv))
	if err != nil {
		t.Fatal(err)
	}
	return c.(*ClaimIQ).WithClient(srv.Client())
}

func sampleClaim() *claim.Claim {
	return &claim.Claim{ID: "clm_1", Input: claim.Input{
		PayerID: "ciq", Currency: "GHS",
		Member:    claim.Member{MemberNumber: "MBR-000123"},
		Provider:  claim.Provider{Name: "Ridge Clinic"},
		Encounter: claim.Encounter{Type: claim.Outpatient, StartDate: claim.NewDate(2026, 10, 20)},
		Lines: []claim.Line{
			{ID: "l1", Description: "Doctors fees", Quantity: 1, UnitPrice: 17900,
				Mapping: &claim.CodeMapping{Target: &claim.Code{Value: "tariff-uuid-1"}}},
			{ID: "l2", Description: "Paracetamol", Category: claim.CategoryDrug, Quantity: 3, UnitPrice: 333,
				Code: &claim.Code{System: "org", Value: "PARA"}},
		},
		Diagnoses: []claim.Diagnosis{{Code: claim.Code{System: "icd-10", Value: "B53.1"}, Role: claim.Primary,
			Description: "Malaria due to simian plasmodia"}},
	}}
}

func send(t *testing.T, c connector.Connector, cl *claim.Claim, cfg connector.SubmissionConfig, p *engine.Progress) (connector.Receipt, error) {
	t.Helper()
	return engine.Send(context.Background(), c, engine.Build(cl, cfg), engine.ChoosePlan(c.Manifest(), cfg), p,
		func(context.Context, *engine.Progress) error { return nil })
}

func TestWholeClaimIsFiledAndBatched(t *testing.T) {
	f, srv := newFake(t)
	q := open(t, srv)
	r, err := send(t, q, sampleClaim(), manifest.Submission, &engine.Progress{})
	if err != nil || !r.Acknowledged || r.PayerClaimNumber != "claim-1" {
		t.Fatalf("receipt %+v %v", r, err)
	}
	body := f.bodies["POST /claims"][0]
	if body["staff_id"] != "p-sena" || body["method"] != "manual" || body["external_ref"] != "clm_1" {
		t.Errorf("claim body %v", body)
	}
	data := body["claim_data"].(map[string]any)
	if data["service_class"] != "episode" || data["visit_type"] != "outpatient" || data["date_of_visit"] != "2026-10-20" || data["hospital_name"] != "Ridge Clinic" {
		t.Errorf("claim_data %v", data)
	}
	items := body["items"].([]any)
	doc, para := items[0].(map[string]any), items[1].(map[string]any)
	if doc["tariff_code"] != "tariff-uuid-1" || doc["unit_cost"] != 179.0 || doc["category"] != "service" {
		t.Errorf("mapped item %v", doc)
	}
	if _, has := para["tariff_code"]; has || para["unit_cost"] != 3.33 || para["category"] != "drug" || para["quantity"] != 3.0 {
		t.Errorf("unmapped item goes without a tariff code %v", para)
	}
	dx := body["diagnoses"].([]any)[0].(map[string]any)
	if dx["icd10_code"] != "B53.1" || dx["is_primary"] != true || dx["diagnosis_text"] != "Malaria due to simian plasmodia" {
		t.Errorf("diagnosis %v", dx)
	}
	if f.claims["claim-1"].status != "batched" {
		t.Errorf("the claim must be batched")
	}
}

func TestRetryAfterLostBatchReusesClaimAndBatch(t *testing.T) {
	f, srv := newFake(t)
	q := open(t, srv)
	f.failBatchOnce = true
	if _, err := send(t, q, sampleClaim(), manifest.Submission, &engine.Progress{}); connector.AsError(err).Code != connector.Unknown {
		t.Fatalf("want a retryable failure, got %v", err)
	}
	r, err := send(t, q, sampleClaim(), manifest.Submission, &engine.Progress{})
	if err != nil || r.PayerClaimNumber != "claim-1" || len(f.claims) != 1 {
		t.Fatalf("retry: %+v %v claims=%d", r, err, len(f.claims))
	}
	if _, err := send(t, q, sampleClaim(), manifest.Submission, &engine.Progress{}); err != nil || len(f.batches) != 1 {
		t.Errorf("a third send is still one batch: %v %d", err, len(f.batches))
	}
}

func TestIncrementalClaim(t *testing.T) {
	f, srv := newFake(t)
	q := open(t, srv)
	cfg := manifest.Submission
	cfg.LineSubmissionMode, cfg.ClaimCreationTiming = "realtime", "early"
	c := sampleClaim()
	var p engine.Progress
	r, err := send(t, q, c, cfg, &p)
	if err != nil || !r.Acknowledged {
		t.Fatalf("receipt %+v %v", r, err)
	}
	held := f.claims[r.PayerClaimNumber]
	if len(held.items) != 2 || len(held.diagnoses) != 1 || !held.confirmed || held.status != "batched" || held.data["service_class"] != "episode" {
		t.Fatalf("claim %+v", held)
	}
	if f.bodies["POST /patients/check-in"][0]["membership_number"] != "MBR-000123" {
		t.Errorf("check-in names the member by membership number")
	}
}

func TestIncrementalCorrectionAndResume(t *testing.T) {
	f, srv := newFake(t)
	q := open(t, srv)
	cfg := manifest.Submission
	cfg.LineSubmissionMode, cfg.ClaimCreationTiming = "realtime", "early"
	c := sampleClaim()
	p := engine.Progress{}
	// Open and add the lines, then stop before finalising (as a crash would).
	s := engine.Build(c, cfg)
	n, _ := q.OpenClaim(context.Background(), s)
	p.PayerClaimNumber = n
	p.Lines = map[string]engine.SentLine{}
	ref, _ := q.AddLine(context.Background(), s, n, s.Lines[0])
	p.Lines[s.Lines[0].Key] = engine.SentLine{Ref: ref, Code: s.Lines[0].Code, Quantity: 1, Amount: 17900}
	// The biller doubles the fee before it goes.
	c.Lines[0].Quantity = 2
	r, err := send(t, q, c, cfg, &p)
	if err != nil {
		t.Fatal(err)
	}
	held := f.claims[r.PayerClaimNumber]
	if len(held.items) != 2 || held.items[ref]["quantity"] != 2.0 || len(f.claims) != 1 {
		t.Errorf("updated in place: %v", held.items)
	}
}

func TestDispenseOnlyClaim(t *testing.T) {
	f, srv := newFake(t)
	q := open(t, srv)
	c := sampleClaim()
	c.Lines = c.Lines[1:]
	c.Diagnoses = nil
	if _, err := send(t, q, c, manifest.Submission, &engine.Progress{}); err != nil {
		t.Fatal(err)
	}
	if got := f.bodies["POST /claims"][0]["claim_data"].(map[string]any)["service_class"]; got != "dispense" {
		t.Errorf("medicines only, no diagnosis: %v", got)
	}
}

func TestMembers(t *testing.T) {
	_, srv := newFake(t)
	q := open(t, srv)
	ctx := context.Background()
	v, err := q.VerifyMember(ctx, "MBR-000123", "")
	if err != nil || !v.Eligible || v.Member.FirstName != "Sena" || v.Member.BirthDate.String() != "1985-09-03" {
		t.Errorf("member %+v %v", v, err)
	}
	if v, _ := q.VerifyMember(ctx, "OLD-1", ""); v.Eligible || v.Reason != "inactive" {
		t.Errorf("inactive %+v", v)
	}
	if _, err := q.VerifyMember(ctx, "NOPE", ""); connector.AsError(err).Code != connector.MemberNotFound {
		t.Errorf("missing %v", err)
	}
	c := sampleClaim()
	c.Member.MemberNumber = "NOPE"
	if _, err := send(t, q, c, manifest.Submission, &engine.Progress{}); connector.AsError(err).Code != connector.MemberNotFound {
		t.Errorf("a claim for an unknown member: %v", err)
	}
}

func TestBadKeyAndBaseURL(t *testing.T) {
	_, srv := newFake(t)
	a := account(srv)
	a.Credentials["api_key"] = "nope"
	c, _ := connector.New(Key, a)
	if _, err := c.(*ClaimIQ).WithClient(srv.Client()).VerifyMember(context.Background(), "X", ""); connector.AsError(err).Code != connector.InvalidCredentials {
		t.Errorf("bad key %v", err)
	}
	a = account(srv)
	a.Credentials["base_url"] = srv.URL + "/api/v1/"
	if c, _ := connector.New(Key, a); c.(*ClaimIQ).api != srv.URL+"/api/v1" {
		t.Errorf("a root ending in /v1 is kept: %s", c.(*ClaimIQ).api)
	}
}
