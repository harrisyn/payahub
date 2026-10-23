package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Lead is one enquiry from the public site.
type Lead struct {
	ID            string    `json:"id"`
	Kind          string    `json:"kind"` // contact, platform, payer or specs
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Phone         string    `json:"phone"`
	Organization  string    `json:"organization"`
	PayerName     string    `json:"payer_name"`
	MonthlyClaims *int      `json:"monthly_claims"`
	PayerContact  string    `json:"payer_contact"`
	Message       string    `json:"message"`
	CreatedAt     time.Time `json:"created_at"`
}

const leadCols = `id, kind, name, email, phone, organization, payer_name, monthly_claims, payer_contact, message, created_at`

func scanLead(r pgx.Row) (Lead, error) {
	var l Lead
	return l, r.Scan(&l.ID, &l.Kind, &l.Name, &l.Email, &l.Phone, &l.Organization, &l.PayerName,
		&l.MonthlyClaims, &l.PayerContact, &l.Message, &l.CreatedAt)
}

// CreateLead saves an enquiry. A repeated idempotency key returns the lead
// first saved under it, with replayed set, so a double submit saves one lead.
func (s *Store) CreateLead(ctx context.Context, l Lead, idemKey string) (out Lead, replayed bool, err error) {
	var key *string
	if idemKey != "" {
		key = &idemKey
	}
	out, err = scanLead(s.Pool.QueryRow(ctx, `
		INSERT INTO leads (id, kind, name, email, phone, organization, payer_name, monthly_claims, payer_contact, message, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING `+leadCols,
		NewID("lead"), l.Kind, l.Name, l.Email, l.Phone, l.Organization, l.PayerName, l.MonthlyClaims, l.PayerContact, l.Message, key))
	if errors.Is(err, pgx.ErrNoRows) && key != nil {
		out, err = scanLead(s.Pool.QueryRow(ctx, `SELECT `+leadCols+` FROM leads WHERE idempotency_key = $1`, idemKey))
		return out, true, err
	}
	return out, false, err
}

// ListLeads is the newest enquiries first, optionally of one kind.
func (s *Store) ListLeads(ctx context.Context, kind string, limit int) ([]Lead, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+leadCols+` FROM leads
		WHERE $1 = '' OR kind = $1 ORDER BY created_at DESC LIMIT $2`, kind, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Lead, error) { return scanLead(r) })
}
