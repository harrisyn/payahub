package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/sheet"
	"github.com/harrisyn/payahub/internal/store"
)

// The price list is the organisation's own codes with names and prices. A
// claim line naming one of the codes needs no price or description of its
// own. Each code's standard item is a mapping a person confirms
// (/v1/item-mappings); the list suggests one for every unmapped code.
func (s *Server) priceRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/price-list", s.auth(s.listPrices))
	mux.Handle("PUT /v1/price-list", s.auth(s.putPrices))
	mux.Handle("POST /v1/price-list/import", s.auth(s.importPrices))
	mux.Handle("DELETE /v1/price-list/{code}", s.auth(s.deletePrice))
}

const maxPrices = 50_000

type priceView struct {
	store.Price
	Suggestion *catalog.Suggestion `json:"suggestion,omitempty"`
}

func (s *Server) listPrices(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListPrices(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]priceView, len(ps))
	mapped := 0
	for i, p := range ps {
		out[i].Price = p
		if p.ItemID != "" {
			mapped++
			continue
		}
		if sg := catalog.Suggest(p.Name, catalog.ItemKind(p.Kind), 1); len(sg) > 0 {
			out[i].Suggestion = &sg[0]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out, "mapped": mapped})
}

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// priceInput is one line as sent: price in minor units.
type priceInput struct {
	LocalCode string `json:"local_code"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	UnitPrice *int64 `json:"unit_price"`
	Currency  string `json:"currency"`
}

func checkPrice(in priceInput, defaultCurrency string) (store.Price, string) {
	p := store.Price{LocalCode: strings.TrimSpace(in.LocalCode), Name: strings.Join(strings.Fields(in.Name), " "),
		Kind: strings.ToLower(strings.TrimSpace(in.Kind)), Currency: strings.ToUpper(strings.TrimSpace(in.Currency))}
	if p.Currency == "" {
		p.Currency = defaultCurrency
	}
	switch {
	case p.LocalCode == "":
		return p, "no code"
	case len(p.LocalCode) > 64:
		return p, "code longer than 64 characters"
	case p.Name == "":
		return p, "no name"
	case len(p.Name) > 300:
		return p, "name longer than 300 characters"
	case in.UnitPrice == nil:
		return p, "no price"
	case *in.UnitPrice < 0:
		return p, "negative price"
	case !currencyRe.MatchString(p.Currency):
		return p, "currency " + p.Currency + " is not a three-letter code"
	case p.Kind != "" && !slices.Contains(catalog.ItemKinds, catalog.ItemKind(p.Kind)):
		p.Kind = "" // an unknown kind only narrows suggestions; drop it
	}
	p.UnitPrice = *in.UnitPrice
	return p, ""
}

func by(r *http.Request) string {
	org := orgFrom(r.Context())
	if org.Session {
		return org.UserID
	}
	return "api:" + org.Environment
}

// putPrices adds or updates lines; the rest of the list is left as it is.
// The same request twice gives the same list.
func (s *Server) putPrices(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prices []priceInput `json:"prices"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if len(in.Prices) == 0 || len(in.Prices) > maxPrices {
		writeError(w, http.StatusUnprocessableEntity, "invalid_prices", "Send between 1 and 50,000 prices.", nil)
		return
	}
	problems := map[string]string{}
	ps := make([]store.Price, 0, len(in.Prices))
	seen := map[string]bool{}
	for i, pi := range in.Prices {
		p, problem := checkPrice(pi, "GHS")
		if problem == "" && seen[p.LocalCode] {
			problem = "repeats an earlier code"
		}
		if problem != "" {
			problems["prices["+strconv.Itoa(i)+"]"] = problem
			continue
		}
		seen[p.LocalCode] = true
		ps = append(ps, p)
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_prices", "Some prices need fixing.", problems)
		return
	}
	changes, err := s.Store.SavePrices(r.Context(), orgFrom(r.Context()).ID, ps, false, by(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, changes)
}

type importRow struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// importPrices reads a CSV or Excel file. Merge (the default) adds and
// updates; replace also removes codes the file does not have. Rows that
// cannot be read are skipped and listed; with dry_run nothing is saved, so
// the console can show what a file would do first.
func (s *Server) importPrices(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File     string `json:"file"` // base64 of a .csv or .xlsx file
		Mode     string `json:"mode"`
		Currency string `json:"currency"`
		DryRun   bool   `json:"dry_run"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	if in.Mode == "" {
		in.Mode = "merge"
	}
	if in.Mode != "merge" && in.Mode != "replace" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_mode", "Mode is merge or replace.", nil)
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "GHS"
	}
	if !currencyRe.MatchString(currency) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_currency", "Use a three-letter currency code, like GHS.", nil)
		return
	}
	data, err := base64.StdEncoding.DecodeString(in.File)
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_file", "Send the file as base64.", nil)
		return
	}
	ps, skipped, err := readPriceFile(data, currency)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_file", err.Error(), nil)
		return
	}
	out := map[string]any{"rows": len(ps), "skipped": skipped, "dry_run": in.DryRun}
	if in.DryRun {
		writeJSON(w, http.StatusOK, out)
		return
	}
	changes, err := s.Store.SavePrices(r.Context(), orgFrom(r.Context()).ID, ps, in.Mode == "replace", by(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out["changes"] = changes
	writeJSON(w, http.StatusOK, out)
}

func readPriceFile(data []byte, currency string) ([]store.Price, []importRow, error) {
	rows, err := sheet.Read(data)
	if err != nil {
		return nil, nil, err
	}
	// The header is the first row that names a code and a name column:
	// exports often start with a title or a blank line.
	start := -1
	var h sheet.Header
	var ci, ni, ki, pi, cu int
	for i := 0; i < len(rows) && i < 10; i++ {
		h = sheet.NewHeader(rows[i])
		ci = h.Col("code", "item code", "service code", "drug code", "product code", "local code", "id")
		ni = h.Col("name", "description", "item", "item name", "service", "service name", "drug name", "product name")
		if ci >= 0 && ni >= 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, nil, errors.New("no header row with a code column and a name column in the first 10 rows")
	}
	ki = h.Col("kind", "type", "category", "group")
	pi = h.Col("price", "unit price", "amount", "tariff", "rate", "cost", "selling price")
	cu = h.Col("currency")
	if pi < 0 {
		return nil, nil, errors.New("no price column (price, unit price, amount, tariff or rate)")
	}
	var ps []store.Price
	skipped := []importRow{}
	seen := map[string]int{}
	for i := start + 1; i < len(rows); i++ {
		row, line := rows[i], i+1
		code, name, price := sheet.Cell(row, ci), sheet.Cell(row, ni), sheet.Cell(row, pi)
		if code == "" && name == "" && price == "" {
			continue
		}
		in := priceInput{LocalCode: code, Name: name, Kind: kindFrom(sheet.Cell(row, ki)), Currency: sheet.Cell(row, cu)}
		if price != "" {
			if v, err := sheet.Money(price); err == nil {
				in.UnitPrice = &v
			} else {
				skipped = append(skipped, importRow{line, "price " + price + " is not an amount"})
				continue
			}
		}
		p, problem := checkPrice(in, currency)
		if problem == "" {
			if first, dup := seen[p.LocalCode]; dup {
				problem = "code " + p.LocalCode + " repeats line " + strconv.Itoa(first)
			}
		}
		if problem != "" {
			skipped = append(skipped, importRow{line, problem})
			continue
		}
		seen[p.LocalCode] = line
		ps = append(ps, p)
		if len(ps) > maxPrices {
			return nil, nil, errors.New("the file has more than 50,000 prices")
		}
	}
	if len(ps) == 0 {
		return nil, skipped, errors.New("no row has a code, a name and a price")
	}
	return ps, skipped, nil
}

// kindFrom reads the kinds clinics' own exports use.
func kindFrom(s string) string {
	s = strings.ToLower(s)
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "drug") || strings.Contains(s, "medic") || strings.Contains(s, "pharm"):
		return string(catalog.Medication)
	case strings.Contains(s, "lab"):
		return string(catalog.Lab)
	case strings.Contains(s, "imag") || strings.Contains(s, "radio") || strings.Contains(s, "scan") || strings.Contains(s, "x-ray"):
		return string(catalog.Imaging)
	case strings.Contains(s, "procedure") || strings.Contains(s, "surg") || strings.Contains(s, "theatre"):
		return string(catalog.Procedure)
	case strings.Contains(s, "consum") || strings.Contains(s, "suppl"):
		return string(catalog.Consumable)
	case strings.Contains(s, "consult") || strings.Contains(s, "service") || strings.Contains(s, "ward") || strings.Contains(s, "admission"):
		return string(catalog.Service)
	}
	return ""
}

func (s *Server) deletePrice(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeletePrice(r.Context(), orgFrom(r.Context()).ID, r.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "price_not_found", "No price for '"+r.PathValue("code")+"'.", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
