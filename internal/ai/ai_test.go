package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/harrisyn/payahub/internal/deid"
)

type seen struct {
	path   string
	header http.Header
	body   map[string]any
}

func fake(t *testing.T, status int, reply string) (*httptest.Server, *seen) {
	t.Helper()
	s := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.path, s.header = r.URL.Path, r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &s.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

// nameOCR finds a "Name:" label at the top of any page.
type nameOCR struct{}

func (nameOCR) Words(context.Context, image.Image) ([]deid.Word, error) {
	return []deid.Word{{Text: "Name:", Box: image.Rect(2, 2, 20, 10), Line: "1"}}, nil
}

// form is a redacted page and a scrubbed instruction: what the de-identifier
// lets through.
var form = func() Request {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewGray(image.Rect(0, 0, 60, 40)))
	red, err := deid.Redact(context.Background(), nameOCR{}, b.Bytes(), "image/png", deid.Identity{})
	if err != nil {
		panic(err)
	}
	text, c := deid.Catalogue("Extract the member number.")
	return Request{
		System: "Read the claim form.",
		Parts:  []Part{{MediaType: "image/png", Data: red.Pages[0], Name: "form.png"}, Text(text)},
		Schema: map[string]any{"type": "object", "properties": map[string]any{"member_number": map[string]any{"type": "string"}},
			"required": []string{"member_number"}, "additionalProperties": false},
		SchemaName: "claim_form",
		Cleared:    deid.Join(red.Cleared, c),
	}
}()

func hi() Request {
	t, c := deid.Catalogue("hi")
	return Request{Parts: []Part{Text(t)}, Cleared: c}
}

func TestNothingUnscrubbedIsSent(t *testing.T) {
	srv, s := fake(t, 200, `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":"ok"}}],"usage":{}}`)
	p, _ := New(Config{ID: "x", Kind: OpenAICompatible, Model: "m", BaseURL: srv.URL})
	raw := Request{Parts: []Part{Text("Patient Kwame Mensah, NHIS 48211907")}}
	if _, err := p.Complete(context.Background(), raw); !errors.Is(err, ErrNotDeidentified) {
		t.Fatalf("raw text: %v", err)
	}
	// A cleared request with one more part added after scrubbing.
	r := form
	r.Parts = append(slices.Clone(r.Parts), Part{MediaType: "application/pdf", Data: []byte("%PDF-1.4")})
	if _, err := (Router{Providers: []Provider{p}}).Complete(context.Background(), r); !errors.Is(err, ErrNotDeidentified) {
		t.Fatalf("added file: %v", err)
	}
	if s.path != "" {
		t.Fatalf("something was sent")
	}
}

