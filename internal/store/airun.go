package store

import (
	"context"
	"encoding/json"
	"time"
)

// AIRun is one model call, as recorded for audit.
type AIRun struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"organization_id,omitempty"`
	Task          string    `json:"task"`
	Provider      string    `json:"provider"`
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	PromptHash    string    `json:"prompt_hash"`
	InputSHA256   string    `json:"input_sha256,omitempty"`
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	DurationMS    int       `json:"duration_ms"`
	OK            bool      `json:"ok"`
	Error         string    `json:"error,omitempty"`
	Privacy       any       `json:"privacy,omitempty"` // the de-identifier's report
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Store) RecordAIRuns(ctx context.Context, runs []AIRun) error {
	for _, r := range runs {
		if r.ID == "" {
			r.ID = NewID("air")
		}
		privacy, err := json.Marshal(r.Privacy)
		if err != nil || r.Privacy == nil {
			privacy = []byte("{}")
		}
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO ai_runs (id, organization_id, task, provider, model, prompt_version, prompt_hash, input_sha256,
				input_tokens, output_tokens, duration_ms, ok, error, privacy)
			VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			r.ID, r.OrgID, r.Task, r.Provider, r.Model, r.PromptVersion, r.PromptHash, r.InputSHA256,
			r.InputTokens, r.OutputTokens, r.DurationMS, r.OK, r.Error, privacy); err != nil {
			return err
		}
	}
	return nil
}

// AIUsage sums the recorded runs per task and model since a time: what an
// operator reads to see how much the models are used and how often they fail.
type AIUsage struct {
	Task         string `json:"task"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	Runs         int64  `json:"runs"`
	Failed       int64  `json:"failed"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	AvgMS        int64  `json:"avg_ms"`
}

func (s *Store) AIUsageSince(ctx context.Context, since time.Time) ([]AIUsage, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT task, provider, model, count(*), count(*) FILTER (WHERE NOT ok),
		       COALESCE(sum(input_tokens), 0), COALESCE(sum(output_tokens), 0), COALESCE(avg(duration_ms), 0)::bigint
		FROM ai_runs WHERE created_at >= $1 GROUP BY task, provider, model ORDER BY task, count(*) DESC`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIUsage{}
	for rows.Next() {
		var u AIUsage
		if err := rows.Scan(&u.Task, &u.Provider, &u.Model, &u.Runs, &u.Failed, &u.InputTokens, &u.OutputTokens, &u.AvgMS); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
