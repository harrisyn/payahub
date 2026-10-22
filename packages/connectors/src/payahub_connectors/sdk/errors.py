"""One error taxonomy for every payer.

core-api-v2 recognised payer failures by matching free-text messages in each
adapter ("claim expired", "authorization required", "already added"). Here a
connector translates its payer's message into one of these codes once, at the
edge, and everything above it (engine, API, dashboard) works with the code.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Any


class ErrorCode(StrEnum):
    MEMBER_NOT_FOUND = "member_not_found"
    MEMBER_INACTIVE = "member_inactive"
    OTP_REQUIRED = "otp_required"
    AUTHORIZATION_REQUIRED = "authorization_required"
    CLAIM_EXPIRED = "claim_expired"
    CLAIM_CLOSED = "claim_closed"
    LINE_ALREADY_ADDED = "line_already_added"
    LINE_NOT_FOUND = "line_not_found"
    CODE_NOT_RECOGNISED = "code_not_recognised"
    INVALID_CREDENTIALS = "invalid_credentials"
    NOT_SUPPORTED = "not_supported"
    VALIDATION = "validation"
    TIMEOUT = "timeout"
    CONNECTION = "connection"
    PAYER_REFUSED = "payer_refused"
    UNKNOWN = "unknown"


RETRYABLE = frozenset({ErrorCode.TIMEOUT, ErrorCode.CONNECTION})


class ConnectorError(Exception):
    def __init__(
        self,
        code: ErrorCode,
        message: str,
        *,
        user_message: str | None = None,
        payer_message: str | None = None,
        raw: Any = None,
    ):
        super().__init__(message)
        self.code = code
        self.message = message
        # Safe to show to a biller; never carries internal identifiers.
        self.user_message = user_message or message
        # The payer's own words, kept for the AI classifier and for audit.
        self.payer_message = payer_message
        self.raw = raw

    @property
    def retryable(self) -> bool:
        return self.code in RETRYABLE
