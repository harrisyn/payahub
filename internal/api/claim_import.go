package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/icd10"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/sheet"
	"github.com/harrisyn/payahub/internal/store"
)

// Claims from a spreadsheet: one row per claim line, rows with the same
// claim reference make one claim (its member, payer, date and diagnoses
// come from its first row). Every claim is built and checked exactly as
// POST /v1/claims would; with dry_run nothing is saved. Saving is safe to
// repeat: each claim's Idempotency-Key comes from the organisation, the
// file and the claim reference, so uploading the same file twice creates
// nothing new.

const maxImportClaims = 2000

type importedClaim struct {
	Reference string             `json:"reference"`
	Rows      []int              `json:"rows"`
	PayerID   string             `json:"payer_id,omitempty"`
	Member    string             `json:"member_number,omitempty"`
	Lines     int                `json:"lines"`
	Total     int64              `json:"total"`
	Currency  string             `json:"currency,omitempty"`
	Errors    []claim.FieldError `json:"errors"`
	Issues    []engine.Issue     `json:"issues"`
	State     claim.State        `json:"state,omitempty"` // where it would land, or landed
	ClaimID   string             `json:"claim_id,omitempty"`
	Replayed  bool               `json:"replayed,omitempty"`
	Queued    bool               `json:"queued,omitempty"`
	input     *claim.Input
	rowsSHA   hash.Hash // over the claim's cells as read, so a re-upload matches
}

func (s *Server) importClaims(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File   string `json:"file"`
		DryRun bool   `json:"dry_run"`
		Submit bool   `json:"submit"` // queue every claim that passes its checks
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	data, err := base64.StdEncoding.DecodeString(in.File)
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_file", "Send the file as base64.", nil)
		return
	}
	ctx := r.Context()
	org := orgFrom(ctx)
	orgName, err := s.Store.OrgName(ctx, org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	claims, err := readClaimFile(data, orgName)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_file", err.Error(), nil)
		return
	}
	fileSHA := sha256.Sum256(data)
	ready := 0
	for i := range claims {
		ic := &claims[i]
		if len(ic.Errors) > 0 {
			continue
		}
		if err := s.checkImported(r, ic, fileSHA[:], in.DryRun, in.Submit); err != nil {
			s.internal(w, r, err)
			return
		}
		if len(ic.Errors) == 0 && ic.State == claim.Ready {
			ready++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"claims": claims, "ready": ready, "dry_run": in.DryRun})
}

// checkImported builds the claim as POST /v1/claims does, validates it and,
// unless dryRun, saves it (and queues it when submit is set and it passes).
func (s *Server) checkImported(r *http.Request, ic *importedClaim, fileSHA []byte, dryRun, submit bool) error {
	ctx := r.Context()
	org := orgFrom(ctx)
	in := ic.input
	given := make([]bool, len(in.Lines))
	for i, l := range in.Lines {
		given[i] = l.UnitPrice >= 0 // -1 marks a line with no price in the file
		if l.UnitPrice < 0 {
			in.Lines[i].UnitPrice = 0
		}
	}
	in.Normalize(func() string { return store.NewID("ln") })
	if err := s.completeInput(ctx, org.ID, in, given); err != nil {
		return err
	}
	for i, l := range in.Lines {
		if !given[i] && l.PriceFrom == "" {
			ic.Errors = append(ic.Errors, claim.FieldError{Field: fmt.Sprintf("lines[%d].unit_price", i),
				Message: "no price, and " + lineName(l) + " is not on your price list"})
		}
	}
	if errs := in.Check(); len(errs) > 0 || len(ic.Errors) > 0 {
		ic.Errors = append(ic.Errors, errs...)
		return nil
	}
	if p, ok := catalog.LookupPayer(in.PayerID); !ok || p.Inactive {
		ic.Errors = []claim.FieldError{{Field: "payer", Message: "no payer '" + in.PayerID + "' in the catalogue"}}
		return nil
	}
	sum := ic.rowsSHA.Sum(nil)
	c := newClaim(org, in, sum)
	c.Provenance.Source = claim.SourceBatch
	m, err := queue.Prepare(ctx, s.Store.Pool, c)
	if err != nil {
		return err
	}
	overrides, err := store.PayerOverrides(ctx, s.Store.Pool, org.ID, c.PayerID, org.Environment)
	if err != nil {
		return err
	}
	v := engine.Validate(c, m, overrides)
	ic.Issues, ic.State, ic.Lines, ic.Total, ic.Currency = v.Issues, v.State, len(c.Lines), c.Total, c.Currency
	if dryRun {
		return nil
	}
	key := fmt.Sprintf("import:%x:%s", fileSHA[:12], ic.Reference)
	saved, replayed, err := s.Store.CreateClaim(ctx, c, key, sum)
	if errors.Is(err, store.ErrIdempotencyConflict) {
		ic.Errors = []claim.FieldError{{Field: "reference", Message: "this file was imported before with different rows for this claim"}}
		return nil
	}
	if err != nil {
		return err
	}
	ic.ClaimID, ic.Replayed = saved.ID, replayed
	if submit && v.State == claim.Ready && saved.State == claim.Draft {
		_, qv, err := queue.Enqueue(ctx, s.Store, s.Queue, org.ID, saved.ID)
		switch {
		case errors.Is(err, queue.ErrTrialLimit):
			ic.Issues = append(ic.Issues, engine.Issue{Code: "trial_limit", Severity: engine.SeverityWarning,
				Message: "Not sent: the trial has no live claims left."})
		case errors.Is(err, queue.ErrWalletEmpty):
			ic.Issues = append(ic.Issues, engine.Issue{Code: "wallet_empty", Severity: engine.SeverityWarning,
				Message: "Not sent: your wallet does not cover another live claim."})
		case errors.Is(err, queue.ErrNotSubmittable):
			ic.Issues = qv.Issues
		case err != nil:
			return err
		default:
			ic.Queued = true
		}
	}
	return nil
}

