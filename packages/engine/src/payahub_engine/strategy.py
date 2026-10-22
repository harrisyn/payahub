"""Choose how a claim reaches a payer.

Ported from core-api-v2's validate_submission_mode: it never raises, it clamps
a configured line mode / creation timing pair to one the connector can
actually perform.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Literal

from payahub_connectors.sdk import ConnectorManifest, SubmissionConfig


@dataclass(frozen=True)
class SubmissionPlan:
    line_mode: Literal["realtime", "bulk"]
    timing: Literal["early", "deferred"]

    @property
    def whole_claim(self) -> bool:
        """Deferred timing means one call carries every line and diagnosis."""
        return self.timing == "deferred"


def resolve_submission_config(
    manifest: ConnectorManifest, org_overrides: Mapping[str, object] | None = None
) -> SubmissionConfig:
    """Connector defaults, with an organisation's overrides on top (unknown keys ignored)."""
    merged = manifest.submission.model_dump()
    for key, value in (org_overrides or {}).items():
        if key in merged:
            merged[key] = value
    return SubmissionConfig.model_validate(merged)


def choose_plan(manifest: ConnectorManifest, config: SubmissionConfig) -> SubmissionPlan:
    line_mode = config.line_submission_mode
    timing = config.claim_creation_timing

    invalid_combo = timing == "deferred" and line_mode == "realtime"
    unsupported = not manifest.supports_incremental_lines and (line_mode == "realtime" or timing == "early")
    if invalid_combo or unsupported:
        line_mode, timing = "bulk", "deferred"

    if timing == "deferred" and not manifest.supports_whole_claim:
        timing = "early"

    return SubmissionPlan(line_mode=line_mode, timing=timing)
