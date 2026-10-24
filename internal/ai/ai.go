// Package ai is Payahub's one door to language models. Features ask for a
// completion by task ("read this claim form"); which provider and model
// answer is operator configuration: Claude, OpenAI, or any endpoint that
// speaks OpenAI's chat completions (Azure, Groq, OpenRouter, vLLM, Ollama).
// Providers are tried in order, so one can stand in when another is down.
//
// Model output is never sent to a payer. Callers validate what comes back
// against their own rules, record it as evidence, and send a claim only
// from fields a rule or a person accepted.
package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/harrisyn/payahub/internal/deid"
)

type Kind string

const (
	Anthropic        Kind = "anthropic"
	OpenAI           Kind = "openai"
	OpenAICompatible Kind = "openai_compatible"
)

// Tasks the platform uses models for. A provider serves the tasks it lists.
const (
	TaskDocumentIntake = "document_intake" // read claim forms and invoices into claim fields
	TaskCodeMatching   = "code_matching"   // propose standard items for clinic codes
)

var Tasks = []string{TaskDocumentIntake, TaskCodeMatching}

// Config is one configured provider. APIKey is held only in memory after
// it is opened from the vault.
type Config struct {
	ID      string
	Kind    Kind
	Name    string
	BaseURL string // empty for the provider's own API
	Model   string
	APIKey  string
	// Effort is Claude's effort level (low, medium, high, xhigh, max);
	// empty means high.
	Effort string
	// JSONMode is how an OpenAI-style endpoint is asked for JSON: "schema"
	// (strict JSON schema), "object" (any JSON object) or "none" (the schema
	// is only described in the prompt). OpenAI defaults to schema; other
	// compatible endpoints to object.
	JSONMode string
}

// Part is one piece of the request: text, or a file (an image or a PDF).
type Part struct {
	Text      string
	MediaType string // image/png, image/jpeg, image/webp, image/gif, application/pdf
	Data      []byte
	Name      string // a file's name, where the provider wants one
}

func Text(s string) Part { return Part{Text: s} }

// Request is one question with its material. With Schema set the answer is
// a JSON object that matches it.
type Request struct {
	Task       string
	System     string
	Parts      []Part
	Schema     map[string]any
	SchemaName string
	MaxTokens  int
	// Cleared vouches that every part passed the de-identifier. A provider
	// sends nothing it does not cover; the system prompt is Payahub's own.
	Cleared deid.Cleared
}

// ErrNotDeidentified is returned, before anything is sent, for a request
// with a part the de-identifier did not produce.
var ErrNotDeidentified = errors.New("ai: refused: part of the request did not pass the de-identifier")

// checkCleared is the privacy gate every provider calls first.
func checkCleared(r Request) error {
	for _, p := range r.Parts {
		m := p.Data
		if m == nil {
			m = []byte(p.Text)
		}
		if !r.Cleared.Covers(m) {
			return ErrNotDeidentified
		}
	}
	return nil
}

type Response struct {
	Text         string
	Provider     string // the Config.ID that answered
	Model        string
	InputTokens  int64
	OutputTokens int64
	StopReason   string
}

// Provider answers requests.
type Provider interface {
	Complete(ctx context.Context, r Request) (Response, error)
}

// Error is a provider's failure. Retryable failures (rate limits, overload,
// timeouts, 5xx) let the router try the next provider; the rest do not.
type Error struct {
	Provider  string
	Status    int
	Retryable bool
	Message   string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, e.Message)
	}
	return e.Provider + ": " + e.Message
}

// ErrRefused is returned when the model declines to answer.
var ErrRefused = errors.New("ai: the model declined to answer")

// ErrNoProvider is returned when no enabled provider serves the task.
var ErrNoProvider = errors.New("ai: no provider is set up for this task")

func retryableStatus(s int) bool { return s == 408 || s == 409 || s == 429 || s >= 500 }

// New builds a provider from its configuration.
func New(c Config) (Provider, error) {
	if strings.TrimSpace(c.Model) == "" {
		return nil, errors.New("ai: a model is required")
	}
	switch c.Kind {
	case Anthropic:
		return newClaude(c), nil
	case OpenAI:
		if c.BaseURL == "" {
			c.BaseURL = "https://api.openai.com/v1"
		}
		if c.JSONMode == "" {
			c.JSONMode = "schema"
		}
		return newOpenAI(c), nil
	case OpenAICompatible:
		if c.BaseURL == "" {
			return nil, errors.New("ai: an OpenAI-compatible provider needs its base URL")
		}
		if c.JSONMode == "" {
			c.JSONMode = "object"
		}
		return newOpenAI(c), nil
	}
	return nil, fmt.Errorf("ai: unknown provider kind %q", c.Kind)
}

// Router tries providers in order: the next one answers when one fails in
// a way another might not (an outage, a rate limit, a refusal).
type Router struct {
	Providers []Provider
}

func (rt Router) Complete(ctx context.Context, r Request) (Response, error) {
	if err := checkCleared(r); err != nil {
		return Response{}, err
	}
	if len(rt.Providers) == 0 {
		return Response{}, ErrNoProvider
	}
	var errs []error
	for _, p := range rt.Providers {
		res, err := p.Complete(ctx, r)
		if err == nil {
			return res, nil
		}
		errs = append(errs, err)
		var pe *Error
		if !errors.Is(err, ErrRefused) && !(errors.As(err, &pe) && pe.Retryable) {
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	return Response{}, errors.Join(errs...)
}
