package engine

import (
	"context"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
)

// Send delivers a claim to the payer by the plan. It is safe to call again
// after a failure: callers reconcile with FindClaim first, and the incremental
// path reuses the payer claim number and relies on idempotent line adds.
func Send(ctx context.Context, conn connector.Connector, c *claim.Claim, plan Plan) (connector.Receipt, error) {
	if plan.WholeClaim() {
		return conn.SubmitWholeClaim(ctx, c)
	}
	number := c.PayerClaimNumber
	if number == "" {
		var err error
		if number, err = conn.OpenClaim(ctx, c); err != nil {
			return connector.Receipt{}, err
		}
	}
	for _, l := range c.Lines {
		if _, err := conn.AddLine(ctx, number, l); err != nil {
			return connector.Receipt{PayerClaimNumber: number}, err
		}
	}
	for _, d := range c.Diagnoses {
		if err := conn.AddDiagnosis(ctx, number, d); err != nil {
			return connector.Receipt{PayerClaimNumber: number}, err
		}
	}
	return conn.FinalizeClaim(ctx, number, c)
}

// Outcome is what a send attempt means for the claim.
type Outcome struct {
	State   claim.State
	Final   bool // no further attempts
	Receipt *connector.Receipt
	Err     *connector.Error
}

// Decide maps a send result to the claim's next state. A claim is only ever
// sent from Submitted, so the transitions below are always valid.
func Decide(r connector.Receipt, err error) Outcome {
	if err == nil {
		state := claim.Submitted
		if r.Acknowledged {
			state = claim.Acknowledged
		}
		return Outcome{State: state, Final: true, Receipt: &r}
	}
	ce := connector.AsError(err)
	if ce.Final() {
		return Outcome{State: claim.Rejected, Final: true, Err: ce}
	}
	if ce.Retryable() {
		// The payer may or may not have it: stay submitted, and the queue
		// retries with backoff after reconciling.
		return Outcome{State: claim.Submitted, Err: ce}
	}
	// Never reached the payer for a reason a person must fix (credentials,
	// OTP, authorization, an unsupported verb): back to ready.
	return Outcome{State: claim.Ready, Final: true, Err: ce}
}
