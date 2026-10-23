package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/sheet"
)

// Importing payers takes a list of payers with the platform each is reached
// through and its reference on that platform, as Serenity keeps them
// (payers.external_reference_id behind a payer plugin; see
// docs/serenity-payers.md for the query that exports them). New payers are
// added; a payer already in the catalogue only has missing settings filled,
// and every difference from what is stored is reported, never overwritten.

// platformOf reads Serenity's plugin names and our own connector keys.
func platformOf(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), ""))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "medserve") || strings.Contains(s, "petherposh"):
		return "medserve"
	case strings.Contains(s, "smartclaims") || strings.Contains(s, "nmh"):
		return "smartclaims"
	case strings.Contains(s, "xceli"):
		return "xceli"
	case strings.Contains(s, "claimiq"):
		return "claimiq"
	case strings.HasPrefix(s, "rx"):
		return "rx_insurance"
	}
	if _, _, ok := connector.Lookup(s); ok {
		return s
	}
	return ""
}

// referenceField is the payer setting a platform's reference fills, the way
// Serenity's plugins read external_reference_id.
var referenceField = map[string]string{
	"medserve":     "insurer_code",
	"smartclaims":  "hicd",
	"xceli":        "insurer_api_url",
	"rx_insurance": "ins_company_code",
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func payerSlug(name string) string {
	s := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "_"), "_")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "_")
	}
	return s
}

type importedPayer struct {
	Line      int               `json:"line"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Connector string            `json:"connector"`
	Action    string            `json:"action"` // add, fill, same, conflict, skip
	Fields    map[string]string `json:"fields,omitempty"`
	Reason    string            `json:"reason,omitempty"`
}

func (s *Server) opImportPayers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		File   string `json:"file"`
		DryRun bool   `json:"dry_run"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
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
	rows, save, err := planPayerImport(data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_file", err.Error(), nil)
		return
	}
	out := map[string]any{"rows": rows, "dry_run": in.DryRun, "saved": 0}
	if !in.DryRun && len(save) > 0 {
		if _, err := s.Store.SavePayers(r.Context(), save, orgFrom(r.Context()).UserID); err != nil {
			s.internal(w, r, err)
			return
		}
		out["saved"] = len(save)
	}
	writeJSON(w, http.StatusOK, out)
}

// planPayerImport decides what each row does against the catalogue as it
// is now, and returns the payers to save.
func planPayerImport(data []byte) ([]importedPayer, []catalog.Payer, error) {
	table, err := sheet.Read(data)
	if err != nil {
		return nil, nil, err
	}
	if len(table) < 2 {
		return nil, nil, errors.New("the file has no rows under its header")
	}
	h := sheet.NewHeader(table[0])
	ni := h.Col("name", "payer", "payer name", "insurer")
	pi := h.Col("platform", "plugin", "plugin name", "connector")
	ri := h.Col("reference", "external_reference_id", "external reference id", "insurer code", "code")
	ci := h.Col("country", "country_code")
	ai := h.Col("active", "is_active")
	if ni < 0 || pi < 0 {
		return nil, nil, errors.New("the first row must name a name column and a platform column")
	}
	byName := map[string]catalog.Payer{}
	for _, p := range catalog.AllPayers() {
		byName[strings.ToLower(p.Name)] = p
	}
	var rows []importedPayer
	var save []catalog.Payer
	planned := map[string]int{} // id -> index in rows of the row that set it
	for i, rec := range table[1:] {
		line := i + 2
		name := strings.Join(strings.Fields(sheet.Cell(rec, ni)), " ")
		if name == "" {
			continue
		}
		row := importedPayer{Line: line, Name: name, Connector: platformOf(sheet.Cell(rec, pi)), Fields: map[string]string{}}
		ref := sheet.Cell(rec, ri)
		if row.Connector == "" {
			row.Action, row.Reason = "skip", "platform "+sheet.Cell(rec, pi)+" is not one we connect to"
			rows = append(rows, row)
			continue
		}
		if f, ok := referenceField[row.Connector]; ok && ref != "" {
			row.Fields[f] = ref
		}
		existing, known := byName[strings.ToLower(name)]
		if known {
			row.ID = existing.ID
		} else {
			row.ID = payerSlug(name)
			if existing, known = catalog.LookupPayer(row.ID); known && !strings.EqualFold(existing.Name, name) {
				row.Action, row.Reason = "skip", "id "+row.ID+" already belongs to "+existing.Name
				rows = append(rows, row)
				continue
			}
		}
		if !payerIDRe.MatchString(row.ID) {
			row.Action, row.Reason = "skip", "the name gives no usable id"
			rows = append(rows, row)
			continue
		}
		if j, dup := planned[row.ID]; dup {
			prev := rows[j]
			switch {
			case prev.Connector != row.Connector:
				row.Action, row.Reason = "skip", "line "+strconv.Itoa(prev.Line)+" has it on "+prev.Connector
			case !sameFields(prev.Fields, row.Fields):
				row.Action, row.Reason = "conflict", "line "+strconv.Itoa(prev.Line)+" gives a different reference"
			default:
				row.Action, row.Reason = "same", "repeats line "+strconv.Itoa(prev.Line)
			}
			rows = append(rows, row)
			continue
		}
		planned[row.ID] = len(rows)

		if !known {
			country := strings.ToUpper(sheet.Cell(rec, ci))
			if len(country) != 2 {
				country = "GH"
			}
			active := strings.ToLower(sheet.Cell(rec, ai))
			p := catalog.Payer{ID: row.ID, Name: name, Country: country, Connector: row.Connector, Fields: row.Fields,
				Inactive: active == "false" || active == "f" || active == "0" || active == "no"}
			row.Action = "add"
			rows = append(rows, row)
			save = append(save, p)
			continue
		}
		if existing.Connector != row.Connector {
			row.Action, row.Reason = "conflict", "the catalogue has it on "+existing.Connector
			rows = append(rows, row)
			continue
		}
		merged := map[string]string{}
		for k, v := range existing.Fields {
			merged[k] = v
		}
		filled, differs := false, ""
		for k, v := range row.Fields {
			switch cur := existing.Fields[k]; {
			case cur == "":
				merged[k], filled = v, true
			case cur != v:
				differs = k + " is " + cur + " in the catalogue"
			}
		}
		switch {
		case differs != "":
			row.Action, row.Reason = "conflict", differs
		case filled:
			row.Action = "fill"
			p := existing
			p.Fields = merged
			save = append(save, p)
		default:
			row.Action = "same"
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil, nil, errors.New("no row has a payer name")
	}
	return rows, save, nil
}

func sameFields(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
