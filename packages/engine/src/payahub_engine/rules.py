"""Deterministic edit rules run before anything is sent.

These are the payer-neutral checks. Payer-specific rules arrive later as data
(EditRule rows per connector), not as code here.
"""

from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel

from payahub_claim_model import Claim, DiagnosisRole, FieldOrigin
from payahub_connectors.sdk import SubmissionConfig


class Severity(StrEnum):
    ERROR = "error"
    WARNING = "warning"
    REVIEW = "review"


class Issue(BaseModel):
    code: str
    severity: Severity
    field: str
    message: str


# One global threshold is a bug (claims-integrator plan, guardrail 2); these
# are starting values per use, tuned from outcomes.
CONFIDENCE_THRESHOLDS = {
    FieldOrigin.AI_EXTRACTED: 0.90,
    FieldOrigin.AI_MAPPED: 0.95,
}


def check_claim(claim: Claim, config: SubmissionConfig) -> list[Issue]:
    issues: list[Issue] = []

    if not claim.lines:
        issues.append(
            Issue(
                code="no_lines",
                severity=Severity.ERROR,
                field="lines",
                message="A claim needs at least one line.",
            )
        )
    if not any(d.role == DiagnosisRole.PRIMARY for d in claim.diagnoses):
        issues.append(
            Issue(
                code="no_primary_diagnosis",
                severity=Severity.ERROR,
                field="diagnoses",
                message="Add a primary diagnosis.",
            )
        )

    enc = claim.encounter
    if enc.end_date and enc.end_date < enc.start_date:
        issues.append(
            Issue(
                code="dates_reversed",
                severity=Severity.ERROR,
                field="encounter.end_date",
                message="The encounter ends before it starts.",
            )
        )

    for i, line in enumerate(claim.lines):
        mapped = line.mapping and line.mapping.target
        if not mapped and config.unmapped_line_policy == "block":
            issues.append(
                Issue(
                    code="line_unmapped",
                    severity=Severity.ERROR,
                    field=f"lines[{i}].mapping",
                    message=f"'{line.description}' has no code for this payer.",
                )
            )
        issues.extend(
            _evidence_issues(line.mapping.evidence if line.mapping else None, f"lines[{i}].mapping")
        )

    for i, diagnosis in enumerate(claim.diagnoses):
        mapped = diagnosis.mapping and diagnosis.mapping.target
        if not mapped and config.unmapped_diagnosis_policy == "block":
            issues.append(
                Issue(
                    code="diagnosis_unmapped",
                    severity=Severity.ERROR,
                    field=f"diagnoses[{i}].mapping",
                    message=f"{diagnosis.code.value} has no code for this payer.",
                )
            )
        elif not mapped and config.diagnosis_code_source == "payer_code":
            issues.append(
                Issue(
                    code="diagnosis_may_be_rejected",
                    severity=Severity.WARNING,
                    field=f"diagnoses[{i}]",
                    message=(
                        f"{diagnosis.code.value} is sent as plain ICD-10; this payer expects its own codes."
                    ),
                )
            )
        issues.extend(
            _evidence_issues(
                diagnosis.mapping.evidence if diagnosis.mapping else None, f"diagnoses[{i}].mapping"
            )
        )

    return issues


def _evidence_issues(evidence, field: str) -> list[Issue]:
    if evidence is None or evidence.origin not in CONFIDENCE_THRESHOLDS:
        return []
    threshold = CONFIDENCE_THRESHOLDS[evidence.origin]
    if evidence.confirmed_by or (evidence.confidence is not None and evidence.confidence >= threshold):
        return []
    return [
        Issue(
            code="needs_review",
            severity=Severity.REVIEW,
            field=field,
            message=f"AI-proposed value below the {threshold:.0%} threshold; a person must confirm it.",
        )
    ]
