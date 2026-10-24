package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// AICheckRun is one run of the regression checks for a model task.
type AICheckRun struct {
	ID            int64           `json:"id"`
	Task          string          `json:"task"`
	Method        string          `json:"method"`
	Models        string          `json:"models"`
	PromptVersion string          `json:"prompt_version"`
	Total         int             `json:"total"`
	Passed        int             `json:"passed"`
	Groups        int             `json:"groups"`
	GroupsPassed  int             `json:"groups_passed"`
	Report        json.RawMessage `json:"report"`
	RunBy         string          `json:"run_by"`
	CreatedAt     time.Time       `json:"created_at"`
}

// RecordAICheck keeps a run of the checks.
func (s *Store) RecordAICheck(ctx context.Context, r AICheckRun) (AICheckRun, error) {
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO ai_check_runs (task, method, models, prompt_version, total, passed, groups, groups_passed, report, run_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at`,
		r.Task, r.Method, r.Models, r.PromptVersion, r.Total, r.Passed, r.Groups, r.GroupsPassed, r.Report, r.RunBy).
		Scan(&r.ID, &r.CreatedAt)
	return r, err
}

// AIChecks lists the latest runs for a task, newest first, with who ran them.
func (s *Store) AIChecks(ctx context.Context, task string, limit int) ([]AICheckRun, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.task, c.method, c.models, c.prompt_version, c.total, c.passed, c.groups, c.groups_passed,
		       c.report, coalesce(u.name, c.run_by), c.created_at
		FROM ai_check_runs c LEFT JOIN users u ON u.id = c.run_by
		WHERE c.task = $1 ORDER BY c.created_at DESC, c.id DESC LIMIT $2`, task, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AICheckRun, error) {
		var c AICheckRun
		return c, r.Scan(&c.ID, &c.Task, &c.Method, &c.Models, &c.PromptVersion, &c.Total, &c.Passed, &c.Groups,
			&c.GroupsPassed, &c.Report, &c.RunBy, &c.CreatedAt)
	})
}