func TestClaudeSendsDocumentsAndSchema(t *testing.T) {
	srv, s := fake(t, 200, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5",
		"content":[{"type":"text","text":"{\"member_number\":\"M-1\"}"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":120,"output_tokens":9}}`)
	p, err := New(Config{ID: "claude", Kind: Anthropic, Model: DefaultClaudeModel, APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Complete(context.Background(), form)
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != `{"member_number":"M-1"}` || res.InputTokens != 120 || res.Provider != "claude" {
		t.Fatalf("response: %+v", res)
	}
	if s.path != "/v1/messages" || s.header.Get("X-Api-Key") != "k" || s.header.Get("Anthropic-Version") == "" {
		t.Fatalf("request: %s %v", s.path, s.header)
	}
	oc := s.body["output_config"].(map[string]any)
	if oc["effort"] != "high" || oc["format"].(map[string]any)["type"] != "json_schema" {
		t.Fatalf("output_config: %v", oc)
	}
	content := s.body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["type"] != "image" || content[1].(map[string]any)["type"] != "text" {
		t.Fatalf("content: %v", content)
	}
	if _, ok := s.body["fallbacks"]; ok {
		t.Fatalf("fallbacks sent to a custom endpoint")
	}
}

func TestClaudeOverloadIsRetryableAndRefusalIsReported(t *testing.T) {
	srv, _ := fake(t, 529, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	p, _ := New(Config{ID: "claude", Kind: Anthropic, Model: DefaultClaudeModel, APIKey: "k", BaseURL: srv.URL})
	_, err := p.Complete(context.Background(), form)
	var pe *Error
	if !errors.As(err, &pe) || !pe.Retryable || pe.Status != 529 {
		t.Fatalf("overload: %v", err)
	}
	srv2, _ := fake(t, 200, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],
		"stop_reason":"refusal","stop_details":{"type":"refusal","category":null,"explanation":""},"usage":{"input_tokens":1,"output_tokens":0}}`)
	p2, _ := New(Config{ID: "claude", Kind: Anthropic, Model: DefaultClaudeModel, APIKey: "k", BaseURL: srv2.URL})
	if _, err := p2.Complete(context.Background(), form); !errors.Is(err, ErrRefused) {
		t.Fatalf("refusal: %v", err)
	}
}

func TestOpenAISchemaAndCompatibleObjectMode(t *testing.T) {
	srv, s := fake(t, 200, `{"model":"gpt-x","choices":[{"finish_reason":"stop","message":{"content":"{\"member_number\":\"M-2\"}"}}],
		"usage":{"prompt_tokens":50,"completion_tokens":7}}`)
	p, _ := New(Config{ID: "oa", Kind: OpenAI, Model: "gpt-x", APIKey: "k", BaseURL: srv.URL})
	res, err := p.Complete(context.Background(), form)
	if err != nil || res.Text != `{"member_number":"M-2"}` || res.OutputTokens != 7 {
		t.Fatalf("%v %+v", err, res)
	}
	if s.path != "/chat/completions" || s.header.Get("Authorization") != "Bearer k" {
		t.Fatalf("request: %s %v", s.path, s.header)
	}
	rf := s.body["response_format"].(map[string]any)
	if rf["type"] != "json_schema" || rf["json_schema"].(map[string]any)["strict"] != true || s.body["max_completion_tokens"] == nil {
		t.Fatalf("body: %v", s.body)
	}
	parts := s.body["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if f := parts[0].(map[string]any)["image_url"].(map[string]any); !strings.HasPrefix(f["url"].(string), "data:image/png;base64,") {
		t.Fatalf("image part: %v", parts[0])
	}

	srv2, s2 := fake(t, 200, `{"model":"llama","choices":[{"finish_reason":"stop","message":{"content":"{}"}}],"usage":{}}`)
	p2, err := New(Config{ID: "local", Kind: OpenAICompatible, Model: "llama", BaseURL: srv2.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Complete(context.Background(), form); err != nil {
		t.Fatal(err)
	}
	if s2.path != "/v1/chat/completions" || s2.body["response_format"].(map[string]any)["type"] != "json_object" || s2.body["max_tokens"] == nil {
		t.Fatalf("compatible body: %s %v", s2.path, s2.body)
	}
	if sys := s2.body["messages"].([]any)[0].(map[string]any)["content"].(string); !strings.Contains(sys, "member_number") {
		t.Fatalf("schema not described to an endpoint without schema mode: %q", sys)
	}
	if _, err := New(Config{Kind: OpenAICompatible, Model: "m"}); err == nil {
		t.Fatalf("compatible without a base URL")
	}
}

func TestRouterFallsThroughOnlyWhenAnotherMightAnswer(t *testing.T) {
	down, _ := fake(t, 429, `{"error":{"message":"rate limited"}}`)
	bad, _ := fake(t, 400, `{"error":{"message":"bad schema"}}`)
	up, _ := fake(t, 200, `{"model":"m","choices":[{"finish_reason":"stop","message":{"content":"ok"}}],"usage":{}}`)
	mk := func(id, url string) Provider {
		p, _ := New(Config{ID: id, Kind: OpenAICompatible, Model: "m", BaseURL: url})
		return p
	}
	res, err := Router{Providers: []Provider{mk("down", down.URL), mk("up", up.URL)}}.Complete(context.Background(), hi())
	if err != nil || res.Provider != "up" {
		t.Fatalf("rate-limited provider not skipped: %v %+v", err, res)
	}
	if _, err := (Router{Providers: []Provider{mk("bad", bad.URL), mk("up", up.URL)}}).Complete(context.Background(), hi()); err == nil {
		t.Fatalf("a request error was retried elsewhere")
	}
	if _, err := (Router{}).Complete(context.Background(), Request{}); !errors.Is(err, ErrNoProvider) {
		t.Fatalf("no providers: %v", err)
	}
}
