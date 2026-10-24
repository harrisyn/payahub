# Payahub

Send any claim, in any form, to any supported payer. One REST API for
real-time submission, plus document, batch and web-form intake later, all
landing in one canonical claim and one AI-assisted, rules-governed engine.

Status: first drop. The landing page, sign-up and onboarding console work end
to end against the sandbox payer. Real payer connectors (XCeli, Medserve,
SmartClaims, RX Insurance, ClaimIQ) are listed and take credentials now; their
send paths are ported from Serenity in phases 0 and 1.

## Run it

```bash
docker compose up --build    # web :3000, API :8080 (sandbox key), Mailpit :8025, Postgres :5432
```

Production runs on Dokploy from `docker-compose.prod.yml`: see [docs/deploy.md](docs/deploy.md).

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

### The web app

```bash
cd apps/web && pnpm install
PAYAHUB_API_URL=http://localhost:8080 pnpm dev     # http://localhost:3000
```

The landing page is `/`, sign-up is `/start`, the console is `/console` and the
API reference is `/docs`. The web server talks to the API from the server side
only; the session lives in an httpOnly cookie. Console endpoints take a session
token, never an API key.

### Configuration

| Variable | What |
| --- | --- |
| `DATABASE_URL` | Postgres |
| `PAYAHUB_VAULT_KEY` | Base64 32-byte key that seals insurer credentials (`openssl rand -base64 32`). Required for `api` and `worker`; `dev` makes a throwaway one if unset. Losing it means re-entering every credential. |
| `PAYAHUB_ADDR` | Listen address, default `:8080` |
| `PAYAHUB_WORKERS` | Concurrent submissions per worker process, default 100 |
| `PAYAHUB_SMTP_URL` | Mail server for Payahub's own email, `smtp://user:password@host:587` (STARTTLS when offered). Unset, email is only logged. Operators get an email for every new enquiry. |
| `PAYAHUB_MAIL_FROM` | Sender address, default `no-reply@payahub.com` |
| `PAYAHUB_CONSOLE_URL` | The web app's public address, for links in email |
| `PAYAHUB_API_URL` | (web) Where the web server finds the API |
| `NEXT_PUBLIC_SITE_URL` | (web) Canonical site URL for metadata and the sitemap |
| `NEXT_PUBLIC_CONTACT_EMAIL` | (web) The address the landing page shows |
| `NEXT_PUBLIC_API_URL` | (web) The public API address shown to developers in the console |
| `PAYAHUB_INSECURE_COOKIES` | (web) `1` drops the Secure flag from session cookies, for the local Docker stack on plain http only |

`NEXT_PUBLIC_` values are written into the web build, so the image takes them as build arguments.

In production run `payahub migrate` once per deploy, then any number of
`payahub api` and `payahub worker` processes (`docker-compose.prod.yml` does this).
`payahub healthcheck` exits 0 when the API in the same container answers `/v1/health`.

## Payers, platforms and standard items

Payers are one shared catalogue (`internal/catalog/data/payers.json`), set up
once for every organisation. A payer is standalone (reached directly) or sits
behind a platform such as XCeli or Medserve; the catalogue holds its code on
that platform. An organisation adds its account on a platform once, and it
serves every payer the platform carries.

Billable items have one standard list (`items.json`: services, labs, imaging,
procedures, medicines, consumables). An organisation maps its own codes to it
once; payer codes hang off the standard items (`data/codes/<payer>.json`). A
claim line travels own code -> standard item -> the payer's code.

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
| `internal/catalog` | Shared payer catalogue and standard item list (data in `data/`), suggestions, line resolution |
| `internal/connector` | Connector SDK, sandbox payer, platform connectors |
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
