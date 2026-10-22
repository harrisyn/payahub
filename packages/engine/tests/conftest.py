from datetime import date
from decimal import Decimal

import pytest

from payahub_claim_model import (
    Claim,
    Code,
    CodeMapping,
    Diagnosis,
    DiagnosisRole,
    Encounter,
    Member,
    Provider,
    ServiceLine,
)


@pytest.fixture
def claim() -> Claim:
    return Claim(
        organization_id="org_test",
        payer_id="sandbox",
        member=Member(member_number="M-1001"),
        provider=Provider(name="Ridge Clinic"),
        encounter=Encounter(start_date=date(2026, 10, 20)),
        lines=[
            ServiceLine(
                description="Consultation",
                unit_price=Decimal("150.00"),
                mapping=CodeMapping(
                    source=Code(system="local", value="CONS"),
                    target=Code(system="payer:sandbox", value="C01"),
                ),
            )
        ],
        diagnoses=[Diagnosis(code=Code(system="icd-10", value="B54"), role=DiagnosisRole.PRIMARY)],
    )
