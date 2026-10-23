package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// openAI speaks the chat completions API, which OpenAI and many other
// hosts (Azure, Groq, OpenRouter, Together, vLLM, Ollama) serve.
type openAI struct {
	cfg    Config
	client *http.Client
}

func newOpenAI(c Config) *openAI {
	return &openAI{cfg: c, client: &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}}
}

type oaPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
	File *oaFile `json:"file,omitempty"`
}

type oaFile struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
}

type oaMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

func dataURL(mediaType string, b []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func (p *openAI) Complete(ctx context.Context, r Request) (Response, error) {
	system := r.System
	if r.Schema != nil && p.cfg.JSONMode != "schema" {
		schema, _ := json.Marshal(r.Schema)
		system = strings.TrimSpace(system + "\n\nAnswer with one JSON object that matches this JSON schema, and nothing else:\n" + string(schema))
	}
	var parts []oaPart
	for _, part := range r.Parts {
		switch {
		case part.MediaType == "application/pdf":
			name := part.Name
			if name == "" {
				name = "document.pdf"
			}
			parts = append(parts, oaPart{Type: "file", File: &oaFile{Filename: name, FileData: dataURL(part.MediaType, part.Data)}})
		case strings.HasPrefix(part.MediaType, "image/"):
			parts = append(parts, oaPart{Type: "image_url", ImageURL: &struct {
				URL string `json:"url"`
			}{dataURL(part.MediaType, part.Data)}})
		case part.Text != "":
			parts = append(parts, oaPart{Type: "text", Text: part.Text})
		}
	}
	var msgs []oaMessage
	if system != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, oaMessage{Role: "user", Content: parts})
	body := map[string]any{"model": p.cfg.Model, "messages": msgs}
	maxTokens := r.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16000
	}
	if p.cfg.Kind == OpenAI {
		body["max_completion_tokens"] = maxTokens
	} else {
		body["max_tokens"] = maxTokens
	}
	if r.Schema != nil {
		switch p.cfg.JSONMode {
		case "schema":
			name := r.SchemaName
			if name == "" {
				name = "answer"
			}
			body["response_format"] = map[string]any{"type": "json_schema",
				"json_schema": map[string]any{"name": name, "schema": r.Schema, "strict": true}}
		case "object":
			body["response_format"] = map[string]any{"type": "json_object"}
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.APIKey)
	}
	res, err := p.client.Do(req)
	if err != nil {
		return Response{}, &Error{Provider: p.cfg.ID, Retryable: ctx.Err() == nil, Message: err.Error()}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return Response{}, &Error{Provider: p.cfg.ID, Retryable: true, Message: "reading the reply: " + err.Error()}
	}
	if res.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(data[:min(len(data), 300)]))
		}
		return Response{}, &Error{Provider: p.cfg.ID, Status: res.StatusCode, Retryable: retryableStatus(res.StatusCode), Message: msg}
	}
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content *string `json:"content"`
				Refusal *string `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil || len(out.Choices) == 0 {
		return Response{}, &Error{Provider: p.cfg.ID, Message: fmt.Sprintf("unreadable reply: %.200s", data)}
	}
	c := out.Choices[0]
	if c.Message.Refusal != nil && *c.Message.Refusal != "" || c.FinishReason == "content_filter" {
		return Response{}, ErrRefused
	}
	if c.FinishReason == "length" {
		return Response{}, &Error{Provider: p.cfg.ID, Message: "the answer was cut off at the token limit"}
	}
	text := ""
	if c.Message.Content != nil {
		text = *c.Message.Content
	}
	return Response{Text: text, Provider: p.cfg.ID, Model: out.Model, InputTokens: out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens, StopReason: c.FinishReason}, nil
}
