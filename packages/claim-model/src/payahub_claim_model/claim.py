from __future__ import annotations

import uuid
from datetime import UTC, date, datetime
from decimal import Decimal
from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field, computed_field

from .states import ClaimState


class Source(StrEnum):
    """The channel a claim arrived through."""

    API = "api"
    DOCUMENT = "document"
    BATCH = "batch"
    FORM = "form"


class FieldOrigin(StrEnum):
    """Who set a value. Anything AI-set must be confirmed or above threshold before submission."""

    SUBMITTED = "submitted"
    AI_EXTRACTED = "ai_extracted"
    AI_MAPPED = "ai_mapped"
    RULE_DERIVED = "rule_derived"
    HUMAN_CONFIRMED = "human_confirmed"


class Evidence(BaseModel):
    """Why a value is what it is. Required on every AI-proposed field."""

    origin: FieldOrigin
    confidence: float | None = Field(default=None, ge=0, le=1)
    model_version: str | None = None
    prompt_version: str | None = None
    prompt_hash: str | None = None
    # A plain-words statement of what the value means, e.g. "office visit,
    # established patient, no modifier" — the plan's guard against confident
    # but wrong mappings.
    assertion: str | None = None
    confirmed_by: str | None = None


class Code(BaseModel):
    """A code in a named system: icd-10, cpt, nhia-gdrg, or a payer's own catalogue ("payer:<id>")."""

    system: str
    value: str
    display: str | None = None


class CodeMapping(BaseModel):
    source: Code
    target: Code | None = None
    evidence: Evidence | None = None


class Member(BaseModel):
    member_number: str
    policy_number: str | None = None
    first_name: str | None = None
    last_name: str | None = None
    birth_date: date | None = None
    gender: str | None = None
    phone: str | None = None


class Provider(BaseModel):
    name: str
    facility_code: str | None = None
    practitioner_id: str | None = None


class EncounterType(StrEnum):
    OUTPATIENT = "outpatient"
    INPATIENT = "inpatient"


class Encounter(BaseModel):
    type: EncounterType = EncounterType.OUTPATIENT
    reference: str | None = Field(default=None, description="The provider's own visit reference.")
    start_date: date
    end_date: date | None = None
    admission_date: date | None = None
    discharge_date: date | None = None


class LineCategory(StrEnum):
    SERVICE = "service"
    PROCEDURE = "procedure"
    DRUG = "drug"
    CONSUMABLE = "consumable"


class ServiceLine(BaseModel):
    id: str = Field(default_factory=lambda: uuid.uuid4().hex)
    description: str
    category: LineCategory = LineCategory.SERVICE
    code: Code | None = None
    mapping: CodeMapping | None = None
    quantity: int = Field(default=1, ge=1)
    unit_price: Decimal = Field(ge=0)
    service_date: date | None = None

    @computed_field  # type: ignore[prop-decorator]
    @property
    def total(self) -> Decimal:
        return self.unit_price * self.quantity


class DiagnosisRole(StrEnum):
    PRIMARY = "primary"
    SECONDARY = "secondary"


class Diagnosis(BaseModel):
    code: Code
    role: DiagnosisRole = DiagnosisRole.SECONDARY
    mapping: CodeMapping | None = None


class Provenance(BaseModel):
    source: Source = Source.API
    received_at: datetime = Field(default_factory=lambda: datetime.now(UTC))
    payload_sha256: str | None = None
    artifact_uri: str | None = Field(
        default=None, description="Original document or file, when there is one."
    )


class ClaimCreate(BaseModel):
    """What an integrator sends. Everything else on a Claim is set by Payahub."""

    model_config = ConfigDict(extra="forbid")

    payer_id: str
    currency: str = "GHS"
    member: Member
    provider: Provider
    encounter: Encounter
    lines: list[ServiceLine] = Field(default_factory=list)
    diagnoses: list[Diagnosis] = Field(default_factory=list)
    authorization_code: str | None = None


class Claim(ClaimCreate):
    model_config = ConfigDict(extra="forbid")

    id: str = Field(default_factory=lambda: f"clm_{uuid.uuid4().hex}")
    organization_id: str
    state: ClaimState = ClaimState.DRAFT
    provenance: Provenance = Field(default_factory=Provenance)
    payer_claim_number: str | None = None

    @computed_field  # type: ignore[prop-decorator]
    @property
    def total(self) -> Decimal:
        return sum((line.total for line in self.lines), Decimal(0))
