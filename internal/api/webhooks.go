package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/harrisyn/payahub/internal/store"
	"github.com/harrisyn/payahub/internal/webhook"
)

// maxEndpoints is how many webhook endpoints an organisation has per environment.
const maxEndpoints = 10

func (s *Server) webhookRoutes(mux *http.ServeMux) {
	mux.Handle("POST /v1/webhook-endpoints", s.auth(s.createWebhookEndpoint))
	mux.Handle("GET /v1/webhook-endpoints", s.auth(s.listWebhookEndpoints))
	mux.Handle("DELETE /v1/webhook-endpoints/{id}", s.auth(s.deleteWebhookEndpoint))
	mux.Handle("POST /v1/webhook-endpoints/{id}/ping", s.auth(s.pingWebhookEndpoint))
	mux.Handle("GET /v1/webhook-endpoints/{id}/deliveries", s.auth(s.listWebhookDeliveries))
}

// envOf is the environment a request acts in: an API key's own, or the
// one a console session names (sandbox unless it says live).
func envOf(org store.Org, asked string) string {
	if !org.Session {
		return org.Environment
	}
	if asked == "live" {
		return "live"
	}
	return "sandbox"
}

func (s *Server) createWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL         string `json:"url"`
		Description string `json:"description"`
		Environment string `json:"environment"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.URL, in.Description = strings.TrimSpace(in.URL), strings.TrimSpace(in.Description)
	if err := webhook.CheckURL(in.URL, s.AllowPrivateWebhooks); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_url", "Enter your system's public https address, like https://hms.example.com/payahub/events.",
			map[string]string{"url": "A public https address."})
		return
	}
	if len(in.Description) > 200 {
		in.Description = in.Description[:200]
	}
	org := orgFrom(r.Context())
	env := envOf(org, in.Environment)
	have, err := s.Store.WebhookEndpoints(r.Context(), org.ID, env)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if len(have) >= maxEndpoints {
		writeError(w, http.StatusUnprocessableEntity, "too_many_endpoints", "An organisation can have 10 webhook endpoints in each environment.", nil)
		return
	}
	e := store.WebhookEndpoint{ID: store.NewID("whe"), Environment: env, URL: in.URL, Description: in.Description}
	secret := webhook.NewSecret()
	sealed, err := s.Vault.Seal(map[string]string{"secret": secret}, webhook.Aad(e.ID))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if e, err = s.Store.CreateWebhookEndpoint(r.Context(), e, org.ID, sealed); err != nil {
		s.internal(w, r, err)
		return
	}
	// The secret is shown this once; Payahub keeps it sealed.
	writeJSON(w, http.StatusCreated, map[string]any{"endpoint": e, "secret": secret})
}

func (s *Server) listWebhookEndpoints(w http.ResponseWriter, r *http.Request) {
	org := orgFrom(r.Context())
	list, err := s.Store.WebhookEndpoints(r.Context(), org.ID, envOf(org, r.URL.Query().Get("environment")))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}

func (s *Server) deleteWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	s.endpointDone(w, r, s.Store.DeleteWebhookEndpoint(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id")), http.StatusNoContent)
}

func (s *Server) pingWebhookEndpoint(w http.ResponseWriter, r *http.Request) {
	s.endpointDone(w, r, s.Store.QueueWebhookPing(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id")), http.StatusAccepted)
}

func (s *Server) endpointDone(w http.ResponseWriter, r *http.Request, err error, status int) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "endpoint_not_found", "No webhook endpoint "+r.PathValue("id")+".", nil)
	case err != nil:
		s.internal(w, r, err)
	case status == http.StatusNoContent:
		w.WriteHeader(status)
	default:
		writeJSON(w, status, map[string]any{"queued": true})
	}
}

func (s *Server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.WebhookDeliveries(r.Context(), orgFrom(r.Context()).ID, r.PathValue("id"), 50)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "endpoint_not_found", "No webhook endpoint "+r.PathValue("id")+".", nil)
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": list})
}
