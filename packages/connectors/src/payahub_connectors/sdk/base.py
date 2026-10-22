"""The typed connector interface.

core-api-v2 adapters expose `execute_method(name, Dict[str, Any])`. Here each
verb is a typed method; a connector implements the ones its manifest lists
and inherits a NOT_SUPPORTED error for the rest.
"""

from __future__ import annotations

from abc import ABC
from collections.abc import Mapping
from datetime import date
from typing import Any, ClassVar

from pydantic import BaseModel, Field

from payahub_claim_model import Claim, Diagnosis, Member, ServiceLine

from .errors import ConnectorError, ErrorCode
from .manifest import ConnectorManifest, Verb


class MemberVerification(BaseModel):
    eligible: bool
    member: Member
    plan_name: str | None = None
    coverage_end: date | None = None
    otp_required: bool = False


class SubmissionReceipt(BaseModel):
    payer_claim_number: str
    acknowledged: bool = False
    line_references: dict[str, str] = Field(
        default_factory=dict, description="Our ServiceLine.id -> the payer's line reference."
    )
    warnings: list[str] = Field(default_factory=list)
    raw: Any = None


class PayerClaimStatus(BaseModel):
    payer_claim_number: str
    status: str
    approved_amount: str | None = None
    raw: Any = None


class Connector(ABC):
    manifest: ClassVar[ConnectorManifest]

    def __init__(self, credentials: Mapping[str, str]):
        missing = [f.name for f in self.manifest.credentials if f.required and not credentials.get(f.name)]
        if missing:
            raise ConnectorError(
                ErrorCode.INVALID_CREDENTIALS,
                f"{self.manifest.name} is missing credentials: {', '.join(missing)}",
            )
        self.credentials = dict(credentials)

    def _unsupported(self, verb: Verb) -> ConnectorError:
        return ConnectorError(ErrorCode.NOT_SUPPORTED, f"{self.manifest.name} does not support {verb.value}")

    async def test_credentials(self) -> None:
        """Raise ConnectorError(INVALID_CREDENTIALS) if the payer rejects these credentials."""
        return

    async def verify_member(self, member_number: str, *, otp: str | None = None) -> MemberVerification:
        raise self._unsupported(Verb.VERIFY_MEMBER)

    async def submit_whole_claim(self, claim: Claim) -> SubmissionReceipt:
        raise self._unsupported(Verb.SUBMIT_WHOLE_CLAIM)

    async def open_claim(self, claim: Claim) -> str:
        raise self._unsupported(Verb.OPEN_CLAIM)

    async def add_line(self, payer_claim_number: str, line: ServiceLine) -> str:
        raise self._unsupported(Verb.ADD_LINE)

    async def update_line(self, payer_claim_number: str, line_reference: str, line: ServiceLine) -> None:
        raise self._unsupported(Verb.UPDATE_LINE)

    async def delete_line(self, payer_claim_number: str, line_reference: str) -> None:
        raise self._unsupported(Verb.DELETE_LINE)

    async def add_diagnosis(self, payer_claim_number: str, diagnosis: Diagnosis) -> None:
        raise self._unsupported(Verb.ADD_DIAGNOSIS)

    async def finalize_claim(self, payer_claim_number: str, claim: Claim) -> SubmissionReceipt:
        raise self._unsupported(Verb.FINALIZE_CLAIM)

    async def withdraw_claim(self, payer_claim_number: str) -> None:
        raise self._unsupported(Verb.WITHDRAW_CLAIM)

    async def fetch_claim(self, payer_claim_number: str) -> PayerClaimStatus:
        raise self._unsupported(Verb.FETCH_CLAIM)
