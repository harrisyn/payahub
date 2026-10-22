package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/store"
)

// Operator routes let the people who run Payahub edit the shared catalogue:
// add a payer, say which platform reaches it and its settings there, load the
// payer's own code list, and set the payer's code for each standard item.
// Only a signed-in operator reaches them; every change is logged.
func (s *Server) operatorRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/operator/payers", s.operator(s.opListPayers))
	mux.Handle("PUT /v1/operator/payers/{id}", s.operator(s.opPutPayer))
	mux.Handle("GET /v1/operator/payers/{id}/codes", s.operator(s.opPayerCodes))
	mux.Handle("PUT /v1/operator/payers/{id}/codes", s.operator(s.opPutCodes))
	mux.Handle("POST /v1/operator/payers/{id}/codes/accept", s.operator(s.opAcceptSuggestions))
	mux.Handle("PUT /v1/operator/payers/{id}/list", s.operator(s.opPutList))
	mux.Handle("GET /v1/operator/payers/{id}/changes", s.operator(s.opChanges))
}

func (s *Server) operator(next http.HandlerFunc) http.Handler {
	return s.session(func(w http.ResponseWriter, r *http.Request) {
		if !orgFrom(r.Context()).Operator {
			writeError(w, http.StatusForbidden, "operator_required", "Only Payahub operators can change the shared catalogue.", nil)
			return
		}
		next(w, r)
	})
}

type opPayerView struct {
	payerView
	Fields        map[string]string `json:"fields"`
	Inactive      bool              `json:"inactive"`
	MissingFields []string          `json:"missing_fields"`
	ListSize      int               `json:"list_size"`
	TotalItems    int               `json:"total_items"`
}

func viewOpPayer(p catalog.Payer) opPayerView {
	v := opPayerView{payerView: viewPayer(p), Fields: p.Fields, Inactive: p.Inactive, MissingFields: []string{},
		ListSize: len(catalog.PayerList(p.ID)), TotalItems: len(catalog.Items(""))}
	if v.Fields == nil {
		v.Fields = map[string]string{}
	}
	if m, _, ok := connector.Lookup(p.Connector); ok {
		for _, f := range m.PayerFields {
			if f.Required && strings.TrimSpace(p.Fields[f.Name]) == "" {
				v.MissingFields = append(v.MissingFields, f.Name)
			}
		}
	}
	return v
}

