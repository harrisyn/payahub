package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/harrisyn/payahub/internal/claim"
)

// After a payer acknowledges a claim, Payahub asks it again until the claim
// is paid or rejected: soon at first, then less often, for StatusFollowFor.

// StatusFirstCheck is the wait before the first question, by environment.
// The sandbox answers at once, so it is asked within the minute.
var StatusFirstCheck = map[string]time.Duration{"sandbox": 30 * time.Second, "live": 2 * time.Hour}

// StatusFollowFor is how long after acknowledgement Payahub keeps asking.
const StatusFollowFor = 120 * 24 * time.Hour

// StatusBackoff is the wait after the nth check (1-based): 2h, 6h, 12h,
// then daily.
func StatusBackoff(n int) time.Duration {
	steps := []time.Duration{2 * time.Hour, 6 * time.Hour, 12 * time.Hour}
	if n-1 < len(steps) {
		return steps[n-1]
	}
	return 24 * time.Hour
}

// followed are the states whose outcome is still to come from the payer.
func followed(s claim.State) bool { return s == claim.Acknowledged || s == claim.Approved }

func scheduleStatusCheck(ctx context.Context, tx pgx.Tx, c *claim.Claim) error {
	if !followed(c.State) {
		_, err := tx.Exec(ctx, `UPDATE claims SET status_check_at = NULL WHERE id = $1`, c.ID)
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE claims SET status_check_at = coalesce(status_check_at, now() + make_interval(secs => $2))
		WHERE id = $1`, c.ID, StatusFirstCheck[c.Environment].Seconds())
	return err
}

// DueStatusCheck is a claim to ask the payer about.
type DueStatusCheck struct{ OrgID, ClaimID string }

// LeaseStatusChecks takes up to n due claims in tx and moves their next
// check a little later, so a second sweep does not queue them again while
// their job runs.
func LeaseStatusChecks(ctx context.Context, tx pgx.Tx, n int) ([]DueStatusCheck, error) {
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT id FROM claims WHERE status_check_at <= now()
			ORDER BY status_check_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		UPDATE claims c SET status_check_at = now() + interval '10 minutes'
		FROM due WHERE c.id = due.id
		RETURNING c.organization_id, c.id`, n)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[DueStatusCheck])
}

// StatusAsked records a check in tx: the claim's body (with what the payer
// said, already applied) and when to ask next.
func StatusAsked(ctx context.Context, tx pgx.Tx, c *claim.Claim, acknowledgedAt time.Time) error {
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	var checks int
	if err := tx.QueryRow(ctx, `UPDATE claims SET body = $2, status_checks = status_checks + 1 WHERE id = $1 RETURNING status_checks`,
		c.ID, body).Scan(&checks); err != nil {
		return err
	}
	var next *time.Time
	if followed(c.State) {
		t := time.Now().Add(StatusBackoff(checks))
		if t.Before(acknowledgedAt.Add(StatusFollowFor)) {
			next = &t
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE claims SET status_check_at = $2 WHERE id = $1`, c.ID, next); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, notifyWaiters, c.ID)
	return err
}

// AcknowledgedAt is when the claim was first acknowledged (its creation if
// the event is missing).
func AcknowledgedAt(ctx context.Context, q Querier, c *claim.Claim) (time.Time, error) {
	var t time.Time
	err := q.QueryRow(ctx, `SELECT created_at FROM claim_events WHERE claim_id = $1 AND to_state = 'acknowledged' ORDER BY id LIMIT 1`, c.ID).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return c.CreatedAt, nil
	}
	return t, err
}

// WaitStatusChecked waits until the claim has been asked about after since,
// or ctx ends.
func (s *Store) WaitStatusChecked(ctx context.Context, orgID, id string, since time.Time) (*claim.Claim, error) {
	ch, release, err := s.watch(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	for {
		c, err := s.GetClaim(ctx, orgID, id)
		if err != nil {
			return nil, err
		}
		if c.StatusCheckedAt != nil && c.StatusCheckedAt.After(since) {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return c, ctx.Err()
		case <-ch:
		case <-poll.C:
		}
	}
}