func readClaimFile(data []byte, orgName string) ([]importedClaim, error) {
	table, err := sheet.Read(data)
	if err != nil {
		return nil, err
	}
	if len(table) < 2 {
		return nil, errors.New("the file has no rows under its header")
	}
	h := sheet.NewHeader(table[0])
	col := map[string]int{
		"ref":       h.Col("claim", "claim reference", "claim ref", "claim id", "claim no", "reference", "invoice", "invoice no", "visit", "visit id"),
		"payer":     h.Col("payer", "payer id", "insurer", "insurance"),
		"member":    h.Col("member number", "member no", "member id", "membership number", "card number", "member"),
		"first":     h.Col("first name", "member first name"),
		"last":      h.Col("last name", "surname", "member last name"),
		"name":      h.Col("member name", "patient", "patient name", "name"),
		"date":      h.Col("date", "visit date", "service date", "encounter date", "start date", "date of service"),
		"end":       h.Col("end date", "discharge date"),
		"type":      h.Col("type", "visit type", "encounter type", "opd/ipd"),
		"provider":  h.Col("provider", "facility", "provider name"),
		"diagnosis": h.Col("diagnosis", "diagnoses", "icd", "icd10", "icd-10", "diagnosis code", "icd code"),
		"auth":      h.Col("authorization", "authorisation", "auth code", "authorization code", "authorisation code", "pre-auth"),
		"code":      h.Col("item code", "code", "service code", "drug code"),
		"desc":      h.Col("description", "item", "service", "item name", "drug"),
		"qty":       h.Col("quantity", "qty", "units"),
		"price":     h.Col("unit price", "price", "rate", "amount per unit"),
		"currency":  h.Col("currency"),
	}
	for _, need := range []string{"ref", "payer", "member", "date"} {
		if col[need] < 0 {
			return nil, fmt.Errorf("the header needs a %s column", map[string]string{
				"ref": "claim reference", "payer": "payer", "member": "member number", "date": "date"}[need])
		}
	}
	if col["code"] < 0 && col["desc"] < 0 {
		return nil, errors.New("the header needs an item code or a description column")
	}
	var claims []importedClaim
	index := map[string]int{}
	for i, rec := range table[1:] {
		row := i + 2
		get := func(k string) string { return sheet.Cell(rec, col[k]) }
		ref := get("ref")
		if ref == "" && get("code") == "" && get("desc") == "" {
			continue
		}
		if ref == "" {
			return nil, fmt.Errorf("row %d has no claim reference", row)
		}
		j, seen := index[ref]
		if !seen {
			if len(claims) == maxImportClaims {
				return nil, fmt.Errorf("the file has more than %d claims; split it", maxImportClaims)
			}
			index[ref] = len(claims)
			claims = append(claims, importedClaim{Reference: ref, Errors: []claim.FieldError{}, Issues: []engine.Issue{}, rowsSHA: sha256.New()})
			j = len(claims) - 1
			claims[j].input = claimHead(&claims[j], get, orgName, row)
		} else if p := get("payer"); p != "" && payerFor(p) != claims[j].input.PayerID {
			claims[j].Errors = append(claims[j].Errors, claim.FieldError{Field: fmt.Sprintf("row %d", row),
				Message: "a different payer from the claim's first row"})
		} else if m := get("member"); m != "" && m != claims[j].input.Member.MemberNumber {
			claims[j].Errors = append(claims[j].Errors, claim.FieldError{Field: fmt.Sprintf("row %d", row),
				Message: "a different member from the claim's first row"})
		}
		ic := &claims[j]
		ic.Rows = append(ic.Rows, row)
		cells, _ := json.Marshal(rec)
		ic.rowsSHA.Write(cells)
		if get("code") == "" && get("desc") == "" {
			continue // a row giving only claim details
		}
		l := claim.Line{Description: get("desc"), UnitPrice: -1}
		if code := get("code"); code != "" {
			system := "local"
			if _, ok := catalog.LookupItem(code); ok {
				system = catalog.System
			}
			l.Code = &claim.Code{System: system, Value: code}
		}
		if q := get("qty"); q != "" {
			n, err := strconv.ParseFloat(strings.ReplaceAll(q, ",", ""), 64)
			if err != nil || n < 1 || n != math.Trunc(n) {
				ic.Errors = append(ic.Errors, claim.FieldError{Field: fmt.Sprintf("row %d quantity", row), Message: q + " is not a whole number"})
				continue
			}
			l.Quantity = int64(n)
		}
		if p := get("price"); p != "" {
			v, err := sheet.Money(p)
			if err != nil {
				ic.Errors = append(ic.Errors, claim.FieldError{Field: fmt.Sprintf("row %d unit price", row), Message: p + " is not an amount"})
				continue
			}
			l.UnitPrice = v
		}
		if d, ok := parseDate(get("date")); ok && !d.Equal(ic.input.Encounter.StartDate.Time) {
			sd := claim.Date{Time: d}
			l.ServiceDate = &sd
		}
		ic.input.Lines = append(ic.input.Lines, l)
	}
	for i := range claims {
		ic := &claims[i]
		ic.PayerID, ic.Member = ic.input.PayerID, ic.input.Member.MemberNumber
		if len(ic.input.Lines) == 0 && len(ic.Errors) == 0 {
			ic.Errors = append(ic.Errors, claim.FieldError{Field: "lines", Message: "no row gives an item"})
		}
	}
	if len(claims) == 0 {
		return nil, errors.New("no row has a claim reference")
	}
	return claims, nil
}

