package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/ai"
	"github.com/harrisyn/payahub/internal/vault"
)

// AIProvider is an operator's model provider setting as the console sees it:
// never the key itself.
type AIProvider struct {
	ID           string     `json:"id"`
	Kind         ai.Kind    `json:"kind"`
	Name         string     `json:"name"`
	BaseURL      string     `json:"base_url"`
	Model        string     `json:"model"`
	Effort       string     `json:"effort"`
	JSONMode     string     `json:"json_mode"`
	Tasks        []string   `json:"tasks"`
	Priority     int        `json:"priority"`
	Enabled      bool       `json:"enabled"`
	HasKey       bool       `json:"has_key"`
	KeyHint      string     `json:"key_hint"`
	CheckedAt    *time.Time `json:"checked_at,omitempty"`
	CheckOK      *bool      `json:"check_ok,omitempty"`
	CheckMessage *string    `json:"check_message,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
	sealed       []byte
}

const aiCols = `id, kind, name, base_url, model, effort, json_mode, tasks, priority, enabled, api_key_sealed, key_hint,
	checked_at, check_ok, check_message, updated_at`

func scanAI(r pgx.Row) (AIProvider, error) {
	var p AIProvider
	err := r.Scan(&p.ID, &p.Kind, &p.Name, &p.BaseURL, &p.Model, &p.Effort, &p.JSONMode, &p.Tasks, &p.Priority, &p.Enabled,
		&p.sealed, &p.KeyHint, &p.CheckedAt, &p.CheckOK, &p.CheckMessage, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	p.HasKey = len(p.sealed) > 0
	return p, err
}

func (s *Store) ListAIProviders(ctx context.Context) ([]AIProvider, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+aiCols+` FROM ai_providers ORDER BY priority, id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AIProvider, error) { return scanAI(r) })
}

func (s *Store) GetAIProvider(ctx context.Context, id string) (AIProvider, error) {
	return scanAI(s.Pool.QueryRow(ctx, `SELECT `+aiCols+` FROM ai_providers WHERE id = $1`, id))
}

func aiAAD(id string) string { return "ai/" + id }

// SaveAIProvider creates or updates a provider. A key, when given, is sealed
// and replaces the old one; an empty key keeps it.
func (s *Store) SaveAIProvider(ctx context.Context, v *vault.Vault, p AIProvider, key, by string) (AIProvider, bool, error) {
	var sealed []byte
	hint := ""
	if key != "" {
		var err error
		if sealed, err = v.Seal(map[string]string{"api_key": key}, aiAAD(p.ID)); err != nil {
			return p, false, err
		}
		hint = key[max(0, len(key)-4):]
	}
	var created bool
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO ai_providers (id, kind, name, base_url, model, effort, json_mode, tasks, priority, enabled,
		                          api_key_sealed, key_hint, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''))
		ON CONFLICT (id) DO UPDATE SET kind = EXCLUDED.kind, name = EXCLUDED.name, base_url = EXCLUDED.base_url,
		    model = EXCLUDED.model, effort = EXCLUDED.effort, json_mode = EXCLUDED.json_mode, tasks = EXCLUDED.tasks,
		    priority = EXCLUDED.priority, enabled = EXCLUDED.enabled,
		    api_key_sealed = COALESCE(EXCLUDED.api_key_sealed, ai_providers.api_key_sealed),
		    key_hint = CASE WHEN EXCLUDED.api_key_sealed IS NULL THEN ai_providers.key_hint ELSE EXCLUDED.key_hint END,
		    -- A changed setting has not been checked.
		    checked_at = NULL, check_ok = NULL, check_message = NULL,
		    updated_by = EXCLUDED.updated_by, updated_at = now()
		RETURNING (xmax = 0)`,
		p.ID, p.Kind, p.Name, p.BaseURL, p.Model, p.Effort, p.JSONMode, p.Tasks, p.Priority, p.Enabled, sealed, hint, by).Scan(&created)
	if err != nil {
		return p, false, err
	}
	saved, err := s.GetAIProvider(ctx, p.ID)
	return saved, created, err
}

func (s *Store) DeleteAIProvider(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM ai_providers WHERE id = $1`, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) SetAICheck(ctx context.Context, id string, ok bool, message string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE ai_providers SET checked_at = now(), check_ok = $2, check_message = NULLIF($3, '') WHERE id = $1`,
		id, ok, message)
	return err
}

// AIConfig opens a provider's key and returns its full configuration, for
// the server only.
func (s *Store) AIConfig(v *vault.Vault, p AIProvider) (ai.Config, error) {
	c := ai.Config{ID: p.ID, Kind: p.Kind, Name: p.Name, BaseURL: p.BaseURL, Model: p.Model, Effort: p.Effort, JSONMode: p.JSONMode}
	if len(p.sealed) > 0 {
		m, err := v.Open(p.sealed, aiAAD(p.ID))
		if err != nil {
			return c, err
		}
		c.APIKey = m["api_key"]
	}
	return c, nil
}

// AIRouter returns the enabled providers that serve task, in priority order.
func (s *Store) AIRouter(ctx context.Context, v *vault.Vault, task string) (ai.Router, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+aiCols+` FROM ai_providers WHERE enabled AND $1 = ANY(tasks) ORDER BY priority, id`, task)
	if err != nil {
		return ai.Router{}, err
	}
	ps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (AIProvider, error) { return scanAI(r) })
	if err != nil {
		return ai.Router{}, err
	}
	var rt ai.Router
	for _, p := range ps {
		c, err := s.AIConfig(v, p)
		if err != nil {
			return rt, err
		}
		prov, err := ai.New(c)
		if err != nil {
			return rt, err
		}
		rt.Providers = append(rt.Providers, prov)
	}
	return rt, nil
}
