# Payahub

Send any claim, in any form, to any supported payer. One REST API for
real-time submission, plus document, batch and web-form intake later, all
landing in one canonical claim and one AI-assisted, rules-governed engine.

Status: scaffold. The sandbox payer works end to end; real payer connectors
are ported from Serenity in phases 0 and 1.

## Run it

```bash
docker compose up            # Postgres + API + worker on :8080
```

or, with your own Postgres:

```bash
DATABASE_URL=postgres://localhost:5432/payahub go run ./cmd/payahub dev
```

Then, with the sandbox key (amounts are in minor units, so 15000 = GHS 150.00):

```bash
curl -s localhost:8080/v1/claims \
  -H 'Authorization: Bearer sk_sandbox_dev' -H 'Idempotency-Key: demo-1' \
  -d '{
    "payer_id": "sandbox",
    "member": {"member_number": "M-1001"},
    "provider": {"name": "Ridge Clinic"},
    "encounter": {"start_date": "2026-10-20"},
    "lines": [{"description": "Consultation", "unit_price": 15000,
      "mapping": {"source": {"system": "local", "value": "CONS"},
                  "target": {"system": "payer:sandbox", "value": "C01"}}}],
    "diagnoses": [{"code": {"system": "icd-10", "value": "B54"}, "role": "primary"}]
  }'
curl -s -X POST localhost:8080/v1/claims/<id>/submit -H 'Authorization: Bearer sk_sandbox_dev'
curl -s localhost:8080/v1/claims/<id>/events -H 'Authorization: Bearer sk_sandbox_dev'
```

Sandbox member numbers starting `NOTFOUND`, `OTP`, `REJECT`, `FLAKY` or
`LOSTREPLY` exercise the failure and recovery paths. The API is described in
[`openapi/payahub.v1.yaml`](openapi/payahub.v1.yaml).

In production run `payahub migrate` once per deploy, then any number of
`payahub api` and `payahub worker` processes.

## How a claim is never lost

See [ADR 0003](docs/adr/0003-durable-queue.md). In short: the claim change and
its queued job commit in one Postgres transaction; workers never hold a
transaction across a payer call; a retried job asks the payer whether it
already has the claim before sending again; every step is in the audit trail.

## Layout

| Path | What |
| --- | --- |
| `cmd/payahub` | The backend binary: `api`, `worker`, `migrate`, `dev` |
| `internal/claim` | Canonical claim and lifecycle |
| `internal/connector` | Connector SDK, sandbox payer, payer connectors |
| `internal/engine` | Edit rules, submission strategy, send and outcome |
| `internal/store` | Postgres, migrations, audit events |
| `internal/queue` | Durable submission jobs (River) |
| `internal/api` | Public API (`/v1`) |
| `apps/web` | Landing page, onboarding, configuration, docs |
| `docs/adr/` | Architecture decisions |
| `.claude/skills/` | Design skills: taste-skill, impeccable, frontend-design |

## Third-party skills

`.claude/skills/design-taste-frontend` is from [Leonxlnx/taste-skill](https://github.com/Leonxlnx/taste-skill)
(MIT, commit 6010f91), `impeccable` from [pbakaus/impeccable](https://github.com/pbakaus/impeccable)
(Apache-2.0, commit 8644175), `frontend-design` from [anthropics/skills](https://github.com/anthropics/skills)
(commit dbd4588). Licences are kept alongside each.
