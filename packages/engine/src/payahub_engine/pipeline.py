"""Validate and submit a claim through a connector.

The payer payload is always assembled deterministically by the connector from
validated fields; nothing here asks a model for output.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field

from payahub_claim_model import Claim, ClaimState, can_transition, transition
from payahub_connectors.sdk import Connector, ConnectorError, ErrorCode, SubmissionReceipt

from .rules import Issue, Severity, check_claim
from .strategy import SubmissionPlan, choose_plan, resolve_submission_config


@dataclass
class ValidationResult:
    issues: list[Issue]
    plan: SubmissionPlan

    @property
    def state(self) -> ClaimState:
        if any(i.severity == Severity.ERROR for i in self.issues):
            return ClaimState.DRAFT
        if any(i.severity == Severity.REVIEW for i in self.issues):
            return ClaimState.NEEDS_REVIEW
        return ClaimState.READY


@dataclass
class SubmissionOutcome:
    claim: Claim
    receipt: SubmissionReceipt | None = None
    error: ConnectorError | None = None
    issues: list[Issue] = field(default_factory=list)


def validate(
    claim: Claim, connector: Connector, org_overrides: Mapping[str, object] | None = None
) -> ValidationResult:
    config = resolve_submission_config(connector.manifest, org_overrides)
    return ValidationResult(issues=check_claim(claim, config), plan=choose_plan(connector.manifest, config))


async def submit(
    claim: Claim, connector: Connector, org_overrides: Mapping[str, object] | None = None
) -> SubmissionOutcome:
    """Submit a READY claim. Returns the updated claim; never leaves it half-moved."""
    result = validate(claim, connector, org_overrides)
    if result.state != ClaimState.READY:
        return SubmissionOutcome(claim=claim, issues=result.issues)

    ready = claim.model_copy(update={"state": _advance_to_ready(claim.state)})
    submitted = ready.model_copy(update={"state": transition(ready.state, ClaimState.SUBMITTED)})
    try:
        receipt = await _send(submitted, connector, result.plan)
    except ConnectorError as exc:
        if exc.code == ErrorCode.PAYER_REFUSED:
            rejected = submitted.model_copy(
                update={"state": transition(submitted.state, ClaimState.REJECTED)}
            )
            return SubmissionOutcome(claim=rejected, error=exc, issues=result.issues)
        # Transport or unknown failure: the claim stays READY so it can be retried.
        return SubmissionOutcome(claim=ready, error=exc, issues=result.issues)

    final = submitted.model_copy(update={"payer_claim_number": receipt.payer_claim_number})
    if receipt.acknowledged:
        final = final.model_copy(update={"state": transition(final.state, ClaimState.ACKNOWLEDGED)})
    return SubmissionOutcome(claim=final, receipt=receipt, issues=result.issues)


def _advance_to_ready(state: ClaimState) -> ClaimState:
    """Walk a validated claim forward through the lifecycle to READY."""
    for step in (ClaimState.MAPPED, ClaimState.VALIDATED, ClaimState.READY):
        if can_transition(state, step):
            state = step
    return state


async def _send(claim: Claim, connector: Connector, plan: SubmissionPlan) -> SubmissionReceipt:
    if plan.whole_claim:
        return await connector.submit_whole_claim(claim)

    number = claim.payer_claim_number or await connector.open_claim(claim)
    for line in claim.lines:
        await connector.add_line(number, line)
    for diagnosis in claim.diagnoses:
        await connector.add_diagnosis(number, diagnosis)
    return await connector.finalize_claim(number, claim)
