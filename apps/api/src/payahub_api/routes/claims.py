from __future__ import annotations

import hashlib
from typing import Annotated

from fastapi import APIRouter, Depends, Header
from pydantic import BaseModel

from payahub_claim_model import Claim, ClaimCreate, InvalidTransition, Provenance, Source
from payahub_connectors.sdk import Connector
from payahub_engine import Issue, submit, validate

from ..deps import Organisation, Store, get_organisation, get_store
from ..errors import ApiError

router = APIRouter(tags=["claims"])

OrgDep = Annotated[Organisation, Depends(get_organisation)]
StoreDep = Annotated[Store, Depends(get_store)]


class ValidationOut(BaseModel):
    state: str
    issues: list[Issue]
    submission_plan: dict[str, str]


class SubmissionOut(BaseModel):
    claim: Claim
    issues: list[Issue]
    error: dict[str, str] | None = None


def _connector_for(claim: Claim, org: Organisation, store: Store) -> Connector:
    if org.environment == "sandbox":
        # Sandbox keys always reach the simulated payer, whatever payer_id says.
        return store.sandbox
    raise ApiError(409, "connector_not_configured", f"No credentials saved for payer '{claim.payer_id}'.")


def _get_claim(claim_id: str, org: Organisation, store: Store) -> Claim:
    claim = store.claims.get(claim_id)
    if claim is None or claim.organization_id != org.id:
        raise ApiError(404, "claim_not_found", f"No claim {claim_id}.")
    return claim


@router.post("/claims", response_model=Claim, status_code=201)
async def create_claim(
    body: ClaimCreate,
    org: OrgDep,
    store: StoreDep,
    idempotency_key: Annotated[str | None, Header()] = None,
) -> Claim:
    """Create a whole claim (lines and diagnoses included). It is validated, not yet sent."""
    if idempotency_key:
        existing = store.idempotency.get((org.id, idempotency_key))
        if existing:
            return store.claims[existing]

    raw = body.model_dump_json().encode()
    claim = Claim(
        **body.model_dump(),
        organization_id=org.id,
        provenance=Provenance(source=Source.API, payload_sha256=hashlib.sha256(raw).hexdigest()),
    )
    store.claims[claim.id] = claim
    if idempotency_key:
        store.idempotency[(org.id, idempotency_key)] = claim.id
    return claim


@router.post("/claims/validate", response_model=ValidationOut)
async def validate_claim(body: ClaimCreate, org: OrgDep, store: StoreDep) -> ValidationOut:
    """Dry run: edit rules and submission plan. Nothing is stored or sent."""
    claim = Claim(**body.model_dump(), organization_id=org.id)
    result = validate(claim, _connector_for(claim, org, store))
    return ValidationOut(
        state=result.state.value,
        issues=result.issues,
        submission_plan={"line_mode": result.plan.line_mode, "timing": result.plan.timing},
    )


@router.get("/claims/{claim_id}", response_model=Claim)
async def get_claim(claim_id: str, org: OrgDep, store: StoreDep) -> Claim:
    return _get_claim(claim_id, org, store)


@router.post("/claims/{claim_id}/submit", response_model=SubmissionOut)
async def submit_claim(claim_id: str, org: OrgDep, store: StoreDep) -> SubmissionOut:
    claim = _get_claim(claim_id, org, store)
    try:
        outcome = await submit(claim, _connector_for(claim, org, store))
    except InvalidTransition as exc:
        raise ApiError(409, "invalid_state", str(exc)) from None

    store.claims[claim_id] = outcome.claim
    error = None
    if outcome.error:
        error = {
            "code": outcome.error.code.value,
            "message": outcome.error.user_message,
            "retryable": str(outcome.error.retryable).lower(),
        }
    return SubmissionOut(claim=outcome.claim, issues=outcome.issues, error=error)