func (s *Server) opListPayers(w http.ResponseWriter, _ *http.Request) {
	out := []opPayerView{}
	for _, p := range catalog.AllPayers() {
		out = append(out, viewOpPayer(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

var payerIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{1,39}$`)

func (s *Server) opPutPayer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var in struct {
		Name      string            `json:"name"`
		Country   string            `json:"country"`
		Connector string            `json:"connector"`
		Fields    map[string]string `json:"fields"`
		Notes     string            `json:"notes"`
		Inactive  bool              `json:"inactive"`
	}
	if !decode(w, r, &in) {
		return
	}
	p := catalog.Payer{ID: id, Name: strings.TrimSpace(in.Name), Country: strings.ToUpper(strings.TrimSpace(in.Country)),
		Connector: strings.TrimSpace(in.Connector), Notes: strings.TrimSpace(in.Notes), Inactive: in.Inactive,
		Fields: map[string]string{}}
	problems := map[string]string{}
	if !payerIDRe.MatchString(id) {
		problems["id"] = "Use 2 to 40 lowercase letters, digits or underscores, like glico or acacia_health."
	}
	if p.Name == "" {
		problems["name"] = "Enter the payer's name."
	}
	if len(p.Country) != 2 {
		problems["country"] = "Use a two-letter country code, like GH."
	}
	m, _, ok := connector.Lookup(p.Connector)
	if !ok {
		problems["connector"] = "Choose how the payer is reached."
	}
	for k, v := range in.Fields {
		if v = strings.TrimSpace(v); v == "" {
			continue
		}
		if ok && !slices.ContainsFunc(m.PayerFields, func(f connector.CredentialField) bool { return f.Name == k }) {
			problems["fields."+k] = m.Name + " has no setting called " + k + "."
			continue
		}
		p.Fields[k] = v
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_payer", "Some details need fixing.", problems)
		return
	}
	if old, exists := catalog.LookupPayer(id); exists {
		// Kept from the seed; operators do not set them.
		p.GeneratedCodes = old.GeneratedCodes
	}
	created, err := s.Store.SavePayer(r.Context(), p, orgFrom(r.Context()).UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	saved, _ := catalog.LookupPayer(id)
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, viewOpPayer(saved))
}

func (s *Server) opPayer(w http.ResponseWriter, r *http.Request) (catalog.Payer, bool) {
	p, ok := catalog.LookupPayer(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_payer", "No payer '"+r.PathValue("id")+"' in the catalogue.", nil)
	}
	return p, ok
}

type itemCode struct {
	Item        catalog.Item             `json:"item"`
	Code        *catalog.PayerCode       `json:"code"`
	InList      *bool                    `json:"in_list,omitempty"` // nil when the payer has no list loaded
	Generated   bool                     `json:"generated,omitempty"`
	Suggestions []catalog.CodeSuggestion `json:"suggestions"`
}

// opPayerCodes is every standard item with the payer's code for it, and for
// items without one, the best matches from the payer's own list.
func (s *Server) opPayerCodes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	hasList := len(catalog.PayerList(p.ID)) > 0
	out := []itemCode{}
	coded := map[catalog.ItemKind][2]int{}
	for _, it := range catalog.Items(catalog.ItemKind(r.URL.Query().Get("kind"))) {
		row := itemCode{Item: it, Suggestions: []catalog.CodeSuggestion{}}
		c := coded[it.Kind]
		c[1]++
		if pc, ok := catalog.CodeFor(p.ID, it.ID); ok {
			row.Code = &pc
			row.Generated = p.GeneratedCodes != ""
			c[0]++
			if hasList {
				_, in := catalog.ListEntryFor(p.ID, pc.Code)
				row.InList = &in
			}
		} else if hasList {
			row.Suggestions = catalog.SuggestPayerCodes(p.ID, it.ID, 3)
		}
		coded[it.Kind] = c
		out = append(out, row)
	}
	byKind := map[string]map[string]int{}
	for k, c := range coded {
		byKind[string(k)] = map[string]int{"coded": c[0], "total": c[1]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "coverage": byKind, "list_size": len(catalog.PayerList(p.ID))})
}

func (s *Server) opPutCodes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	var in struct {
		Changes []store.CodeChange `json:"changes"`
	}
	if !decode(w, r, &in) {
		return
	}
	s.applyCodes(w, r, p, in.Changes)
}

func (s *Server) applyCodes(w http.ResponseWriter, r *http.Request, p catalog.Payer, changes []store.CodeChange) {
	if len(changes) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no_changes", "Send at least one change.", nil)
		return
	}
	problems := map[string]string{}
	var warnings []string
	for i := range changes {
		c := &changes[i]
		c.Code, c.Description = strings.TrimSpace(c.Code), strings.TrimSpace(c.Description)
		if _, ok := catalog.LookupItem(c.ItemID); !ok {
			problems[c.ItemID] = "No standard item with this id."
			continue
		}
		if len(c.Code) > 64 {
			problems[c.ItemID] = "Codes are at most 64 characters."
			continue
		}
		if c.Code == "" {
			continue
		}
		if e, ok := catalog.ListEntryFor(p.ID, c.Code); ok {
			if c.Description == "" {
				c.Description = e.Name
			}
		} else if len(catalog.PayerList(p.ID)) > 0 {
			warnings = append(warnings, c.Code+" is not in "+p.Name+"'s code list.")
		}
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_codes", "Some codes need fixing.", problems)
		return
	}
	if err := s.Store.SetPayerCodes(r.Context(), p.ID, changes, orgFrom(r.Context()).UserID); err != nil {
		s.internal(w, r, err)
		return
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": len(changes), "warnings": warnings,
		"coded_items": catalog.CodedItems(p.ID)})
}

// opAcceptSuggestions takes the best match from the payer's list for every
// item without a code, where the match is at least min_confidence (0.9 by
// default) and clearly ahead of the next one. The operator asks for it, so a
// person stands behind each code it sets.
func (s *Server) opAcceptSuggestions(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	var in struct {
		MinConfidence float64 `json:"min_confidence"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.MinConfidence == 0 {
		in.MinConfidence = 0.9
	}
	if in.MinConfidence < 0.5 || in.MinConfidence > 1 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_threshold", "Use a confidence between 0.5 and 1.", nil)
		return
	}
	var changes []store.CodeChange
	for _, it := range catalog.Items("") {
		if _, ok := catalog.CodeFor(p.ID, it.ID); ok {
			continue
		}
		sg := catalog.SuggestPayerCodes(p.ID, it.ID, 2)
		if len(sg) == 0 || sg[0].Confidence < in.MinConfidence || (len(sg) > 1 && sg[0].Confidence-sg[1].Confidence < 0.1) {
			continue
		}
		changes = append(changes, store.CodeChange{ItemID: it.ID, Code: sg[0].Code, Description: sg[0].Name})
	}
	if len(changes) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"applied": 0, "warnings": []string{}, "coded_items": catalog.CodedItems(p.ID)})
		return
	}
	s.applyCodes(w, r, p, changes)
}

