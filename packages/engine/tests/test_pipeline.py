from payahub_claim_model import ClaimState, Code, CodeMapping, Evidence, FieldOrigin, Member
from payahub_connectors.sandbox import SandboxConnector
from payahub_engine import submit, validate


async def test_clean_claim_is_acknowledged_over_incremental_path(claim):
    outcome = await submit(claim, SandboxConnector())
    assert outcome.error is None
    assert outcome.claim.state == ClaimState.ACKNOWLEDGED
    assert outcome.claim.payer_claim_number.startswith("SBX-")


async def test_whole_claim_path_when_org_defers(claim):
    outcome = await submit(
        claim, SandboxConnector(), {"line_submission_mode": "bulk", "claim_creation_timing": "deferred"}
    )
    assert outcome.claim.state == ClaimState.ACKNOWLEDGED
    assert len(outcome.receipt.line_references) == 1


async def test_payer_refusal_marks_claim_rejected(claim):
    refused = claim.model_copy(update={"member": Member(member_number="REJECT-1")})
    outcome = await submit(refused, SandboxConnector())
    assert outcome.claim.state == ClaimState.REJECTED
    assert outcome.error.payer_message


async def test_unmapped_line_blocks_submission(claim):
    line = claim.lines[0].model_copy(update={"mapping": None})
    outcome = await submit(claim.model_copy(update={"lines": [line]}), SandboxConnector())
    assert outcome.claim.state == ClaimState.DRAFT
    assert [i.code for i in outcome.issues] == ["line_unmapped"]


def test_low_confidence_ai_mapping_needs_review(claim):
    mapping = CodeMapping(
        source=Code(system="local", value="CONS"),
        target=Code(system="payer:sandbox", value="C01"),
        evidence=Evidence(origin=FieldOrigin.AI_MAPPED, confidence=0.7),
    )
    line = claim.lines[0].model_copy(update={"mapping": mapping})
    result = validate(claim.model_copy(update={"lines": [line]}), SandboxConnector())
    assert result.state == ClaimState.NEEDS_REVIEW
