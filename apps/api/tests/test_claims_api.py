from fastapi.testclient import TestClient

from payahub_api.main import app

client = TestClient(app)
AUTH = {"Authorization": "Bearer sk_sandbox_dev"}

CLAIM = {
    "payer_id": "sandbox",
    "member": {"member_number": "M-1001"},
    "provider": {"name": "Ridge Clinic"},
    "encounter": {"start_date": "2026-10-20"},
    "lines": [
        {
            "description": "Consultation",
            "unit_price": "150.00",
            "mapping": {
                "source": {"system": "local", "value": "CONS"},
                "target": {"system": "payer:sandbox", "value": "C01"},
            },
        }
    ],
    "diagnoses": [{"code": {"system": "icd-10", "value": "B54"}, "role": "primary"}],
}


def test_requires_api_key():
    r = client.post("/v1/claims", json=CLAIM)
    assert r.status_code == 401
    assert r.json()["error"]["code"] == "unauthenticated"


def test_lists_payers():
    keys = {p["id"] for p in client.get("/v1/payers").json()}
    assert {"sandbox", "claimiq"} <= keys


def test_create_is_idempotent_then_submit_end_to_end():
    headers = {**AUTH, "Idempotency-Key": "abc-123"}
    first = client.post("/v1/claims", json=CLAIM, headers=headers)
    again = client.post("/v1/claims", json=CLAIM, headers=headers)
    assert first.status_code == 201
    assert first.json()["id"] == again.json()["id"]

    claim_id = first.json()["id"]
    submitted = client.post(f"/v1/claims/{claim_id}/submit", headers=AUTH).json()
    assert submitted["claim"]["state"] == "acknowledged"
    assert client.get(f"/v1/claims/{claim_id}", headers=AUTH).json()["state"] == "acknowledged"

    twice = client.post(f"/v1/claims/{claim_id}/submit", headers=AUTH)
    assert twice.status_code == 409


def test_validate_reports_issues_without_storing():
    bad = {**CLAIM, "diagnoses": []}
    r = client.post("/v1/claims/validate", json=bad, headers=AUTH).json()
    assert r["state"] == "draft"
    assert r["issues"][0]["code"] == "no_primary_diagnosis"
