package queue

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/engine"
	"github.com/harrisyn/payahub/internal/store"
)

// Confirm names one issue a person has looked at: its code and field, as
// the checks report them.
type Confirm struct {
	Code  string `json:"code"`
	Field string `json:"field"`
}

// ErrNotReviewable is returned for a confirmation of an issue the claim
// does not have, or one that is not a person's to confirm (an error must be
// fixed, not confirmed).
var ErrNotReviewable = errors.New("not an issue awaiting review on this claim")

// ErrClaimMoving is returned when the claim is already on its way to the payer.
var ErrClaimMoving = errors.New("the claim is already being sent")

// Review records a person's confirmations on a claim and checks it again,
// in one transaction: a claim whose only issues were awaiting review becomes
// ready to send.
func Review(ctx context.Context, st *store.Store, orgID, claimID, by, note string, confirms []Confirm) (*claim.Claim, engine.Validation, error) {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	defer tx.Rollback(ctx)
	c, queued, err := store.LockClaim(ctx, tx, orgID, claimID)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	if queued || c.State == claim.Submitted || c.State.Billable() || c.State == claim.Withdrawn {
		return c, engine.Validation{}, ErrClaimMoving
	}
	m, err := Prepare(ctx, tx, c)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	v, err := validate(ctx, tx, c, m)
	if err != nil {
		return nil, engine.Validation{}, err
	}
	now := time.Now().UTC()
	for _, cf := range confirms {
		awaiting := false
		for _, i := range v.Issues {
			if i.Severity == engine.SeverityReview && i.Code == cf.Code && i.Field == cf.Field {
				awaiting = true
			}
		}
		if !awaiting {
			return c, v, ErrNotReviewable
		}
		if !c.Reviewed(cf.Code, cf.Field) {
			c.Reviews = append(c.Reviews, claim.Review{Code: cf.Code, Field: cf.Field, By: by, Note: strings.TrimSpace(note), At: now})
		}
	}
	if v, err = validate(ctx, tx, c, m); err != nil {
		return nil, engine.Validation{}, err
	}
	from := c.State
	if claim.CanTransition(c.State, v.State) {
		c.State = v.State
	}
	if err := store.SaveClaim(ctx, tx, c, from, false, "reviewed", map[string]any{"confirmed": confirms, "by": by}); err != nil {
		return nil, engine.Validation{}, err
	}
	return c, v, tx.Commit(ctx)
}
