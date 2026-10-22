from __future__ import annotations

from dataclasses import dataclass, field
from functools import lru_cache
from typing import Annotated

from fastapi import Depends, Header
from pydantic_settings import BaseSettings, SettingsConfigDict

from payahub_claim_model import Claim
from payahub_connectors.sandbox import SandboxConnector

from .errors import ApiError


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="PAYAHUB_")

    sandbox_api_key: str = "sk_sandbox_dev"


@lru_cache
def get_settings() -> Settings:
    return Settings()


@dataclass
class Organisation:
    id: str
    environment: str


@dataclass
class Store:
    """In-memory stand-in for Postgres; replaced in phase 0."""

    claims: dict[str, Claim] = field(default_factory=dict)
    idempotency: dict[tuple[str, str], str] = field(default_factory=dict)
    sandbox: SandboxConnector = field(default_factory=SandboxConnector)


_store = Store()


def get_store() -> Store:
    return _store


def get_organisation(
    settings: Annotated[Settings, Depends(get_settings)],
    authorization: Annotated[str | None, Header()] = None,
) -> Organisation:
    token = (authorization or "").removeprefix("Bearer ").strip()
    if not token:
        raise ApiError(401, "unauthenticated", "Send your API key as 'Authorization: Bearer <key>'.")
    if token != settings.sandbox_api_key:
        raise ApiError(401, "invalid_api_key", "That API key is not recognised.")
    return Organisation(id="org_sandbox", environment="sandbox")
