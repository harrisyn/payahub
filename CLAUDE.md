# Payahub

AI-driven multi-payer claims integration. Plan and architecture: see
`docs/adr/` and the "Payahub: analysis and execution plan" doc.

## Layout

- `packages/claim-model` canonical Claim, lifecycle states
- `packages/connectors` connector SDK (typed verbs, manifest, error taxonomy) and one folder per payer
- `packages/engine` edit rules, submission strategy, submission pipeline
- `apps/api` FastAPI public API under `/v1`
- `apps/web` Next.js: landing page, onboarding, dashboard, docs

## Commands

- `uv sync --all-packages` then `uv run pytest -q`, `uv run ruff check .`, `uv run ruff format .`
- `uv run python scripts/export_openapi.py` after any API change (CI fails if the spec is stale)
- `cd apps/web && pnpm install && pnpm build`
- `uv run uvicorn payahub_api.main:app --reload` (sandbox key: `sk_sandbox_dev`)

## Rules

- **No model output goes to a payer.** Connectors build payloads from validated fields. AI-set
  fields carry `Evidence`; below the per-use threshold a claim goes to `needs_review`.
- **Payer knowledge is data.** New payer behaviour goes in the connector manifest
  (`SubmissionConfig`, verbs, credentials), not in engine branches.
- **Translate payer errors once, at the connector edge**, into `ErrorCode`. Never match payer
  text above the connector.
- **Every write endpoint honours `Idempotency-Key`.**
- **Alembic migrations must be idempotent** (same rule as core-api-v2): tolerate objects that
  already exist on upgrade, check existence before dropping on downgrade.
- Porting a connector from serenity-health/core-api-v2 `backend/app/plugins/insurance/`: keep the
  payer quirks its comments document; drop the `Dict[str, Any]` interface.

## Frontend design

Public pages use the `design-taste-frontend` skill (taste-skill) and its browser inspector;
app screens use its app-screen run, then `impeccable` for audit and polish. `frontend-design`
is the fallback for typography and direction. Skills live in `.claude/skills/`.
