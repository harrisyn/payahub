"""The canonical claim: one shape for every intake channel and every payer.

Every channel (REST, document upload, batch file, web form) produces a Claim,
and every connector consumes one. Fields an AI step proposed carry Evidence,
so a reviewer or a payer audit can see where each value came from.
"""

from .claim import (
    Claim,
    ClaimCreate,
    Code,
    CodeMapping,
    Diagnosis,
    DiagnosisRole,
    Encounter,
    EncounterType,
    Evidence,
    FieldOrigin,
    LineCategory,
    Member,
    Provenance,
    Provider,
    ServiceLine,
    Source,
)
from .states import ClaimState, InvalidTransition, can_transition, transition

__all__ = [
    "Claim",
    "ClaimCreate",
    "ClaimState",
    "Code",
    "CodeMapping",
    "Diagnosis",
    "DiagnosisRole",
    "Encounter",
    "EncounterType",
    "Evidence",
    "FieldOrigin",
    "InvalidTransition",
    "LineCategory",
    "Member",
    "Provenance",
    "Provider",
    "ServiceLine",
    "Source",
    "can_transition",
    "transition",
]
