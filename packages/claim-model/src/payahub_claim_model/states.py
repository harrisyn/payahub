"""Claim lifecycle shared by every channel and payer.

Draft -> Mapped -> Validated -> (Needs review) -> Ready -> Submitted
-> Acknowledged -> Approved | Rejected -> Paid. A rejected claim can be
corrected and go back through validation; a claim can be withdrawn any time
before the payer has decided on it.
"""

from __future__ import annotations

from enum import StrEnum


class ClaimState(StrEnum):
    DRAFT = "draft"
    MAPPED = "mapped"
    VALIDATED = "validated"
    NEEDS_REVIEW = "needs_review"
    READY = "ready"
    SUBMITTED = "submitted"
    ACKNOWLEDGED = "acknowledged"
    APPROVED = "approved"
    REJECTED = "rejected"
    PAID = "paid"
    WITHDRAWN = "withdrawn"


_WITHDRAWABLE = {
    ClaimState.DRAFT,
    ClaimState.MAPPED,
    ClaimState.VALIDATED,
    ClaimState.NEEDS_REVIEW,
    ClaimState.READY,
    ClaimState.SUBMITTED,
    ClaimState.ACKNOWLEDGED,
}

TRANSITIONS: dict[ClaimState, frozenset[ClaimState]] = {
    ClaimState.DRAFT: frozenset({ClaimState.MAPPED, ClaimState.NEEDS_REVIEW}),
    ClaimState.MAPPED: frozenset({ClaimState.VALIDATED, ClaimState.NEEDS_REVIEW, ClaimState.DRAFT}),
    ClaimState.VALIDATED: frozenset({ClaimState.READY, ClaimState.NEEDS_REVIEW, ClaimState.DRAFT}),
    ClaimState.NEEDS_REVIEW: frozenset({ClaimState.MAPPED, ClaimState.VALIDATED, ClaimState.READY}),
    ClaimState.READY: frozenset({ClaimState.SUBMITTED, ClaimState.DRAFT}),
    # A submission the payer refuses outright (bad member, closed claim) comes
    # back as REJECTED without ever being acknowledged.
    ClaimState.SUBMITTED: frozenset({ClaimState.ACKNOWLEDGED, ClaimState.REJECTED}),
    ClaimState.ACKNOWLEDGED: frozenset({ClaimState.APPROVED, ClaimState.REJECTED}),
    ClaimState.APPROVED: frozenset({ClaimState.PAID}),
    ClaimState.REJECTED: frozenset({ClaimState.DRAFT}),
    ClaimState.PAID: frozenset(),
    ClaimState.WITHDRAWN: frozenset(),
}

# Claims are billed once the payer acknowledges them (see docs/adr/0002).
BILLABLE_STATES = frozenset({ClaimState.ACKNOWLEDGED, ClaimState.APPROVED, ClaimState.PAID})


class InvalidTransition(ValueError):
    def __init__(self, current: ClaimState, target: ClaimState):
        super().__init__(f"Cannot move a claim from {current.value} to {target.value}")
        self.current = current
        self.target = target


def can_transition(current: ClaimState, target: ClaimState) -> bool:
    if target == ClaimState.WITHDRAWN:
        return current in _WITHDRAWABLE
    return target in TRANSITIONS[current]


def transition(current: ClaimState, target: ClaimState) -> ClaimState:
    if not can_transition(current, target):
        raise InvalidTransition(current, target)
    return target