// opPutList loads the payer's own code list from CSV with a header row:
// code and name are required; group and price (in major units) optional.
func (s *Server) opPutList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	var in struct {
		CSV string `json:"csv"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	entries, skipped, err := parseList(in.CSV)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_csv", err.Error(), nil)
		return
	}
	if err := s.Store.ReplacePayerList(r.Context(), p.ID, entries, orgFrom(r.Context()).UserID); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": len(entries), "skipped": skipped})
}

type skippedRow struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

func parseList(text string) ([]catalog.ListEntry, []skippedRow, error) {
	rd := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	rd.FieldsPerRecord = -1
	rd.TrimLeadingSpace = true
	header, err := rd.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil, errors.New("the file is empty")
	}
	if err != nil {
		return nil, nil, err
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	pick := func(names ...string) int {
		for _, n := range names {
			if i, ok := col[n]; ok {
				return i
			}
		}
		return -1
	}
	ci, ni := pick("code", "item code", "product id", "tariff code"), pick("name", "description", "item", "product name")
	gi, pi := pick("group", "category", "type"), pick("price", "tariff", "unit price", "amount")
	if ci < 0 || ni < 0 {
		return nil, nil, errors.New("the first row must name the columns, with at least code and name")
	}
	var entries []catalog.ListEntry
	skipped := []skippedRow{}
	seen := map[string]bool{}
	for line := 2; ; line++ {
		rec, err := rd.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		get := func(i int) string {
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		e := catalog.ListEntry{Code: get(ci), Name: get(ni), Group: get(gi)}
		switch {
		case e.Code == "" && e.Name == "":
			continue
		case e.Code == "":
			skipped = append(skipped, skippedRow{line, "no code"})
			continue
		case len(e.Code) > 64:
			skipped = append(skipped, skippedRow{line, "code longer than 64 characters"})
			continue
		case seen[e.Code]:
			skipped = append(skipped, skippedRow{line, "code " + e.Code + " repeats an earlier row"})
			continue
		}
		if raw := strings.ReplaceAll(get(pi), ",", ""); raw != "" {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil || f < 0 {
				skipped = append(skipped, skippedRow{line, "price " + get(pi) + " is not a number"})
				continue
			}
			minor := int64(f*100 + 0.5)
			e.Price = &minor
		}
		seen[e.Code] = true
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, skipped, errors.New("no rows with a code")
	}
	return entries, skipped, nil
}

func (s *Server) opChanges(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	out, err := s.Store.PayerChanges(r.Context(), p.ID, 50)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
