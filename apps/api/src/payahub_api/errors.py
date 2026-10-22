from __future__ import annotations

from typing import Any


class ApiError(Exception):
    """One error shape for every endpoint: {"error": {"code", "message", "details"}}."""

    def __init__(self, status_code: int, code: str, message: str, details: Any | None = None):
        super().__init__(message)
        self.status_code = status_code
        self.code = code
        self.message = message
        self.details = details

    def body(self) -> dict[str, Any]:
        body: dict[str, Any] = {"code": self.code, "message": self.message}
        if self.details is not None:
            body["details"] = self.details
        return body
