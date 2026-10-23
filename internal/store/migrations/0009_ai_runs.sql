-- Every model call, kept for audit: which model and prompt version produced
-- a suggestion or a read claim form, what it cost and whether it worked.
-- The document itself is not kept here.
CREATE TABLE IF NOT EXISTS ai_runs (
    id               text PRIMARY KEY,
    organization_id  text,
    task             text NOT NULL,
    provider         text NOT NULL DEFAULT '',
    model            text NOT NULL DEFAULT '',
    prompt_version   text NOT NULL,
    prompt_hash      text NOT NULL,
    input_sha256     text NOT NULL DEFAULT '',
    input_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens    bigint NOT NULL DEFAULT 0,
    duration_ms      integer NOT NULL DEFAULT 0,
    ok               boolean NOT NULL,
    error            text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ai_runs_org_created ON ai_runs (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ai_runs_task_created ON ai_runs (task, created_at DESC);
