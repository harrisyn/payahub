"""Connector SDK and the registry of payer connectors.

To be ported from core-api-v2 in phases 0 and 1: claimiq (manifest here),
xceli, medserve, smartclaims, rx-insurance, and an email/portal pack connector
replacing the ClaimBatch email flow.
"""

from __future__ import annotations

from .claimiq import ClaimIQConnector
from .sandbox import SandboxConnector
from .sdk import Connector, ConnectorManifest

_REGISTRY: dict[str, type[Connector]] = {
    SandboxConnector.manifest.key: SandboxConnector,
    ClaimIQConnector.manifest.key: ClaimIQConnector,
}


def get_connector_class(key: str) -> type[Connector]:
    try:
        return _REGISTRY[key]
    except KeyError:
        raise KeyError(f"Unknown connector '{key}'") from None


def list_manifests() -> list[ConnectorManifest]:
    return [cls.manifest for cls in _REGISTRY.values()]


__all__ = ["get_connector_class", "list_manifests"]
