"""What a connector can do and how its payer wants claims built — as data.

This promotes core-api-v2's capability checks (supports_incremental_items,
supports_whole_claim_submission) and Plugin.default_config submission keys
into one typed manifest per connector, so the engine chooses a submission
strategy from data and organisations override it from the dashboard.
"""

from __future__ import annotations

from enum import StrEnum
from typing import Literal

from pydantic import BaseModel, Field


class Verb(StrEnum):
    VERIFY_MEMBER = "verify_member"
    SEND_OTP = "send_otp"
    REQUEST_AUTHORIZATION = "request_authorization"
    SUBMIT_WHOLE_CLAIM = "submit_whole_claim"
    OPEN_CLAIM = "open_claim"
    ADD_LINE = "add_line"
    UPDATE_LINE = "update_line"
    DELETE_LINE = "delete_line"
    ADD_DIAGNOSIS = "add_diagnosis"
    FINALIZE_CLAIM = "finalize_claim"
    WITHDRAW_CLAIM = "withdraw_claim"
    FETCH_CLAIM = "fetch_claim"
    LIST_CATALOG = "list_catalog"
    CONVERT_ENCOUNTER_TYPE = "convert_encounter_type"


class Channel(StrEnum):
    API = "api"
    PORTAL = "portal"
    EMAIL = "email"


class CredentialField(BaseModel):
    name: str
    label: str
    secret: bool = False
    required: bool = True


class SubmissionConfig(BaseModel):
    """Per-payer claim-building rules, overridable per organisation.

    Same keys and meaning as core-api-v2's DEFAULT_SUBMISSION_CONFIG.
    """

    item_grouping_outpatient: Literal["grouped", "individual"] = "grouped"
    item_grouping_inpatient: Literal["grouped", "individual"] = "individual"
    unmapped_line_policy: Literal["block", "send_raw"] = "block"
    unmapped_diagnosis_policy: Literal["block", "send_raw"] = "send_raw"
    diagnosis_code_source: Literal["icd", "payer_code"] = "icd"
    line_submission_mode: Literal["realtime", "bulk"] = "realtime"
    claim_creation_timing: Literal["early", "deferred"] = "early"
    same_day_encounter_conversion: bool = False


class ConnectorManifest(BaseModel):
    key: str = Field(pattern=r"^[a-z0-9-]+$")
    name: str
    country: str
    channel: Channel = Channel.API
    verbs: frozenset[Verb]
    member_otp_required: bool = False
    credentials: list[CredentialField] = Field(default_factory=list)
    submission: SubmissionConfig = Field(default_factory=SubmissionConfig)
    notes: str | None = None

    def supports(self, verb: Verb) -> bool:
        return verb in self.verbs

    @property
    def supports_incremental_lines(self) -> bool:
        return Verb.ADD_LINE in self.verbs and Verb.UPDATE_LINE in self.verbs

    @property
    def supports_whole_claim(self) -> bool:
        return Verb.SUBMIT_WHOLE_CLAIM in self.verbs
