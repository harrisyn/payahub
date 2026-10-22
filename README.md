# Payahub

Send any claim, in any form, to any supported payer. One REST API for
real-time submission, plus document, batch and web-form intake, all landing in
one canonical claim and one AI-assisted, rules-governed engine.

Status: scaffold. The sandbox payer works end to end; real payer connectors
are ported from Serenity in phases 0 and 1.

## Quick start

```bash
uv sync --all-packages
uv run pytest -q
uv run uvicorn payahub_api.main:app --reload
```

Then, with the sandbox key:

```bash
curl -s localhost:8000/v1/claims \
  -H 'Authorization: Bearer sk_sandbox_dev' -H 'Idempotency-Key: demo-1' \
  -H 'Content-Type: application/json' -d '{
    "payer_id": "sandbox",
    "member": {"member_number": "M-1001"},
    "provider": {"name": "Ridge Clinic"},
    "encounter": {"start_date": "2026-10-20"},
    "lines": [{"description": "Consultation", "unit_price": "150.00",
      "mapping": {"source": {"system": "local", "value": "CONS"},
                  "target": {"system": "payer:sandbox", "value": "C01"}}}],
    "diagnoses": [{"code": {"system": "icd-10", "value": "B54"}, "role": "primary"}]
  }'
# then POST /v1/claims/{id}/submit
```

Sandbox member numbers starting `NOTFOUND`, `OTP` or `REJECT` exercise the
failure paths. API docs: http://localhost:8000/v1/docs

Web app: `cd apps/web && pnpm install && pnpm dev`.

## Layout

| Path | What |
| --- | --- |
| `packages/claim-model` | Canonical claim and lifecycle |
| `packages/connectors` | Connector SDK, sandbox payer, payer connectors |
| `packages/engine` | Edit rules, submission strategy, pipeline |
| `apps/api` | Public API (`/v1`) |
| `apps/web` | Landing page, onboarding, dashboard, docs |
| `openapi/` | Generated spec, source for docs and SDKs |
| `docs/adr/` | Architecture decisions |
| `.claude/skills/` | Design skills: taste-skill, impeccable, frontend-design |

## Third-party skills

`.claude/skills/design-taste-frontend` is from [Leonxlnx/taste-skill](https://github.com/Leonxlnx/taste-skill)
(MIT, commit 6010f91), `impeccable` from [pbakaus/impeccable](https://github.com/pbakaus/impeccable)
(Apache-2.0, commit 8644175), `frontend-design` from [anthropics/skills](https://github.com/anthropics/skills)
(commit dbd4588). Licences are kept alongside each.
