package engine

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/harrisyn/payahub/internal/claim"
	"github.com/harrisyn/payahub/internal/connector"
)

// Progress is what the payer holds of a claim sent line by line. Send
// records it after every step, so a retry after a crash or a timeout resumes
// where the last run stopped, and a claim corrected after a rejection is
// brought in line with the payer's copy (changed lines updated, removed ones
// deleted) instead of being opened a second time.
type Progress struct {
	PayerClaimNumber string              `json:"payer_claim_number,omitempty"`
	Lines            map[string]SentLine `json:"lines,omitempty"`     // by SendLine.Key
	Diagnoses        map[string]string   `json:"diagnoses,omitempty"` // ICD-10 code -> payer ref
}

type SentLine struct {
	Ref      string `json:"ref"`
	Code     string `json:"code"`
	Quantity int64  `json:"quantity"`
	Amount   int64  `json:"amount"`
}

func (s SentLine) matches(l connector.SendLine) bool {
	return s.Code == l.Code && s.Quantity == l.Quantity && s.Amount == l.Amount
}

// Journal stores Progress durably. It is called after each payer step.
type Journal func(context.Context, *Progress) error

// Send delivers a claim to the payer by the plan.
//
// The whole-claim path is one call; callers reconcile it with FindClaim
// before a resend. The incremental path records each step in p through
// journal, and on a resumed send first reads the payer's copy (FetchClaim) to
// adopt any line whose add reached the payer but whose reply was lost, and to
// notice a claim the payer already has as submitted.
func Send(ctx context.Context, conn connector.Connector, s *connector.Submission, plan Plan, p *Progress, journal Journal) (connector.Receipt, error) {
	if plan.WholeClaim() {
		return conn.SubmitWholeClaim(ctx, s)
	}
	if p.Lines == nil {
		p.Lines = map[string]SentLine{}
	}
	if p.Diagnoses == nil {
		p.Diagnoses = map[string]string{}
	}
	record := func() error {
		if err := journal(ctx, p); err != nil {
			// The payer has the step but we could not write it down: retry,
			// and the next run reconciles against the payer's copy.
			return connector.Errorf(connector.Connection, "recording progress: %v", err)
		}
		return nil
	}

	if p.PayerClaimNumber == "" {
		number, err := conn.OpenClaim(ctx, s)
		if err != nil {
			return connector.Receipt{}, err
		}
		p.PayerClaimNumber = number
		if err := record(); err != nil {
			return connector.Receipt{}, err
		}
	} else {
		done, err := reconcile(ctx, conn, s, p)
		if err != nil {
			return connector.Receipt{PayerClaimNumber: p.PayerClaimNumber}, err
		}
		if done != nil {
			return *done, nil
		}
		if err := record(); err != nil {
			return connector.Receipt{}, err
		}
	}
	number := p.PayerClaimNumber
	fail := func(err error) (connector.Receipt, error) {
		return connector.Receipt{PayerClaimNumber: number}, err
	}

	want := make(map[string]bool, len(s.Lines))
	for _, l := range s.Lines {
		want[l.Key] = true
	}
	for _, key := range sortedKeys(p.Lines) {
		if want[key] {
			continue
		}
		if err := conn.DeleteLine(ctx, s, number, p.Lines[key].Ref); err != nil && !isCode(err, connector.LineNotFound) {
			return fail(err)
		}
		delete(p.Lines, key)
		if err := record(); err != nil {
			return fail(err)
		}
	}
	for _, l := range s.Lines {
		sent, ok := p.Lines[l.Key]
		switch {
		case ok && sent.matches(l):
			continue
		case ok:
			ref, err := conn.UpdateLine(ctx, s, number, sent.Ref, l)
			if ref != "" {
				sent.Ref = ref
			}
			if err != nil {
				if ref != "" {
					p.Lines[l.Key] = SentLine{Ref: sent.Ref, Code: sent.Code, Quantity: sent.Quantity, Amount: sent.Amount}
					_ = record()
				}
				return fail(err)
			}
		default:
			ref, err := conn.AddLine(ctx, s, number, l)
			if err != nil {
				if ref != "" {
					// The payer holds the line but needs something first.
					p.Lines[l.Key] = SentLine{Ref: ref, Code: l.Code, Quantity: l.Quantity, Amount: l.Amount}
					_ = record()
				}
				return fail(err)
			}
			sent.Ref = ref
		}
		p.Lines[l.Key] = SentLine{Ref: sent.Ref, Code: l.Code, Quantity: l.Quantity, Amount: l.Amount}
		if err := record(); err != nil {
			return fail(err)
		}
	}

	wantDx := make(map[string]bool, len(s.Diagnoses))
	for _, d := range s.Diagnoses {
		wantDx[d.Key()] = true
	}
	for _, key := range sortedKeys(p.Diagnoses) {
		if wantDx[key] {
			continue
		}
		if err := conn.DeleteDiagnosis(ctx, s, number, p.Diagnoses[key]); err != nil {
			return fail(err)
		}
		delete(p.Diagnoses, key)
		if err := record(); err != nil {
			return fail(err)
		}
	}
	for _, d := range s.Diagnoses {
		if _, ok := p.Diagnoses[d.Key()]; ok {
			continue
		}
		ref, err := conn.AddDiagnosis(ctx, s, number, d)
		if err != nil {
			return fail(err)
		}
		p.Diagnoses[d.Key()] = ref
		if err := record(); err != nil {
			return fail(err)
		}
	}

	s.Sent = make(map[string]string, len(p.Lines))
	for key, sent := range p.Lines {
		s.Sent[key] = sent.Ref
	}
	r, err := conn.FinalizeClaim(ctx, s, number)
	if r.PayerClaimNumber == "" {
		r.PayerClaimNumber = number
	}
	return r, err
}

// reconcile compares a resumed send's progress with the payer's copy. It
// returns a receipt when the payer already has the claim as submitted.
func reconcile(ctx context.Context, conn connector.Connector, s *connector.Submission, p *Progress) (*connector.Receipt, error) {
	pc, err := conn.FetchClaim(ctx, p.PayerClaimNumber)
	if isCode(err, connector.NotSupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	switch pc.Status {
	case connector.PayerSubmitted, connector.PayerApproved, connector.PayerPaid, connector.PayerQueried:
		return &connector.Receipt{PayerClaimNumber: p.PayerClaimNumber, Acknowledged: true}, nil
	}
	// Payer lines we know about are spoken for; any other line with the code
	// of a line we have not recorded is the add whose reply was lost.
	key := func(ref string) string { return connector.RefKey(conn, ref) }
	taken := map[string]bool{}
	for _, sent := range p.Lines {
		taken[key(sent.Ref)] = true
	}
	for _, l := range s.Lines {
		if _, ok := p.Lines[l.Key]; ok {
			continue
		}
		for _, pl := range pc.Lines {
			if taken[key(pl.Ref)] || pl.Code != l.Code {
				continue
			}
			taken[key(pl.Ref)] = true
			p.Lines[l.Key] = SentLine{Ref: pl.Ref, Code: pl.Code, Quantity: pl.Quantity, Amount: pl.Amount}
			break
		}
	}
	// A recorded line the payer no longer has must be added again.
	have := map[string]bool{}
	for _, pl := range pc.Lines {
		have[key(pl.Ref)] = true
	}
	for k, sent := range p.Lines {
		if !have[key(sent.Ref)] {
			delete(p.Lines, k)
		}
	}
	return nil, nil
}

func isCode(err error, code connector.ErrorCode) bool {
	var ce *connector.Error
	return err != nil && errors.As(err, &ce) && ce.Code == code
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

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