// claimHead reads the claim-level fields from a claim's first row.
func claimHead(ic *importedClaim, get func(string) string, orgName string, row int) *claim.Input {
	in := &claim.Input{PayerID: payerFor(get("payer")), Currency: get("currency"),
		Provider:  claim.Provider{Name: orgName},
		Encounter: claim.Encounter{Reference: ic.Reference}, AuthorizationCode: get("auth")}
	in.Member = claim.Member{MemberNumber: get("member"), FirstName: get("first"), LastName: get("last")}
	if n := get("name"); n != "" && in.Member.FirstName == "" && in.Member.LastName == "" {
		first, last, _ := strings.Cut(n, " ")
		in.Member.FirstName, in.Member.LastName = first, strings.TrimSpace(last)
	}
	if p := get("provider"); p != "" {
		in.Provider.Name = p
	}
	switch t := strings.ToLower(get("type")); {
	case strings.HasPrefix(t, "in") || t == "ipd":
		in.Encounter.Type = claim.Inpatient
	case t != "":
		in.Encounter.Type = claim.Outpatient
	}
	add := func(field, msg string) {
		ic.Errors = append(ic.Errors, claim.FieldError{Field: fmt.Sprintf("row %d %s", row, field), Message: msg})
	}
	if raw := get("date"); raw != "" {
		if d, ok := parseDate(raw); ok {
			in.Encounter.StartDate = claim.Date{Time: d}
		} else {
			add("date", raw+" is not a date (use 2026-10-21 or 21/10/2026)")
		}
	}
	if raw := get("end"); raw != "" {
		if d, ok := parseDate(raw); ok {
			ed := claim.Date{Time: d}
			in.Encounter.EndDate = &ed
		} else {
			add("end date", raw+" is not a date")
		}
	}
	for i, code := range strings.FieldsFunc(get("diagnosis"), func(r rune) bool { return r == ';' || r == ',' || r == '|' || r == '/' }) {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		role := claim.Secondary
		if i == 0 {
			role = claim.Primary
		}
		in.Diagnoses = append(in.Diagnoses, claim.Diagnosis{Code: claim.Code{System: icd10.System, Value: code}, Role: role})
	}
	return in
}

// payerFor accepts a payer's id or its name as written.
func payerFor(s string) string {
	s = strings.TrimSpace(s)
	if _, ok := catalog.LookupPayer(s); ok {
		return s
	}
	for _, p := range catalog.AllPayers() {
		if strings.EqualFold(p.Name, s) {
			return p.ID
		}
	}
	return s // unknown: reported as written
}

// parseDate reads the dates clinics' exports carry: ISO, day-first with
// slashes or dashes, and Excel's serial day numbers.
func parseDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && n > 20000 && n < 80000 {
		return time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, int(n)), true
	}
	for _, layout := range []string{"2006-01-02", "02/01/2006", "2/1/2006", "02-01-2006", "2-1-2006", "02 Jan 2006", "2 Jan 2006", "2006-01-02 15:04:05", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), true
		}
	}
	return time.Time{}, false
}

func lineName(l claim.Line) string {
	if l.Code != nil {
		return l.Code.Value
	}
	return strconvQuote(l.Description)
}

func strconvQuote(s string) string { return strconv.Quote(s) }
