package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CodePick is a model's proposed code from a payer's list for a standard
// item. Code is empty when the model found none.
type CodePick struct {
	ItemID        string    `json:"item_id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Confidence    float64   `json:"confidence"`
	Reason        string    `json:"reason"`
	Model         string    `json:"model"`
	PromptVersion string    `json:"prompt_version"`
	CreatedAt     time.Time `json:"created_at"`
}

// SaveCodePicks replaces the payer's picks for the items asked about, so an
// item the model no longer answers loses its old pick.
func (s *Store) SaveCodePicks(ctx context.Context, payerID string, asked []string, picks []CodePick, by string) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM payer_code_picks WHERE payer_id = $1 AND item_id = ANY($2)`, payerID, asked); err != nil {
			return err
		}
		rows := make([][]any, len(picks))
		for i, p := range picks {
			rows[i] = []any{payerID, p.ItemID, p.Code, p.Name, p.Confidence, p.Reason, p.Model, p.PromptVersion, by}
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"payer_code_picks"},
			[]string{"payer_id", "item_id", "code", "name", "confidence", "reason", "model", "prompt_version", "asked_by"},
			pgx.CopyFromRows(rows))
		return err
	})
}

// CodePicks are the payer's stored picks by item.
func (s *Store) CodePicks(ctx context.Context, payerID string) (map[string]CodePick, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT item_id, code, name, confidence, reason, model, prompt_version, created_at
		FROM payer_code_picks WHERE payer_id = $1`, payerID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (CodePick, error) {
		var p CodePick
		return p, r.Scan(&p.ItemID, &p.Code, &p.Name, &p.Confidence, &p.Reason, &p.Model, &p.PromptVersion, &p.CreatedAt)
	})
	out := make(map[string]CodePick, len(list))
	for _, p := range list {
		out[p.ItemID] = p
	}
	return out, err
}
