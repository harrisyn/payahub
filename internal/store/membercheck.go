package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MemberCheck is one question to a payer about a member: are they covered
// (verify), or please text them a code (send_otp).
type MemberCheck struct {
	ID           string          `json:"id"`
	Environment  string          `json:"environment"`
	PayerID      string          `json:"payer_id"`
	MemberNumber string          `json:"member_number"`
	Action       string          `json:"action"`
	Status       string          `json:"status"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`

	OrganizationID string `json:"-"`
	CreatedBy      string `json:"-"`
}

const memberCheckCols = `id, environment, payer_id, member_number, action, status, result, error_code, error_message,
	created_at, finished_at, organization_id, coalesce(created_by, '')`

func scanMemberCheck(row pgx.Row, extra ...any) (MemberCheck, error) {
	var m MemberCheck
	err := row.Scan(append([]any{&m.ID, &m.Environment, &m.PayerID, &m.MemberNumber, &m.Action, &m.Status, &m.Result,
		&m.ErrorCode, &m.ErrorMessage, &m.CreatedAt, &m.FinishedAt, &m.OrganizationID, &m.CreatedBy}, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// CreateMemberCheck records a pending check in q, the transaction that
// queues its job. A repeated idempotency key returns the first check
// (replayed) and records nothing; with a different request it returns
// ErrIdempotencyConflict.
func CreateMemberCheck(ctx context.Context, q Querier, m MemberCheck, idemKey string, sha []byte) (MemberCheck, bool, error) {
	out, err := scanMemberCheck(q.QueryRow(ctx, `
		INSERT INTO member_checks (id, organization_id, environment, payer_id, member_number, action, created_by,
			idempotency_key, request_sha256)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9)
		ON CONFLICT (organization_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING `+memberCheckCols,
		m.ID, m.OrganizationID, m.Environment, m.PayerID, m.MemberNumber, m.Action, m.CreatedBy, idemKey, sha))
	if !errors.Is(err, ErrNotFound) {
		return out, false, err
	}
	var prior []byte
	out, err = scanMemberCheck(q.QueryRow(ctx, `
		SELECT `+memberCheckCols+`, request_sha256 FROM member_checks WHERE organization_id = $1 AND idempotency_key = $2`,
		m.OrganizationID, idemKey), &prior)
	if err != nil {
		return out, false, err
	}
	if !bytes.Equal(prior, sha) {
		return out, false, ErrIdempotencyConflict
	}
	return out, true, nil
}

// GetMemberCheck reads a check; orgID "" reads any (the worker).
func (s *Store) GetMemberCheck(ctx context.Context, orgID, id string) (MemberCheck, error) {
	return scanMemberCheck(s.Pool.QueryRow(ctx, `
		SELECT `+memberCheckCols+` FROM member_checks WHERE id = $1 AND ($2 = '' OR organization_id = $2)`, id, orgID))
}

// FinishMemberCheck records the payer's answer, once, and wakes anyone
// waiting on it.
func (s *Store) FinishMemberCheck(ctx context.Context, id string, result any, code, message string) error {
	status := "done"
	var body []byte
	if code != "" {
		status = "failed"
	} else {
		var err error
		if body, err = json.Marshal(result); err != nil {
			return err
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		UPDATE member_checks SET status = $2, result = $3, error_code = $4, error_message = $5, finished_at = now()
		WHERE id = $1 AND status = 'pending'`, id, status, body, code, message); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, notifyWaiters, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WaitMemberCheck waits until the check has an answer or ctx ends.
func (s *Store) WaitMemberCheck(ctx context.Context, orgID, id string) (MemberCheck, error) {
	ch, release, err := s.watch(ctx, id)
	if err != nil {
		return MemberCheck{}, err
	}
	defer release()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		m, err := s.GetMemberCheck(ctx, orgID, id)
		if err != nil || m.Status != "pending" {
			return m, err
		}
		select {
		case <-ctx.Done():
			return m, ctx.Err()
		case <-ch:
		case <-poll.C:
		}
	}
}
