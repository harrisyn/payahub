package queue

import (
	"context"
	"errors"
	"slices"
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

// Rechecked counts what a recheck did to the claims it looked at.
type Rechecked struct {
	Claims      int `json:"claims"`
	Ready       int `json:"ready"`
	NeedsReview int `json:"needs_review"`
	ToFix       int `json:"to_fix"`
}

// recheckBatch is how many claims one recheck transaction locks.
const recheckBatch = 100

// RecheckHeld runs the checks again on claims waiting to be sent whose lines
// carry one of codes and had no standard item: after the organisation maps a
// code, its lines resolve and the claims move on (ready to send, waiting for
// a person, or still with something to fix). Claims are locked a batch at a
// time; one already queued or sent meanwhile is left alone. A claim this
// misses is still resolved when it is sent.
func RecheckHeld(ctx context.Context, st *store.Store, orgID, by string, codes []string) (Rechecked, error) {
	var out Rechecked
	if len(codes) == 0 {
		return out, nil
	}
	ids, err := store.HeldClaimsWithCodes(ctx, st.Pool, orgID, codes, 5000)
	if err != nil {
		return out, err
	}
	for len(ids) > 0 {
		n := min(recheckBatch, len(ids))
		if err := recheckBatchOf(ctx, st, orgID, by, codes, ids[:n], &out); err != nil {
			return out, err
		}
		ids = ids[n:]
	}
	return out, nil
}

func recheckBatchOf(ctx context.Context, st *store.Store, orgID, by string, codes, ids []string, out *Rechecked) error {
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var done Rechecked
	for _, id := range ids {
		c, queued, err := store.LockClaim(ctx, tx, orgID, id)
		if err != nil {
			return err
		}
		if queued || !slices.Contains(store.HeldStates, string(c.State)) {
			continue
		}
		m, err := Prepare(ctx, tx, c)
		if err != nil {
			return err
		}
		v, err := validate(ctx, tx, c, m)
		if err != nil {
			return err
		}
		from := c.State
		switch {
		case v.State == claim.Ready:
			c.State = claim.AdvanceToReady(c.State)
		case claim.CanTransition(c.State, v.State):
			c.State = v.State
		}
		switch c.State {
		case claim.Ready:
			done.Ready++
		case claim.NeedsReview:
			done.NeedsReview++
		default:
			done.ToFix++
		}
		done.Claims++
		if err := store.SaveClaim(ctx, tx, c, from, false, "rechecked",
			map[string]any{"codes": codes, "by": by, "issues": v.Issues}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	out.Claims += done.Claims
	out.Ready += done.Ready
	out.NeedsReview += done.NeedsReview
	out.ToFix += done.ToFix
	return nil
}
