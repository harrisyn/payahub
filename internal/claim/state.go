package claim

import "fmt"

// State is the claim lifecycle shared by every channel and payer:
// draft -> mapped -> validated -> (needs_review) -> ready -> submitted
// -> acknowledged -> approved | rejected -> paid. A rejected claim can be
// corrected and go back through validation; a claim can be withdrawn until
// the payer has decided on it.
type State string

const (
	Draft        State = "draft"
	Mapped       State = "mapped"
	Validated    State = "validated"
	NeedsReview  State = "needs_review"
	Ready        State = "ready"
	Submitted    State = "submitted"
	Acknowledged State = "acknowledged"
	Approved     State = "approved"
	Rejected     State = "rejected"
	Paid         State = "paid"
	Withdrawn    State = "withdrawn"
)

var transitions = map[State][]State{
	Draft:       {Mapped, NeedsReview},
	Mapped:      {Validated, NeedsReview, Draft},
	Validated:   {Ready, NeedsReview, Draft},
	NeedsReview: {Mapped, Validated, Ready},
	Ready:       {Submitted, Draft},
	// A submission the payer refuses outright comes back rejected without
	// ever being acknowledged. One that never reached the payer for a reason
	// a person must fix (credentials, an OTP) goes back to ready.
	Submitted:    {Acknowledged, Rejected, Ready},
	Acknowledged: {Approved, Rejected},
	Approved:     {Paid},
	Rejected:     {Draft},
}

var withdrawable = map[State]bool{
	Draft: true, Mapped: true, Validated: true, NeedsReview: true, Ready: true, Submitted: true, Acknowledged: true,
}

// Billable states: a claim is billed once the payer acknowledged it (ADR 0002).
func (s State) Billable() bool { return s == Acknowledged || s == Approved || s == Paid }

func CanTransition(from, to State) bool {
	if to == Withdrawn {
		return withdrawable[from]
	}
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

type InvalidTransitionError struct{ From, To State }

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("cannot move a claim from %s to %s", e.From, e.To)
}

func Transition(from, to State) (State, error) {
	if !CanTransition(from, to) {
		return from, &InvalidTransitionError{From: from, To: to}
	}
	return to, nil
}

// AdvanceToReady walks a validated claim forward through the lifecycle to Ready.
func AdvanceToReady(s State) State {
	for _, step := range []State{Mapped, Validated, Ready} {
		if CanTransition(s, step) {
			s = step
		}
	}
	return s
}
