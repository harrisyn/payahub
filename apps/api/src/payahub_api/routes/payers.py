from __future__ import annotations

from fastapi import APIRouter
from pydantic import BaseModel

from payahub_connectors import list_manifests

router = APIRouter(tags=["payers"])


class CredentialFieldOut(BaseModel):
    name: str
    label: str
    secret: bool
    required: bool


class PayerOut(BaseModel):
    id: str
    name: str
    country: str
    channel: str
    capabilities: list[str]
    member_otp_required: bool
    credentials: list[CredentialFieldOut]


@router.get("/payers", response_model=list[PayerOut])
async def list_payers() -> list[PayerOut]:
    """Supported payers, how each is reached, and the credentials it needs."""
    return [
        PayerOut(
            id=m.key,
            name=m.name,
            country=m.country,
            channel=m.channel.value,
            capabilities=sorted(v.value for v in m.verbs),
            member_otp_required=m.member_otp_required,
            credentials=[CredentialFieldOut(**f.model_dump()) for f in m.credentials],
        )
        for m in list_manifests()
    ]
