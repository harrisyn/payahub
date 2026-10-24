package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/assist"
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
	mux.Handle("POST /v1/operator/payers/import", s.operator(s.opImportPayers))
	mux.Handle("PUT /v1/operator/payers/{id}", s.operator(s.opPutPayer))
	mux.Handle("GET /v1/operator/payers/{id}/codes", s.operator(s.opPayerCodes))
	mux.Handle("PUT /v1/operator/payers/{id}/codes", s.operator(s.opPutCodes))
	mux.Handle("POST /v1/operator/payers/{id}/codes/accept", s.operator(s.opAcceptSuggestions))
	mux.Handle("POST /v1/operator/payers/{id}/codes/model", s.operator(s.opModelCodes))
	mux.Handle("PUT /v1/operator/payers/{id}/list", s.operator(s.opPutList))
	mux.Handle("PUT /v1/operator/payers/{id}/rules", s.operator(s.opPutRules))
	mux.Handle("GET /v1/operator/payers/{id}/list", s.operator(s.opGetList))
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
	ListRules     int               `json:"list_rules"` // codes in the list with a rule beyond price
	TotalItems    int               `json:"total_items"`
}

func viewOpPayer(p catalog.Payer) opPayerView {
	v := opPayerView{payerView: viewPayer(p), Fields: p.Fields, Inactive: p.Inactive, MissingFields: []string{},
		ListSize: len(catalog.PayerList(p.ID)), TotalItems: len(catalog.Items(""))}
	if v.Fields == nil {
		v.Fields = map[string]string{}
	}
	for _, e := range catalog.PayerList(p.ID) {
		if e.Limited() {
			v.ListRules++
		}
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
	// ModelPick is a model's proposal for an item without a code.
	ModelPick *store.CodePick `json:"model_pick,omitempty"`
}

// opPayerCodes is every standard item with the payer's code for it, and for
// items without one, the best matches from the payer's own list.
func (s *Server) opPayerCodes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	hasList := len(catalog.PayerList(p.ID)) > 0
	picks, err := s.Store.CodePicks(r.Context(), p.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
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
			if mp, ok := picks[it.ID]; ok {
				row.ModelPick = &mp
			}
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
		Source        string  `json:"source"` // words (the default) or model
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Source != "" && in.Source != "words" && in.Source != "model" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_source", "Use words or model.", nil)
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
	var picks map[string]store.CodePick
	if in.Source == "model" {
		var err error
		if picks, err = s.Store.CodePicks(r.Context(), p.ID); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	for _, it := range catalog.Items("") {
		if _, ok := catalog.CodeFor(p.ID, it.ID); ok {
			continue
		}
		if in.Source == "model" {
			// The model's pick, still in the payer's list.
			mp, ok := picks[it.ID]
			if !ok || mp.Code == "" || mp.Confidence < in.MinConfidence {
				continue
			}
			if e, ok := catalog.ListEntryFor(p.ID, mp.Code); ok {
				changes = append(changes, store.CodeChange{ItemID: it.ID, Code: e.Code, Description: e.Name})
			}
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
// code and name are required; group, price (in major units) and the payer's
// rules for each code (max quantity, sex, min age, max age, authorisation)
// optional.
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

// opGetList returns the payer's own list, at most 500 codes; ?limited=true
// keeps the codes that carry a rule beyond their price.
func (s *Server) opGetList(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	limited := r.URL.Query().Get("limited") == "true"
	all := catalog.PayerList(p.ID)
	out := []catalog.ListEntry{}
	n := 0
	for _, e := range all {
		if limited && !e.Limited() {
			continue
		}
		n++
		if len(out) < 500 {
			out = append(out, e)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "total": n})
}

// opPutRules sets whether a price above the payer's tariff, or a code outside
// its list, stops a claim (block) or only warns (warn).
func (s *Server) opPutRules(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	var in catalog.PayerRules
	if !decode(w, r, &in) {
		return
	}
	for _, v := range []string{in.PricePolicy, in.UnlistedPolicy} {
		if v != "warn" && v != "block" {
			writeError(w, http.StatusUnprocessableEntity, "invalid_rules", "price_policy and unlisted_policy are warn or block.", nil)
			return
		}
	}
	if err := s.Store.SavePayerRules(r.Context(), p.ID, in, orgFrom(r.Context()).UserID); err != nil {
		s.internal(w, r, err)
		return
	}
	np, _ := catalog.LookupPayer(p.ID)
	writeJSON(w, http.StatusOK, viewOpPayer(np))
}

// listRules reads a list row's rules into e, or says why the row cannot be
// used. Blank cells set nothing.
func listRules(e *catalog.ListEntry, qty, sex, minAge, maxAge, auth string) string {
	if qty != "" {
		n, err := strconv.ParseInt(qty, 10, 64)
		if err != nil || n < 1 {
			return "max quantity " + qty + " is not a whole number of 1 or more"
		}
		e.MaxQuantity = &n
	}
	switch strings.ToLower(sex) {
	case "", "any", "all", "both":
	case "f", "female", "women", "woman":
		e.Sex = "female"
	case "m", "male", "men", "man":
		e.Sex = "male"
	default:
		return "sex " + sex + " is not female, male or blank"
	}
	for _, a := range []struct {
		raw string
		to  **int
		at  string
	}{{minAge, &e.MinAge, "min age"}, {maxAge, &e.MaxAge, "max age"}} {
		if a.raw == "" {
			continue
		}
		n, err := strconv.Atoi(a.raw)
		if err != nil || n < 0 || n > 150 {
			return a.at + " " + a.raw + " is not an age in years"
		}
		*a.to = &n
	}
	if e.MinAge != nil && e.MaxAge != nil && *e.MinAge > *e.MaxAge {
		return "min age is above max age"
	}
	switch strings.ToLower(auth) {
	case "", "no", "n", "false", "0":
	case "yes", "y", "true", "1", "required":
		e.NeedsAuthorization = true
	default:
		return "authorisation " + auth + " is not yes or no"
	}
	return ""
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
	qi := pick("max quantity", "max_quantity", "max qty", "quantity limit", "limit")
	si := pick("sex", "gender")
	loi, hii := pick("min age", "min_age", "minimum age"), pick("max age", "max_age", "maximum age")
	ai := pick("needs authorization", "needs_authorization", "needs authorisation", "authorization", "authorisation",
		"prior authorization", "prior authorisation")
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
		if why := listRules(&e, get(qi), get(si), get(loi), get(hii), get(ai)); why != "" {
			skipped = append(skipped, skippedRow{line, why})
			continue
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

// opModelCodes asks the code-matching model for the payer's code of each
// standard item that has none (or of the items named), from the payer's own
// list, and keeps the picks for an operator to accept.
func (s *Server) opModelCodes(w http.ResponseWriter, r *http.Request) {
	p, ok := s.opPayer(w, r)
	if !ok {
		return
	}
	var in struct {
		ItemIDs []string         `json:"item_ids"`
		Kind    catalog.ItemKind `json:"kind"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !catalog.HasList(p.ID) {
		writeError(w, http.StatusConflict, "no_list", "Load "+p.Name+"'s code list first; the model picks only from it.", nil)
		return
	}
	asked := in.ItemIDs
	if len(asked) == 0 {
		for _, it := range catalog.Items(in.Kind) {
			if _, ok := catalog.CodeFor(p.ID, it.ID); !ok {
				asked = append(asked, it.ID)
			}
		}
	}
	if len(asked) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"asked": 0, "found": 0, "data": []store.CodePick{}})
		return
	}
	if len(asked) > 1000 {
		writeError(w, http.StatusUnprocessableEntity, "too_many_items", "Ask about at most 1,000 items at a time.", nil)
		return
	}
	rt, err := s.Store.AIRouter(r.Context(), s.Vault, ai.TaskCodeMatching)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if len(rt.Providers) == 0 {
		writeError(w, http.StatusConflict, "no_model", "No model is set up for code matching. Add one under AI models.", nil)
		return
	}
	extendWrite(w, 5*time.Minute+10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	org := orgFrom(r.Context())
	got, runs := assist.MatchPayerCodes(ctx, rt, p.ID, asked)
	s.recordRuns(ctx, org.ID, runs)
	if len(got) == 0 {
		for _, run := range runs {
			if run.Err != nil {
				s.Logger.WarnContext(r.Context(), "model payer codes", "payer", p.ID, "err", run.Err)
				writeError(w, http.StatusBadGateway, "model_failed", "The model could not answer. Try again, or use the word matches.", nil)
				return
			}
		}
	}
	picks := make([]store.CodePick, 0, len(got))
	answered := make([]string, 0, len(got))
	found := 0
	for _, id := range asked {
		g, ok := got[id]
		if !ok {
			continue
		}
		answered = append(answered, id)
		cp := store.CodePick{ItemID: id, Confidence: g.Confidence, Reason: g.Reason, Model: g.Evidence.ModelVersion, PromptVersion: g.Evidence.PromptVersion}
		if g.Found {
			cp.Code, cp.Name = g.Entry.Code, g.Entry.Name
			found++
		}
		picks = append(picks, cp)
	}
	// Only the items the model answered lose their old pick.
	if err := s.Store.SaveCodePicks(r.Context(), p.ID, answered, picks, org.UserID); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asked": len(asked), "answered": len(answered), "found": found, "data": picks})
}
