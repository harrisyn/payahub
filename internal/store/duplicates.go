package store

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/harrisyn/payahub/internal/claim"
)

// FindDuplicates lists the organisation's other claims for the same payer,
// member and visit date, in the same environment. Withdrawn and rejected
// claims are left out: they will not be paid, so another claim is no repeat.
func FindDuplicates(ctx context.Context, q Querier, c *claim.Claim) ([]claim.Duplicate, error) {
	member := strings.TrimSpace(c.Member.MemberNumber)
	if member == "" || c.Encounter.StartDate.IsZero() {
		return nil, nil
	}
	rows, err := q.Query(ctx, `
		SELECT id, state, body->'lines' FROM claims
		WHERE organization_id = $1 AND environment = $2 AND payer_id = $3 AND id <> $4
		  AND upper(trim(body->'member'->>'member_number')) = upper($5)
		  AND body->'encounter'->>'start_date' = $6
		  AND state NOT IN ('withdrawn', 'rejected')
		ORDER BY created_at LIMIT 20`,
		c.OrganizationID, c.Environment, c.PayerID, c.ID, member, c.Encounter.StartDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	key := claim.LinesKey(c.Lines)
	var out []claim.Duplicate
	for rows.Next() {
		var d claim.Duplicate
		var raw []byte
		if err := rows.Scan(&d.ID, &d.State, &raw); err != nil {
			return nil, err
		}
		var lines []claim.Line
		_ = json.Unmarshal(raw, &lines)
		d.Exact = claim.LinesKey(lines) == key
		out = append(out, d)
	}
	return out, rows.Err()
}
