package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/store"
)

// Model providers are platform settings: operators choose which providers
// and models serve each task, in what order. Keys are write-only.
func (s *Server) aiRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/operator/ai-providers", s.operator(s.listAIProviders))
	mux.Handle("PUT /v1/operator/ai-providers/{id}", s.operator(s.putAIProvider))
	mux.Handle("DELETE /v1/operator/ai-providers/{id}", s.operator(s.deleteAIProvider))
	mux.Handle("POST /v1/operator/ai-providers/{id}/check", s.operator(s.checkAIProvider))
}

var aiID = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,39}$`)

func (s *Server) listAIProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListAIProviders(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": ps, "tasks": ai.Tasks,
		"default_models": map[string]string{string(ai.Anthropic): ai.DefaultClaudeModel}})
}

func (s *Server) putAIProvider(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind     ai.Kind  `json:"kind"`
		Name     string   `json:"name"`
		BaseURL  string   `json:"base_url"`
		Model    string   `json:"model"`
		Effort   string   `json:"effort"`
		JSONMode string   `json:"json_mode"`
		Tasks    []string `json:"tasks"`
		Priority int      `json:"priority"`
		Enabled  *bool    `json:"enabled"`
		APIKey   string   `json:"api_key"`
	}
	if !decode(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	in.Name, in.Model, in.APIKey = strings.TrimSpace(in.Name), strings.TrimSpace(in.Model), strings.TrimSpace(in.APIKey)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	problems := map[string]string{}
	if !aiID.MatchString(id) {
		problems["id"] = "lowercase letters, digits, - and _, starting with a letter"
	}
	if !slices.Contains([]ai.Kind{ai.Anthropic, ai.OpenAI, ai.OpenAICompatible}, in.Kind) {
		problems["kind"] = "must be anthropic, openai or openai_compatible"
	}
	if in.Name == "" {
		problems["name"] = "is required"
	}
	if in.Model == "" {
		problems["model"] = "is required"
	}
	if in.BaseURL != "" {
		u, err := url.Parse(in.BaseURL)
		local := err == nil && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")
		if err != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && local)) {
			problems["base_url"] = "must be an https URL (http only for localhost)"
		}
	} else if in.Kind == ai.OpenAICompatible {
		problems["base_url"] = "is required for an OpenAI-compatible endpoint"
	}
	if in.Effort != "" && !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, in.Effort) {
		problems["effort"] = "must be low, medium, high, xhigh or max"
	}
	if in.JSONMode != "" && !slices.Contains([]string{"schema", "object", "none"}, in.JSONMode) {
		problems["json_mode"] = "must be schema, object or none"
	}
	for _, t := range in.Tasks {
		if !slices.Contains(ai.Tasks, t) {
			problems["tasks"] = "unknown task " + t
		}
	}
	if in.Priority < 0 || in.Priority > 1000 {
		problems["priority"] = "must be 0 to 1000"
	}
	existing, err := s.Store.GetAIProvider(r.Context(), id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.internal(w, r, err)
		return
	}
	if in.APIKey == "" && !existing.HasKey && in.Kind != ai.OpenAICompatible {
		problems["api_key"] = "is required"
	}
	if len(problems) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_ai_provider", "Check the highlighted fields.", problems)
		return
	}
	enabled := in.Enabled == nil || *in.Enabled
	if in.Tasks == nil {
		in.Tasks = []string{}
	}
	p, created, err := s.Store.SaveAIProvider(r.Context(), s.Vault, store.AIProvider{ID: id, Kind: in.Kind, Name: in.Name,
		BaseURL: in.BaseURL, Model: in.Model, Effort: in.Effort, JSONMode: in.JSONMode, Tasks: in.Tasks,
		Priority: in.Priority, Enabled: enabled}, in.APIKey, orgFrom(r.Context()).UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, p)
}

func (s *Server) deleteAIProvider(w http.ResponseWriter, r *http.Request) {
	err := s.Store.DeleteAIProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "ai_provider_not_found", "No provider "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkAIProvider asks the provider one small structured question, so an
// operator knows the key, model and JSON mode work before a task relies on them.
func (s *Server) checkAIProvider(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetAIProvider(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "ai_provider_not_found", "No provider "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cfg, err := s.Store.AIConfig(s.Vault, p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	ok, message := false, ""
	prov, err := ai.New(cfg)
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		start := time.Now()
		var res ai.Response
		res, err = prov.Complete(ctx, ai.Request{
			System:     "You are checking that an integration works.",
			Parts:      []ai.Part{ai.Text(`Answer with {"ok": true}.`)},
			Schema:     map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []string{"ok"}, "additionalProperties": false},
			SchemaName: "check",
			MaxTokens:  2000,
		})
		var out struct {
			OK bool `json:"ok"`
		}
		switch {
		case err != nil:
		case json.Unmarshal([]byte(strings.TrimSpace(res.Text)), &out) != nil || !out.OK:
			message = "The model answered, but not with the JSON asked for. Try another JSON mode."
		default:
			ok = true
			message = "Answered in " + time.Since(start).Round(time.Millisecond).String() + " with " + res.Model + "."
		}
	}
	if err != nil {
		message = err.Error()
	}
	if err := s.Store.SetAICheck(r.Context(), p.ID, ok, message); err != nil {
		s.internal(w, r, err)
		return
	}
	p, err = s.Store.GetAIProvider(r.Context(), p.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
