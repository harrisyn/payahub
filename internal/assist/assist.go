// Package assist is where models help with claims: matching a clinic's
// own item names to standard items, and reading a paper claim form.
//
// Everything a model says is a proposal. It is checked against the
// catalogue and the ICD-10 list before it is shown, carries the model and
// prompt version that produced it, and becomes part of a claim only when a
// person accepts it or it clears its per-use confidence threshold.
package assist

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/ai"
)

// Completer answers a model request; ai.Router is one.
type Completer interface {
	Complete(ctx context.Context, r ai.Request) (ai.Response, error)
}

// Run is one model call, for the audit log.
type Run struct {
	Task          string
	Provider      string
	Model         string
	PromptVersion string
	PromptHash    string
	InputSHA256   string
	InputTokens   int64
	OutputTokens  int64
	Duration      time.Duration
	Err           error
}

// promptHash fingerprints what the model was told, so a decision can be
// traced to the exact instructions and schema behind it.
func promptHash(system string, schema map[string]any) string {
	b, _ := json.Marshal(schema)
	h := sha256.Sum256(append([]byte(system+"\x00"), b...))
	return hex.EncodeToString(h[:8])
}

func sha(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// call sends r and records the run. The answer is decoded into out.
func call(ctx context.Context, c Completer, r ai.Request, version string, input []byte, out any) Run {
	run := Run{Task: r.Task, PromptVersion: version, PromptHash: promptHash(r.System, r.Schema), InputSHA256: sha(input)}
	start := time.Now()
	res, err := c.Complete(ctx, r)
	run.Duration = time.Since(start)
	run.Provider, run.Model, run.InputTokens, run.OutputTokens = res.Provider, res.Model, res.InputTokens, res.OutputTokens
	if err == nil {
		err = decodeJSON(res.Text, out)
	}
	run.Err = err
	return run
}

// decodeJSON reads a JSON object from a model's answer, allowing for a code
// fence or a sentence around it from endpoints that do not enforce JSON.
func decodeJSON(text string, out any) error {
	t := strings.TrimSpace(text)
	if i, j := strings.Index(t, "{"), strings.LastIndex(t, "}"); i >= 0 && j > i {
		t = t[i : j+1]
	}
	if err := json.Unmarshal([]byte(t), out); err != nil {
		return &ai.Error{Provider: "answer", Message: "the model's answer was not the JSON asked for: " + err.Error()}
	}
	return nil
}

func clamp(f float64) float64 {
	if f != f || f < 0 { // NaN or negative
		return 0
	}
	if f > 0.99 {
		return 0.99
	}
	return float64(int(f*100)) / 100
}

func object(props map[string]any) map[string]any {
	req := make([]string, 0, len(props))
	for k := range props {
		req = append(req, k)
	}
	slices.Sort(req)
	return map[string]any{"type": "object", "properties": props, "required": req, "additionalProperties": false}
}

var (
	str = map[string]any{"type": "string"}
	num = map[string]any{"type": "number"}
)

func array(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}
