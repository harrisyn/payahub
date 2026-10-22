// Package engine holds the deterministic core: edit rules, submission
// strategy, and sending a claim through a connector. Nothing here asks a
// model for output; connectors build payer payloads from validated fields.
package engine

import (
	"encoding/json"

	"github.com/harrisyn/payahub/internal/connector"
)

type Plan struct {
	LineMode string `json:"line_mode"` // realtime | bulk
	Timing   string `json:"timing"`    // early | deferred
}

// WholeClaim means one call carries every line and diagnosis.
func (p Plan) WholeClaim() bool { return p.Timing == "deferred" }

// ResolveConfig applies an organisation's overrides (a JSON object of
// SubmissionConfig keys) on top of the connector's defaults. Unknown keys are ignored.
func ResolveConfig(m connector.Manifest, overrides json.RawMessage) connector.SubmissionConfig {
	cfg := m.Submission
	if len(overrides) > 0 {
		_ = json.Unmarshal(overrides, &cfg)
	}
	return cfg
}

// ChoosePlan clamps a configured line mode and creation timing to a pair the
// connector can perform. Ported from core-api-v2's validate_submission_mode:
// it never fails, it corrects.
func ChoosePlan(m connector.Manifest, cfg connector.SubmissionConfig) Plan {
	mode, timing := cfg.LineSubmissionMode, cfg.ClaimCreationTiming

	invalidCombo := timing == "deferred" && mode == "realtime"
	unsupported := !m.IncrementalLines() && (mode == "realtime" || timing == "early")
	if invalidCombo || unsupported {
		mode, timing = "bulk", "deferred"
	}
	if timing == "deferred" && !m.WholeClaim() {
		timing = "early"
	}
	return Plan{LineMode: mode, Timing: timing}
}
