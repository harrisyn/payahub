# Payahub

AI-driven multi-payer claims integration. Headless first: the REST API is the
product; the web app configures it. Decisions: `docs/adr/`.

## Layout

- `cmd/payahub` the one backend binary: `api`, `worker`, `migrate`, `dev`
- `internal/claim` canonical claim, lifecycle states (money in minor units)
- `internal/connector` connector SDK (typed verbs, manifest, error taxonomy); one package per payer
- `internal/engine` edit rules, submission strategy, send and outcome
- `internal/store` Postgres (pgx), embedded idempotent migrations, audit events
- `internal/queue` River jobs: transactional enqueue, submit worker with reconciliation
- `internal/api` `/v1` on `net/http`
- `openapi/payahub.v1.yaml` API spec (update it with any API change)
- `apps/web` Next.js: landing page, onboarding, configuration, docs

## Commands

- `go test ./...` (unit); with Postgres: `PAYAHUB_TEST_DATABASE_URL=postgres://... go test -race ./...`
- `PAYAHUB_LOAD=20000 go test -run TestThroughput -v ./internal/api/` for a throughput check
- `gofmt -l .` must be empty; `go vet ./...`
- `docker compose up` or `DATABASE_URL=... go run ./cmd/payahub dev` (sandbox key `sk_sandbox_dev`)
- `cd apps/web && pnpm install && pnpm build`

## Rules

- **Never lose a claim.** A claim change and the job it triggers commit in one transaction
  (`river.InsertTx`). Never hold a transaction open across a payer call. Retries reconcile
  with `FindClaim` before resending.
- **No model output goes to a payer.** Connectors build payloads from validated fields. AI-set
  fields carry `Evidence`; below the per-use threshold a claim goes to `needs_review`.
- **Payer knowledge is data.** New payer behaviour goes in the connector manifest
  (`SubmissionConfig`, verbs, credentials), not in engine branches.
- **Translate payer errors once, at the connector edge**, into `connector.ErrorCode`.
- **Every write endpoint honours `Idempotency-Key`.**
- **Migrations must be idempotent** (`IF NOT EXISTS`, existence checks before drops), as in core-api-v2.
- Keep dependencies few: standard library first.
- Porting a connector from serenity-health/core-api-v2 `backend/app/plugins/insurance/`: keep the
  payer quirks its comments document; drop the `Dict[str, Any]` interface.

## Frontend design

Public pages use the `design-taste-frontend` skill (taste-skill) and its browser inspector;
app screens use its app-screen run, then `impeccable` for audit and polish. `frontend-design`
is the fallback for typography and direction. Skills live in `.claude/skills/`.
