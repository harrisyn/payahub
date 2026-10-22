from payahub_connectors.sdk import ConnectorManifest, SubmissionConfig, Verb
from payahub_engine import choose_plan, resolve_submission_config

INCREMENTAL = {Verb.OPEN_CLAIM, Verb.ADD_LINE, Verb.UPDATE_LINE, Verb.FINALIZE_CLAIM}


def manifest(verbs, **submission):
    return ConnectorManifest(
        key="t", name="T", country="GH", verbs=frozenset(verbs), submission=SubmissionConfig(**submission)
    )


def test_incremental_payer_keeps_realtime_early():
    m = manifest(INCREMENTAL)
    plan = choose_plan(m, m.submission)
    assert (plan.line_mode, plan.timing) == ("realtime", "early")


def test_whole_claim_only_payer_is_clamped_to_bulk_deferred():
    m = manifest({Verb.SUBMIT_WHOLE_CLAIM})
    plan = choose_plan(m, m.submission)
    assert (plan.line_mode, plan.timing) == ("bulk", "deferred")
    assert plan.whole_claim


def test_deferred_without_whole_claim_support_falls_back_to_early():
    m = manifest(INCREMENTAL, line_submission_mode="bulk", claim_creation_timing="deferred")
    plan = choose_plan(m, m.submission)
    assert (plan.line_mode, plan.timing) == ("bulk", "early")


def test_deferred_with_realtime_is_invalid_and_clamped():
    m = manifest(INCREMENTAL | {Verb.SUBMIT_WHOLE_CLAIM}, claim_creation_timing="deferred")
    plan = choose_plan(m, m.submission)
    assert (plan.line_mode, plan.timing) == ("bulk", "deferred")


def test_org_overrides_apply_and_unknown_keys_are_ignored():
    m = manifest(INCREMENTAL)
    config = resolve_submission_config(m, {"unmapped_line_policy": "send_raw", "nonsense": 1})
    assert config.unmapped_line_policy == "send_raw"
