package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/assist"
	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
	"github.com/harrisyn/payahub/internal/deid"
	"github.com/harrisyn/payahub/internal/icd10"
	"github.com/harrisyn/payahub/internal/queue"
	"github.com/harrisyn/payahub/internal/store"
)

// Catalogue routes: the shared payers and standard items are public reference
// data; an organisation's mappings of its own codes need its key or session.
func (s *Server) catalogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/payers", s.listPayers)
	mux.HandleFunc("GET /v1/payers/{id}", s.getPayer)
	mux.HandleFunc("GET /v1/connectors", s.listConnectors)
	mux.HandleFunc("GET /v1/items", s.listItems)
	mux.HandleFunc("GET /v1/items/{id}", s.getItem)
	mux.Handle("POST /v1/items/suggest", s.auth(s.suggestItems))
	mux.HandleFunc("GET /v1/diagnoses", s.searchDiagnoses)
	mux.Handle("POST /v1/diagnoses/suggest", s.auth(s.suggestDiagnoses))
	mux.Handle("GET /v1/item-mappings", s.auth(s.listItemMappings))
	mux.Handle("PUT /v1/item-mappings", s.auth(s.putItemMappings))
	mux.Handle("DELETE /v1/item-mappings/{code}", s.auth(s.deleteItemMapping))
}

type connectorRef struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Kind   connector.Kind `json:"kind"`
	Status string         `json:"status"`
}

type payerView struct {
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Country           string           `json:"country"`
	Kind              string           `json:"kind"` // standalone | integrated
	Connector         connectorRef     `json:"connector"`
	Status            string           `json:"status"` // available | connecting
	MemberOTPRequired bool             `json:"member_otp_required"`
	Capabilities      []connector.Verb `json:"capabilities"`
	CodedItems        int              `json:"coded_items"`
	Notes             string           `json:"notes,omitempty"`
	Rules             payerRulesView   `json:"rules"`
}

// payerRulesView is how strictly the payer's own list applies, with the
// defaults spelled out.
type payerRulesView struct {
	PricePolicy    string `json:"price_policy"`
	UnlistedPolicy string `json:"unlisted_policy"`
	HasList        bool   `json:"has_list"`
}

func policy(p string) string {
	if p == "" {
		return "warn"
	}
	return p
}

func viewPayer(p catalog.Payer) payerView {
	m, _, _ := connector.Lookup(p.Connector)
	status := "connecting"
	if p.Ready() {
		status = connector.StatusAvailable
	}
	return payerView{
		ID: p.ID, Name: p.Name, Country: p.Country, Kind: p.Kind(),
		Connector: connectorRef{ID: m.Key, Name: m.Name, Kind: m.Kind, Status: m.Status},
		Status:    status, MemberOTPRequired: m.MemberOTPRequired, Capabilities: m.Verbs,
		CodedItems: catalog.CodedItems(p.ID), Notes: p.Notes,
		Rules: payerRulesView{PricePolicy: policy(p.Rules.PricePolicy), UnlistedPolicy: policy(p.Rules.UnlistedPolicy),
			HasList: catalog.HasList(p.ID)},
	}
}

