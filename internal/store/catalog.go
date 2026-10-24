package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/catalog"
)

// SeedCatalog copies the catalogue shipped in internal/catalog/data into the
// database: payers, items and codes that are not there yet. It never
// overwrites a row, so an operator's edits survive every release.
func (s *Store) SeedCatalog(ctx context.Context) error {
	d := catalog.Seed()
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		added := 0
		for _, it := range d.Items {
			body, err := json.Marshal(it)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `INSERT INTO catalog_items (id, kind, body) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
				it.ID, it.Kind, body)
			if err != nil {
				return err
			}
			added += int(tag.RowsAffected())
		}
		for _, p := range d.Payers {
			fields, err := json.Marshal(nonNil(p.Fields))
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `
				INSERT INTO catalog_payers (id, name, country, connector, fields, notes, generated_codes, inactive)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING`,
				p.ID, p.Name, p.Country, p.Connector, fields, p.Notes, p.GeneratedCodes, p.Inactive)
			if err != nil {
				return err
			}
			added += int(tag.RowsAffected())
			for item, pc := range d.Codes[p.ID] {
				tag, err := tx.Exec(ctx, `
					INSERT INTO catalog_payer_codes (payer_id, item_id, code, description) VALUES ($1, $2, $3, $4)
					ON CONFLICT DO NOTHING`, p.ID, item, pc.Code, pc.Description)
				if err != nil {
					return err
				}
				added += int(tag.RowsAffected())
			}
		}
		if added == 0 {
			return nil
		}
		_, err := tx.Exec(ctx, `INSERT INTO catalog_changes (kind, detail, changed_by) VALUES ('seed', $1, 'seed')`,
			map[string]int{"rows": added})
		return err
	})
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// LoadCatalog reads the catalogue from the database.
func LoadCatalog(ctx context.Context, q Querier) (catalog.Data, error) {
	d := catalog.Data{Codes: map[string]map[string]catalog.PayerCode{}, Lists: map[string][]catalog.ListEntry{}}
	rows, err := q.Query(ctx, `SELECT body FROM catalog_items ORDER BY id`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var body []byte
		var it catalog.Item
		if err := rows.Scan(&body); err != nil {
			rows.Close()
			return d, err
		}
		if err := json.Unmarshal(body, &it); err != nil {
			rows.Close()
			return d, err
		}
		d.Items = append(d.Items, it)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	rows, err = q.Query(ctx, `
		SELECT id, name, country, connector, fields, notes, generated_codes, inactive, rules FROM catalog_payers ORDER BY id`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var p catalog.Payer
		var fields, rules []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.Country, &p.Connector, &fields, &p.Notes, &p.GeneratedCodes, &p.Inactive, &rules); err != nil {
			rows.Close()
			return d, err
		}
		if err := errors.Join(json.Unmarshal(fields, &p.Fields), json.Unmarshal(rules, &p.Rules)); err != nil {
			rows.Close()
			return d, err
		}
		d.Payers = append(d.Payers, p)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	rows, err = q.Query(ctx, `SELECT payer_id, item_id, code, description FROM catalog_payer_codes`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var payer, item string
		var pc catalog.PayerCode
		if err := rows.Scan(&payer, &item, &pc.Code, &pc.Description); err != nil {
			rows.Close()
			return d, err
		}
		if d.Codes[payer] == nil {
			d.Codes[payer] = map[string]catalog.PayerCode{}
		}
		d.Codes[payer][item] = pc
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	rows, err = q.Query(ctx, `
		SELECT payer_id, code, name, grp, price, max_quantity, sex, min_age, max_age, needs_authorization
		FROM catalog_payer_lists ORDER BY payer_id, position`)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var payer string
		var e catalog.ListEntry
		if err := rows.Scan(&payer, &e.Code, &e.Name, &e.Group, &e.Price,
			&e.MaxQuantity, &e.Sex, &e.MinAge, &e.MaxAge, &e.NeedsAuthorization); err != nil {
			rows.Close()
			return d, err
		}
		d.Lists[payer] = append(d.Lists[payer], e)
	}
	return d, rows.Err()
}

func catalogVersion(ctx context.Context, q Querier) (int64, error) {
	var v int64
	err := q.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM catalog_changes`).Scan(&v)
	return v, err
}

// RefreshCatalog swaps in the database's catalogue when it changed since the
// last load. Every process calls it at start and every few seconds, and the
// API right after an operator's change, so all processes see the same
// catalogue within one interval.
func (s *Store) RefreshCatalog(ctx context.Context) error {
	v, err := catalogVersion(ctx, s.Pool)
	if err != nil {
		return err
	}
	if v == s.catalogVersion.Load() {
		return nil
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if v == s.catalogVersion.Load() {
		return nil
	}
	var d catalog.Data
	err = pgx.BeginTxFunc(ctx, s.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		if v, err = catalogVersion(ctx, tx); err != nil {
			return err
		}
		d, err = LoadCatalog(ctx, tx)
		return err
	})
	if err != nil {
		return err
	}
	c, err := catalog.Build(d)
	if err != nil {
		return fmt.Errorf("catalogue in the database: %w", err)
	}
	catalog.Use(c)
	s.catalogVersion.Store(v)
	return nil
}

// WatchCatalog refreshes the catalogue every interval until ctx ends.
func (s *Store) WatchCatalog(ctx context.Context, every time.Duration, onErr func(error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.RefreshCatalog(ctx); err != nil && ctx.Err() == nil {
				onErr(err)
			}
		}
	}
}

func logChange(ctx context.Context, tx pgx.Tx, payerID, kind, by string, detail any) error {
	_, err := tx.Exec(ctx, `INSERT INTO catalog_changes (payer_id, kind, detail, changed_by) VALUES ($1, $2, $3, $4)`,
		payerID, kind, detail, by)
	return err
}

