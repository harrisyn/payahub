package api

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/assist"
	"github.com/harrisyn/payahub/internal/catalog"
	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/deid"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/store"
)

func (s *Server) assistRoutes(mux *http.ServeMux) {
	mux.Handle("POST /v1/claims/extract", s.auth(s.extractClaim))
	mux.Handle("GET /v1/operator/ai-usage", s.operator(s.aiUsage))
}

// suggestion is a standard item proposed for an organisation's own item:
// by the catalogue's word matching, or by a model, with its evidence.
type suggestion struct {
	Item       catalog.Item    `json:"item"`
	Confidence float64         `json:"confidence"`
	Source     string          `json:"source"` // words | model
	Reason     string          `json:"reason,omitempty"`
	Evidence   *claim.Evidence `json:"evidence,omitempty"`
}

// modelPicks asks the configured models for the items behind own, when
// any model serves code matching. It returns nil when none is set up or
// every call failed, so the caller keeps to word matching.
func (s *Server) modelPicks(ctx context.Context, orgID string, own []assist.Own) (map[string]assist.Pick, error) {
	rt, err := s.Store.AIRouter(ctx, s.Vault, ai.TaskCodeMatching)
	if err != nil || len(rt.Providers) == 0 {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	picks, runs := assist.MatchItems(ctx, rt, own)
	s.recordRuns(ctx, orgID, runs)
	var failed error
	for _, r := range runs {
		if r.Err != nil {
			failed = r.Err
		}
	}
	if len(picks) == 0 && failed != nil {
		return nil, failed
	}
	return picks, nil
}

// mergeSuggestions puts the model's pick first, then the word matches that
// differ from it, at most limit in all.
func mergeSuggestions(p assist.Pick, picked bool, words []catalog.Suggestion, limit int) []suggestion {
	out := []suggestion{}
	if picked && p.Found {
		ev := p.Evidence
		out = append(out, suggestion{Item: p.Item, Confidence: p.Confidence, Source: "model", Reason: p.Reason, Evidence: &ev})
	}
	for _, w := range words {
		if len(out) == limit {
			break
		}
		if picked && p.Found && w.Item.ID == p.Item.ID {
			continue
		}
		conf := w.Confidence
		// The model looked and found no match: say so by lowering the words' guess.
		if picked && !p.Found {
			conf = min(conf, 0.5)
		}
		out = append(out, suggestion{Item: w.Item, Confidence: conf, Source: "words"})
	}
	return out
}

func (s *Server) recordRuns(ctx context.Context, orgID string, runs []assist.Run) {
	rec := make([]store.AIRun, 0, len(runs))
	for _, r := range runs {
		msg := ""
		if r.Err != nil {
			msg = r.Err.Error()
			if len(msg) > 500 {
				msg = msg[:500]
			}
		}
		rec = append(rec, store.AIRun{OrgID: orgID, Task: r.Task, Provider: r.Provider, Model: r.Model,
			PromptVersion: r.PromptVersion, PromptHash: r.PromptHash, InputSHA256: r.InputSHA256,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, DurationMS: int(r.Duration.Milliseconds()),
			OK: r.Err == nil, Error: msg, Privacy: r.Privacy})
	}
	// The audit row must not be lost to a request that has just timed out.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.Store.RecordAIRuns(ctx, rec); err != nil {
		s.Logger.ErrorContext(ctx, "recording model runs", "err", err)
	}
}

