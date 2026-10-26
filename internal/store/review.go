package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/claim"
)

// HeldStates are the states of a claim that has not gone to the payer and
// can still change: what the review queue and a recheck look at.
var HeldStates = []string{string(claim.Draft), string(claim.Mapped), string(claim.Validated), string(claim.NeedsReview)}

// HeldCode is one of the organisation's own codes that has no standard item,
// on lines of claims still waiting to be sent.
type HeldCode struct {
	Code     string    `json:"code"`
	Name     string    `json:"name"`
	Claims   int       `json:"claims"`
	Lines    int       `json:"lines"`
	Amount   int64     `json:"amount"`
	Currency string    `json:"currency"`
	Oldest   time.Time `json:"oldest_claim_at"`
}

// heldLine is the condition for a line holding a claim back: it carries the
// organisation's own code, and neither a standard item nor a payer code.
const heldLine = `
	l->'code' IS NOT NULL AND coalesce(l->'code'->>'value', '') <> ''
	AND coalesce(l->'code'->>'system', '') <> 'payahub'
	AND coalesce(l->>'item', '') = '' AND l->'mapping'->'target' IS NULL`

// HeldCodes lists the organisation's own codes that hold claims back because
// no standard item is mapped to them, the codes on most claims first. The
// name is the newest line's description.
func (s *Store) HeldCodes(ctx context.Context, orgID, env string, limit int) ([]HeldCode, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT l->'code'->>'value',
		       (array_agg(l->>'description' ORDER BY c.created_at DESC))[1],
		       count(DISTINCT c.id), count(*),
		       sum(coalesce((l->>'quantity')::bigint, 1) * coalesce((l->>'unit_price')::bigint, 0)),
		       min(c.currency), min(c.created_at)
		FROM claims c CROSS JOIN LATERAL jsonb_array_elements(c.body->'lines') l
		WHERE c.organization_id = $1 AND ($2 = '' OR c.environment = $2)
		  AND c.state = ANY($3) AND NOT c.submission_queued AND `+heldLine+`
		  AND NOT EXISTS (SELECT 1 FROM item_mappings m
		                  WHERE m.organization_id = c.organization_id AND m.local_code = l->'code'->>'value')
		GROUP BY 1
		ORDER BY count(DISTINCT c.id) DESC, 1
		LIMIT $4`, orgID, env, HeldStates, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (HeldCode, error) {
		var h HeldCode
		return h, r.Scan(&h.Code, &h.Name, &h.Claims, &h.Lines, &h.Amount, &h.Currency, &h.Oldest)
	})
}

// HeldClaimsWithCodes returns the ids of claims waiting to be sent that have
// an unresolved line with one of codes, oldest first.
func HeldClaimsWithCodes(ctx context.Context, q Querier, orgID string, codes []string, limit int) ([]string, error) {
	rows, err := q.Query(ctx, `
		SELECT c.id FROM claims c
		WHERE c.organization_id = $1 AND c.state = ANY($2) AND NOT c.submission_queued
		  AND EXISTS (SELECT 1 FROM jsonb_array_elements(c.body->'lines') l
		              WHERE `+heldLine+` AND l->'code'->>'value' = ANY($3))
		ORDER BY c.created_at
		LIMIT $4`, orgID, HeldStates, codes, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// CountClaims counts the organisation's claims in a state.
func (s *Store) CountClaims(ctx context.Context, orgID, env, state string) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM claims WHERE organization_id = $1 AND ($2 = '' OR environment = $2) AND state = $3`,
		orgID, env, state).Scan(&n)
	return n, err
}

// ClaimsInState returns the organisation's claims in a state, oldest first:
// the order a person works through a queue in.
func (s *Store) ClaimsInState(ctx context.Context, orgID, env, state string, limit int) ([]*claim.Claim, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT body FROM claims
		WHERE organization_id = $1 AND ($2 = '' OR environment = $2) AND state = $3
		ORDER BY created_at LIMIT $4`, orgID, env, state, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (*claim.Claim, error) {
		var body []byte
		if err := r.Scan(&body); err != nil {
			return nil, err
		}
		var c claim.Claim
		return &c, json.Unmarshal(body, &c)
	})
}