func (s *Server) listPayers(w http.ResponseWriter, _ *http.Request) {
	out := []payerView{}
	for _, p := range catalog.Payers() {
		out = append(out, viewPayer(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (s *Server) getPayer(w http.ResponseWriter, r *http.Request) {
	p, ok := catalog.LookupPayer(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_payer", "No payer '"+r.PathValue("id")+"' in the catalogue.", nil)
		return
	}
	writeJSON(w, http.StatusOK, viewPayer(p))
}

// listConnectors lists the platforms and payer interfaces, with the account
// fields an organisation fills in for each.
func (s *Server) listConnectors(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": connector.Manifests()})
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	kind := catalog.ItemKind(r.URL.Query().Get("kind"))
	items := []catalog.Item{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		for _, sg := range catalog.Suggest(q, kind, 25) {
			items = append(items, sg.Item)
		}
	} else {
		items = append(items, catalog.Items(kind)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": items})
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	it, ok := catalog.LookupItem(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "unknown_item", "No standard item '"+r.PathValue("id")+"'.", nil)
		return
	}
	type payerCode struct {
		PayerID string `json:"payer_id"`
		catalog.PayerCode
	}
	codes := []payerCode{}
	for _, p := range catalog.Payers() {
		if pc, ok := catalog.CodeFor(p.ID, it.ID); ok {
			codes = append(codes, payerCode{p.ID, pc})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": it, "payer_codes": codes})
}

type ownItem struct {
	Code string           `json:"code"`
	Name string           `json:"name"`
	Kind catalog.ItemKind `json:"kind,omitempty"`
}

// suggestItems proposes standard items for the organisation's own items, for
// a person (or the organisation's system) to confirm with PUT /v1/item-mappings.
func (s *Server) suggestItems(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []ownItem `json:"items"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Items) == 0 || len(in.Items) > 500 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_items", "Send between 1 and 500 items.", nil)
		return
	}
	type result struct {
		ownItem
		Suggestions []suggestion `json:"suggestions"`
	}
	// A model, when one is set up for matching, picks first; the catalogue's
	// word matching fills in and stands in when no model can answer.
	method, note := "words", ""
	var picks map[string]assist.Pick
	if r.URL.Query().Get("method") != "words" {
		own := make([]assist.Own, len(in.Items))
		for i, it := range in.Items {
			own[i] = assist.Own{Code: it.Code, Name: it.Name, Kind: it.Kind}
		}
		var err error
		extendWrite(w, 2*time.Minute+10*time.Second)
		if picks, err = s.modelPicks(r.Context(), orgFrom(r.Context()).ID, own); err != nil {
			s.Logger.WarnContext(r.Context(), "model matching failed; using word matching", "err", err)
			note = "The model could not answer, so these are word matches only."
		} else if picks != nil {
			method = "model"
		}
	}
	out := make([]result, 0, len(in.Items))
	for _, it := range in.Items {
		p, picked := picks[it.Code]
		out = append(out, result{it, mergeSuggestions(p, picked, catalog.Suggest(it.Name, it.Kind, 3), 3)})
	}
	resp := map[string]any{"data": out, "method": method}
	if note != "" {
		resp["note"] = note
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) listItemMappings(w http.ResponseWriter, r *http.Request) {
	ms, err := s.Store.ListItemMappings(r.Context(), orgFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ms})
}

// putItemMappings saves confirmed mappings, all or none. Repeating a request
// gives the same result, so it is safe to retry.
func (s *Server) putItemMappings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mappings []struct {
			LocalCode string `json:"local_code"`
			LocalName string `json:"local_name"`
			ItemID    string `json:"item_id"`
		} `json:"mappings"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Mappings) == 0 || len(in.Mappings) > 500 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_mappings", "Send between 1 and 500 mappings.", nil)
		return
	}
	org := orgFrom(r.Context())
	by := "api:" + org.Environment
	if org.Session {
		by = org.UserID
	}
	problems := map[string]string{}
	ms := make([]store.ItemMapping, 0, len(in.Mappings))
	for _, m := range in.Mappings {
		code := strings.TrimSpace(m.LocalCode)
		if code == "" {
			problems["local_code"] = "Every mapping needs your own code."
			continue
		}
		if _, ok := catalog.LookupItem(m.ItemID); !ok {
			problems[code] = "No standard item '" + m.ItemID + "'."
			continue
		}
		ms = append(ms, store.ItemMapping{LocalCode: code, LocalName: strings.TrimSpace(m.LocalName), ItemID: m.ItemID, ConfirmedBy: by})
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_mappings", "Some mappings need fixing.", problems)
		return
	}
	if err := s.Store.SaveItemMappings(r.Context(), org.ID, ms); err != nil {
		s.internal(w, r, err)
		return
	}
	// Claims waiting on these codes are checked again now, so they move on
	// without anyone opening each one.
	codes := make([]string, len(ms))
	for i, m := range ms {
		codes[i] = m.LocalCode
	}
	re, err := queue.RecheckHeld(r.Context(), s.Store, org.ID, s.actor(r.Context(), org), codes)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	saved, err := s.Store.ListItemMappings(r.Context(), org.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": saved, "rechecked": re})
}

func (s *Server) deleteItemMapping(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteItemMapping(r.Context(), orgFrom(r.Context()).ID, r.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "mapping_not_found", "No mapping for '"+r.PathValue("code")+"'.", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// searchDiagnoses finds ICD-10 codes by code prefix or by words.
func (s *Server) searchDiagnoses(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": icd10.Search(r.URL.Query().Get("q"), limit)})
}

// diagnosisSuggestion is an ICD-10 code proposed for a diagnosis in words.
type diagnosisSuggestion struct {
	Code       icd10.Code      `json:"code"`
	Confidence float64         `json:"confidence"`
	Source     string          `json:"source"` // words | model
	Reason     string          `json:"reason,omitempty"`
	Evidence   *claim.Evidence `json:"evidence,omitempty"`
}

// suggestDiagnoses proposes ICD-10 codes for diagnoses in a clinician's
// words: the code-matching model first, when one is set up, then the title
// search. The words are scrubbed of anything identifying before a model sees
// them. Nothing is saved; a person (or the caller's system) chooses.
func (s *Server) suggestDiagnoses(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Diagnoses []struct {
			Text string `json:"text"`
		} `json:"diagnoses"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Diagnoses) == 0 || len(in.Diagnoses) > 100 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_diagnoses", "Send between 1 and 100 diagnoses.", nil)
		return
	}
	texts := make([]assist.DiagnosisText, 0, len(in.Diagnoses))
	for i, d := range in.Diagnoses {
		t := strings.TrimSpace(d.Text)
		if t == "" || len(t) > 300 {
			writeError(w, http.StatusUnprocessableEntity, "invalid_diagnoses", "Each diagnosis needs its words, up to 300 characters.",
				map[string]string{fmt.Sprintf("diagnoses[%d].text", i): "Between 1 and 300 characters."})
			return
		}
		texts = append(texts, assist.DiagnosisText{Key: strconv.Itoa(i), Text: t})
	}
	method, note := "words", ""
	var picks map[string]assist.DiagnosisPick
	if r.URL.Query().Get("method") != "words" {
		rt, err := s.Store.AIRouter(r.Context(), s.Vault, ai.TaskCodeMatching)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		if len(rt.Providers) > 0 {
			extendWrite(w, 2*time.Minute+10*time.Second)
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			var runs []assist.Run
			picks, runs = assist.MatchDiagnoses(ctx, rt, texts, deid.Identity{})
			s.recordRuns(ctx, orgFrom(r.Context()).ID, runs)
			if len(picks) > 0 {
				method = "model"
			} else {
				for _, run := range runs {
					if run.Err != nil {
						s.Logger.WarnContext(r.Context(), "model diagnosis coding failed; using the title search", "err", run.Err)
						note = "The model could not answer, so these are title matches only."
						break
					}
				}
			}
		}
	}
	type result struct {
		Text        string                `json:"text"`
		Suggestions []diagnosisSuggestion `json:"suggestions"`
	}
	out := make([]result, 0, len(texts))
	for _, t := range texts {
		res := result{Text: t.Text, Suggestions: []diagnosisSuggestion{}}
		p, picked := picks[t.Key]
		if picked && p.Found {
			ev := p.Evidence
			res.Suggestions = append(res.Suggestions, diagnosisSuggestion{Code: p.Code, Confidence: p.Confidence, Source: "model", Reason: p.Reason, Evidence: &ev})
		}
		for _, h := range icd10.Search(t.Text, 3) {
			if len(res.Suggestions) == 3 {
				break
			}
			if picked && p.Found && h.Code == p.Code.Code {
				continue
			}
			// A title that holds every word is a fair guess, never a sure one.
			conf := 0.5
			if picked && !p.Found {
				conf = 0.3
			}
			res.Suggestions = append(res.Suggestions, diagnosisSuggestion{Code: h, Confidence: conf, Source: "words"})
		}
		out = append(out, res)
	}
	resp := map[string]any{"data": out, "method": method}
	if note != "" {
		resp["note"] = note
	}
	writeJSON(w, http.StatusOK, resp)
}
