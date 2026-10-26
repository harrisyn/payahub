package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// DefaultClaudeModel is offered when an operator adds Claude.
const DefaultClaudeModel = "claude-opus-5-5"

type claude struct {
	cfg    Config
	client anthropic.Client
}

func newClaude(c Config) *claude {
	opts := []option.RequestOption{
		option.WithAPIKey(c.APIKey),
		option.WithMaxRetries(2),
		option.WithRequestTimeout(5 * time.Minute),
	}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	return &claude{cfg: c, client: anthropic.NewClient(opts...)}
}

func (p *claude) Complete(ctx context.Context, r Request) (Response, error) {
	if err := checkCleared(r); err != nil {
		return Response{}, err
	}
	var blocks []anthropic.BetaContentBlockParamUnion
	for _, part := range r.Parts {
		switch {
		case part.MediaType == "application/pdf":
			blocks = append(blocks, anthropic.NewBetaDocumentBlock(anthropic.BetaBase64PDFSourceParam{
				Data: base64.StdEncoding.EncodeToString(part.Data)}))
		case strings.HasPrefix(part.MediaType, "image/"):
			blocks = append(blocks, anthropic.NewBetaImageBlock(anthropic.BetaBase64ImageSourceParam{
				Data: base64.StdEncoding.EncodeToString(part.Data), MediaType: anthropic.BetaBase64ImageSourceMediaType(part.MediaType)}))
		case part.Text != "":
			blocks = append(blocks, anthropic.NewBetaTextBlock(part.Text))
		}
	}
	maxTokens := r.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16000
	}
	effort := p.cfg.Effort
	if effort == "" {
		effort = "high"
	}
	params := anthropic.BetaMessageNewParams{
		Model:        anthropic.Model(p.cfg.Model),
		MaxTokens:    int64(maxTokens),
		Messages:     []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(blocks...)},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffort(effort)},
	}
	if r.System != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: r.System}}
	}
	if r.Schema != nil {
		params.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: r.Schema}
	}
	// On Anthropic's own API a declined request is answered by the model
	// the API picks for that kind of refusal, within the same call.
	if p.cfg.BaseURL == "" {
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}
	msg, err := p.client.Beta.Messages.New(ctx, params)
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) {
			return Response{}, &Error{Provider: p.cfg.ID, Status: apierr.StatusCode,
				Retryable: retryableStatus(apierr.StatusCode), Message: apierr.Error()}
		}
		return Response{}, &Error{Provider: p.cfg.ID, Retryable: ctx.Err() == nil, Message: err.Error()}
	}
	switch msg.StopReason {
	case anthropic.BetaStopReasonRefusal:
		return Response{}, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return Response{}, &Error{Provider: p.cfg.ID, Message: "the answer was cut off at the token limit"}
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	return Response{Text: text.String(), Provider: p.cfg.ID, Model: string(msg.Model),
		InputTokens: msg.Usage.InputTokens, OutputTokens: msg.Usage.OutputTokens, StopReason: string(msg.StopReason)}, nil
}