// SavePayer creates or updates a payer.
func (s *Store) SavePayer(ctx context.Context, p catalog.Payer, by string) (created bool, err error) {
	n, err := s.SavePayers(ctx, []catalog.Payer{p}, by)
	return n == 1, err
}

// SavePayers creates or updates payers in one transaction, logging each, and
// reports how many were new.
func (s *Store) SavePayers(ctx context.Context, ps []catalog.Payer, by string) (created int, err error) {
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		for _, p := range ps {
			fields, err := json.Marshal(nonNil(p.Fields))
			if err != nil {
				return err
			}
			var inserted bool
			err = tx.QueryRow(ctx, `
				INSERT INTO catalog_payers (id, name, country, connector, fields, notes, inactive, updated_by, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
				ON CONFLICT (id) DO UPDATE SET name = $2, country = $3, connector = $4, fields = $5, notes = $6,
					inactive = $7, updated_by = $8, updated_at = now()
				RETURNING xmax = 0`, p.ID, p.Name, p.Country, p.Connector, fields, p.Notes, p.Inactive, by).Scan(&inserted)
			if err != nil {
				return err
			}
			kind := "payer_updated"
			if inserted {
				kind = "payer_created"
				created++
			}
			if err := logChange(ctx, tx, p.ID, kind, by, map[string]any{
				"name": p.Name, "connector": p.Connector, "fields": nonNil(p.Fields), "inactive": p.Inactive}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return created, s.RefreshCatalog(ctx)
}

// CodeChange sets a payer's code for a standard item; an empty code removes it.
type CodeChange struct {
	ItemID      string `json:"item_id"`
	Code        string `json:"code"`
	Description string `json:"description"`
}

// SetPayerCodes applies code changes for one payer in one transaction.
func (s *Store) SetPayerCodes(ctx context.Context, payerID string, changes []CodeChange, by string) error {
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		set, removed := 0, 0
		for _, c := range changes {
			if c.Code == "" {
				tag, err := tx.Exec(ctx, `DELETE FROM catalog_payer_codes WHERE payer_id = $1 AND item_id = $2`, payerID, c.ItemID)
				if err != nil {
					return err
				}
				removed += int(tag.RowsAffected())
				continue
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO catalog_payer_codes (payer_id, item_id, code, description, updated_by, updated_at)
				VALUES ($1, $2, $3, $4, $5, now())
				ON CONFLICT (payer_id, item_id) DO UPDATE SET code = $3, description = $4, updated_by = $5, updated_at = now()`,
				payerID, c.ItemID, c.Code, c.Description, by); err != nil {
				return err
			}
			set++
		}
		return logChange(ctx, tx, payerID, "codes_changed", by, map[string]any{"set": set, "removed": removed, "changes": changes})
	})
	if err != nil {
		return err
	}
	return s.RefreshCatalog(ctx)
}

// ReplacePayerList stores a payer's own code list, replacing the last one.
func (s *Store) ReplacePayerList(ctx context.Context, payerID string, entries []catalog.ListEntry, by string) error {
	limited := 0
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM catalog_payer_lists WHERE payer_id = $1`, payerID); err != nil {
			return err
		}
		rows := make([][]any, len(entries))
		for i, e := range entries {
			rows[i] = []any{payerID, i, e.Code, e.Name, e.Group, e.Price, e.MaxQuantity, e.Sex, e.MinAge, e.MaxAge, e.NeedsAuthorization}
			if e.Limited() {
				limited++
			}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"catalog_payer_lists"},
			[]string{"payer_id", "position", "code", "name", "grp", "price", "max_quantity", "sex", "min_age", "max_age",
				"needs_authorization"}, pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		return logChange(ctx, tx, payerID, "list_loaded", by, map[string]int{"entries": len(entries), "with_rules": limited})
	})
	if err != nil {
		return err
	}
	return s.RefreshCatalog(ctx)
}

// SavePayerRules sets how strictly the payer's list applies to claims.
func (s *Store) SavePayerRules(ctx context.Context, payerID string, r catalog.PayerRules, by string) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE catalog_payers SET rules = $2, updated_by = $3, updated_at = now() WHERE id = $1`,
			payerID, raw, by)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return logChange(ctx, tx, payerID, "rules_changed", by, r)
	})
	if err != nil {
		return err
	}
	return s.RefreshCatalog(ctx)
}

// CatalogChange is one line of a payer's change history.
type CatalogChange struct {
	Kind      string          `json:"kind"`
	Detail    json.RawMessage `json:"detail"`
	ChangedBy string          `json:"changed_by"`
	ChangedAt time.Time       `json:"changed_at"`
}

// PayerChanges lists the latest changes to a payer, newest first.
func (s *Store) PayerChanges(ctx context.Context, payerID string, limit int) ([]CatalogChange, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.kind, c.detail, coalesce(u.name, c.changed_by), c.changed_at
		FROM catalog_changes c LEFT JOIN users u ON u.id = c.changed_by
		WHERE c.payer_id = $1 ORDER BY c.id DESC LIMIT $2`, payerID, limit)
	if err != nil {
		return nil, err
	}
	out := []CatalogChange{}
	for rows.Next() {
		var c CatalogChange
		if err := rows.Scan(&c.Kind, &c.Detail, &c.ChangedBy, &c.ChangedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetOperator grants or removes operator rights by email.
func (s *Store) SetOperator(ctx context.Context, email string, on bool) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET operator = $2 WHERE lower(email) = lower($1)`, email, on)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Cached sessions pick the change up within keyTTL.
	return nil
}