// extractClaim reads a claim form (a PDF or a photo) into a proposed claim.
// Nothing is saved: the caller checks the fields marked needs_review and
// creates the claim with POST /v1/claims.
func (s *Server) extractClaim(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, assist.MaxDocument+1<<20)
	doc, ok := readDocument(w, r)
	if !ok {
		return
	}
	rt, err := s.Store.AIRouter(r.Context(), s.Vault, ai.TaskDocumentIntake)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if len(rt.Providers) == 0 {
		writeError(w, http.StatusConflict, "no_model", "No model is set up to read documents yet. Enter the claim, or upload a spreadsheet.", nil)
		return
	}
	extendWrite(w, 3*time.Minute+10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	org := orgFrom(r.Context())
	ocr := s.OCR
	if ocr == nil {
		ocr = deid.Local{}
	}
	x, run, err := assist.ReadClaimForm(ctx, rt, ocr, doc, engine.Thresholds[claim.OriginAIExtracted])
	if run.Task != "" {
		s.recordRuns(r.Context(), org.ID, []assist.Run{run})
	}
	switch {
	case errors.Is(err, deid.ErrNameNotFound):
		writeError(w, http.StatusUnprocessableEntity, "cannot_deidentify",
			"We could not find where the patient's name is on this form, so it was not sent to the model. Try a clearer scan, or enter the claim.", nil)
		return
	case errors.Is(err, deid.ErrNoOCR):
		s.Logger.ErrorContext(r.Context(), "reading a claim form", "err", err)
		writeError(w, http.StatusServiceUnavailable, "ocr_unavailable", "Form reading is not available on this server. Enter the claim, or upload a spreadsheet.", nil)
		return
	}
	if err != nil {
		s.Logger.WarnContext(r.Context(), "reading a claim form", "err", err, slog.String("org", org.ID))
		var pe *ai.Error
		if errors.As(err, &pe) || errors.Is(err, ai.ErrRefused) || errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusBadGateway, "model_failed", "The model could not read this document. Try a clearer scan, or enter the claim.", nil)
			return
		}
		s.internal(w, r, err)
		return
	}
	// Diagnoses the form gave only in words are coded by the code-matching
	// model, when one is set up; the title search stands otherwise.
	if mt, err := s.Store.AIRouter(ctx, s.Vault, ai.TaskCodeMatching); err == nil && len(mt.Providers) > 0 {
		if runs := assist.CodeDiagnoses(ctx, mt, &x, engine.Thresholds[claim.OriginAIMapped]); len(runs) > 0 {
			s.recordRuns(r.Context(), org.ID, runs)
		}
	}
	out := struct {
		assist.Extraction
		RedactedPages []string `json:"redacted_pages,omitempty"`
	}{Extraction: x}
	// What the model saw, for a person to see that the identity was hidden.
	if r.URL.Query().Get("include") == "redacted_pages" {
		for _, p := range x.Pages {
			out.RedactedPages = append(out.RedactedPages, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(p))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// readDocument takes the file from a multipart form field "file", or the
// whole body when it is sent with the file's own content type.
func readDocument(w http.ResponseWriter, r *http.Request) (assist.Document, bool) {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var doc assist.Document
	var body io.Reader
	if ct == "multipart/form-data" {
		f, h, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_document", "Send the claim form in a field named file.", nil)
			return doc, false
		}
		defer f.Close()
		doc.Name = h.Filename
		ct, _, _ = mime.ParseMediaType(h.Header.Get("Content-Type"))
		body = f
	} else {
		body = r.Body
	}
	data, err := io.ReadAll(io.LimitReader(body, assist.MaxDocument+1))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "document_too_large", "A claim form can be up to 10 MB.", nil)
		return doc, false
	}
	if len(data) > assist.MaxDocument {
		writeError(w, http.StatusRequestEntityTooLarge, "document_too_large", "A claim form can be up to 10 MB.", nil)
		return doc, false
	}
	// Trust the bytes over the declared type.
	sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	switch {
	case strings.HasPrefix(string(data), "%PDF-"):
		ct = "application/pdf"
	case sniffed == "image/png" || sniffed == "image/jpeg" || sniffed == "image/webp":
		ct = sniffed
	default:
		writeError(w, http.StatusUnsupportedMediaType, "invalid_document", "Send a PDF, or a PNG, JPEG or WebP photo of the form.", nil)
		return doc, false
	}
	doc.MediaType, doc.Data = ct, data
	return doc, true
}

// aiUsage sums model use over the last 30 days, per task and model.
func (s *Server) aiUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.AIUsageSince(r.Context(), time.Now().AddDate(0, 0, -30))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": u, "days": 30})
}
