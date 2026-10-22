package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Price is one line of an organisation's price list.
type Price struct {
	LocalCode string    `json:"local_code"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind,omitempty"`
	UnitPrice int64     `json:"unit_price"` // minor units
	Currency  string    `json:"currency"`
	ItemID    string    `json:"item_id,omitempty"` // the confirmed standard item, from item_mappings
	UpdatedAt time.Time `json:"updated_at"`
}

// ListPrices is the organisation's price list in code order, with the
// standard item each code is mapped to.
func (s *Store) ListPrices(ctx context.Context, orgID string) ([]Price, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT p.local_code, p.name, p.kind, p.unit_price, p.currency, coalesce(m.item_id, ''), p.updated_at
		FROM price_list p
		LEFT JOIN item_mappings m ON m.organization_id = p.organization_id AND m.local_code = p.local_code
		WHERE p.organization_id = $1 ORDER BY p.local_code`, orgID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Price, error) {
		var p Price
		return p, r.Scan(&p.LocalCode, &p.Name, &p.Kind, &p.UnitPrice, &p.Currency, &p.ItemID, &p.UpdatedAt)
	})
}

// PriceChanges counts what SavePrices did.
type PriceChanges struct {
	Added     int `json:"added"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
	Removed   int `json:"removed"`
}

// SavePrices adds or updates prices in one transaction. With replace, codes
// not in ps are removed, so the list becomes exactly ps.
func (s *Store) SavePrices(ctx context.Context, orgID string, ps []Price, replace bool, by string) (PriceChanges, error) {
	var out PriceChanges
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE TEMP TABLE incoming_prices (
			local_code text PRIMARY KEY, name text, kind text, unit_price bigint, currency text) ON COMMIT DROP`); err != nil {
			return err
		}
		rows := make([][]any, len(ps))
		for i, p := range ps {
			rows[i] = []any{p.LocalCode, p.Name, p.Kind, p.UnitPrice, p.Currency}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"incoming_prices"},
			[]string{"local_code", "name", "kind", "unit_price", "currency"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `
			SELECT count(*) FILTER (WHERE p.local_code IS NULL),
			       count(*) FILTER (WHERE p.local_code IS NOT NULL AND (p.name, p.kind, p.unit_price, p.currency) IS DISTINCT FROM (i.name, i.kind, i.unit_price, i.currency))
			FROM incoming_prices i
			LEFT JOIN price_list p ON p.organization_id = $1 AND p.local_code = i.local_code`, orgID).Scan(&out.Added, &out.Changed)
		if err != nil {
			return err
		}
		out.Unchanged = len(ps) - out.Added - out.Changed
		if _, err := tx.Exec(ctx, `
			INSERT INTO price_list (organization_id, local_code, name, kind, unit_price, currency, updated_by)
			SELECT $1, local_code, name, kind, unit_price, currency, $2 FROM incoming_prices
			ON CONFLICT (organization_id, local_code) DO UPDATE
			SET name = EXCLUDED.name, kind = EXCLUDED.kind, unit_price = EXCLUDED.unit_price,
			    currency = EXCLUDED.currency, updated_by = EXCLUDED.updated_by, updated_at = now()
			WHERE (price_list.name, price_list.kind, price_list.unit_price, price_list.currency)
			      IS DISTINCT FROM (EXCLUDED.name, EXCLUDED.kind, EXCLUDED.unit_price, EXCLUDED.currency)`, orgID, by); err != nil {
			return err
		}
		if replace {
			tag, err := tx.Exec(ctx, `
				DELETE FROM price_list p WHERE p.organization_id = $1
				AND NOT EXISTS (SELECT 1 FROM incoming_prices i WHERE i.local_code = p.local_code)`, orgID)
			if err != nil {
				return err
			}
			out.Removed = int(tag.RowsAffected())
		}
		return nil
	})
	return out, err
}

func (s *Store) DeletePrice(ctx context.Context, orgID, localCode string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM price_list WHERE organization_id = $1 AND local_code = $2`, orgID, localCode)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// PricesFor returns the price list entries for the given own codes.
func PricesFor(ctx context.Context, q Querier, orgID string, codes []string) (map[string]Price, error) {
	out := map[string]Price{}
	if len(codes) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `
		SELECT local_code, name, unit_price, currency FROM price_list
		WHERE organization_id = $1 AND local_code = ANY($2)`, orgID, codes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Price
		if err := rows.Scan(&p.LocalCode, &p.Name, &p.UnitPrice, &p.Currency); err != nil {
			return nil, err
		}
		out[p.LocalCode] = p
	}
	return out, rows.Err()
}
