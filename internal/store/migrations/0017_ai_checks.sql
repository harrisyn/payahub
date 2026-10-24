-- Regression checks of the matching: each run's score against the known
-- answers, with the model that gave it, kept so a change of model, provider
-- or prompt can be compared with the last. Idempotent.
CREATE TABLE IF NOT EXISTS ai_check_runs (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task            text NOT NULL,
    method          text NOT NULL,
    models          text NOT NULL DEFAULT '',
    prompt_version  text NOT NULL DEFAULT '',
    total           integer NOT NULL,
    passed          integer NOT NULL,
    groups          integer NOT NULL,
    groups_passed   integer NOT NULL,
    report          jsonb NOT NULL,
    run_by          text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ai_check_runs_created_idx ON ai_check_runs (task, created_at DESC);
