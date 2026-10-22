"""A deterministic in-memory payer for trials, docs and tests.

Every organisation gets it in sandbox mode, so a first claim can go end to
end before any real payer credentials exist. Behaviour is driven by the
member number so integrators can exercise failure paths on purpose:

- starts with "NOTFOUND" -> member_not_found
- starts with "OTP"      -> verification needs an OTP ("123456")
- starts with "REJECT"   -> the payer refuses the claim on submission
- anything else          -> eligible, claim acknowledged
"""

from __future__ import annotations

import hashlib

from payahub_claim_model import Claim, Diagnosis, Member, ServiceLine

from ..sdk import (
    Connector,
    ConnectorError,
    ConnectorManifest,
    ErrorCode,
    MemberVerification,
    PayerClaimStatus,
    SubmissionReceipt,
    Verb,
)

SANDBOX_OTP = "123456"


class SandboxConnector(Connector):
    manifest = ConnectorManifest(
        key="sandbox",
        name="Payahub Sandbox",
        country="ZZ",
        verbs=frozenset(Verb),
        notes="Simulated payer. Never reaches a real insurer.",
    )

    def __init__(self, credentials=None):
        super().__init__(credentials or {})
        self._claims: dict[str, dict] = {}

    @staticmethod
    def _claim_number(seed: str) -> str:
        return "SBX-" + hashlib.sha256(seed.encode()).hexdigest()[:10].upper()

    async def verify_member(self, member_number: str, *, otp: str | None = None) -> MemberVerification:
        upper = member_number.upper()
        if upper.startswith("NOTFOUND"):
            raise ConnectorError(ErrorCode.MEMBER_NOT_FOUND, f"No member {member_number}")
        if upper.startswith("OTP") and otp != SANDBOX_OTP:
            return MemberVerification(
                eligible=False, member=Member(member_number=member_number), otp_required=True
            )
        return MemberVerification(
            eligible=True,
            member=Member(member_number=member_number, first_name="Ama", last_name="Mensah"),
            plan_name="Sandbox Gold",
        )

    def _refuse_if_needed(self, claim: Claim) -> None:
        if claim.member.member_number.upper().startswith("REJECT"):
            raise ConnectorError(
                ErrorCode.PAYER_REFUSED,
                "Sandbox payer refused the claim",
                payer_message="Member benefit exhausted for this period",
            )

    async def submit_whole_claim(self, claim: Claim) -> SubmissionReceipt:
        self._refuse_if_needed(claim)
        number = self._claim_number(claim.id)
        refs = {line.id: f"{number}-{i + 1}" for i, line in enumerate(claim.lines)}
        self._claims[number] = {"status": "acknowledged", "lines": refs}
        return SubmissionReceipt(payer_claim_number=number, acknowledged=True, line_references=refs)

    async def open_claim(self, claim: Claim) -> str:
        number = self._claim_number(claim.id)
        self._claims.setdefault(number, {"status": "open", "lines": {}, "diagnoses": []})
        return number

    def _open(self, number: str) -> dict:
        state = self._claims.get(number)
        if state is None:
            raise ConnectorError(ErrorCode.CLAIM_CLOSED, f"No open claim {number}")
        return state

    async def add_line(self, payer_claim_number: str, line: ServiceLine) -> str:
        state = self._open(payer_claim_number)
        if line.id in state["lines"]:
            raise ConnectorError(ErrorCode.LINE_ALREADY_ADDED, f"Line {line.id} already on claim")
        ref = f"{payer_claim_number}-{len(state['lines']) + 1}"
        state["lines"][line.id] = ref
        return ref

    async def update_line(self, payer_claim_number: str, line_reference: str, line: ServiceLine) -> None:
        if line_reference not in self._open(payer_claim_number)["lines"].values():
            raise ConnectorError(ErrorCode.LINE_NOT_FOUND, f"No line {line_reference}")

    async def delete_line(self, payer_claim_number: str, line_reference: str) -> None:
        lines = self._open(payer_claim_number)["lines"]
        for line_id, ref in list(lines.items()):
            if ref == line_reference:
                del lines[line_id]
                return
        raise ConnectorError(ErrorCode.LINE_NOT_FOUND, f"No line {line_reference}")

    async def add_diagnosis(self, payer_claim_number: str, diagnosis: Diagnosis) -> None:
        self._open(payer_claim_number).setdefault("diagnoses", []).append(diagnosis.code.value)

    async def finalize_claim(self, payer_claim_number: str, claim: Claim) -> SubmissionReceipt:
        self._refuse_if_needed(claim)
        state = self._open(payer_claim_number)
        state["status"] = "acknowledged"
        return SubmissionReceipt(
            payer_claim_number=payer_claim_number, acknowledged=True, line_references=dict(state["lines"])
        )

    async def withdraw_claim(self, payer_claim_number: str) -> None:
        self._open(payer_claim_number)["status"] = "withdrawn"

    async def fetch_claim(self, payer_claim_number: str) -> PayerClaimStatus:
        return PayerClaimStatus(
            payer_claim_number=payer_claim_number, status=self._open(payer_claim_number)["status"]
        )
