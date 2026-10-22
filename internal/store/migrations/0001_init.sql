-- Every statement is idempotent: a migration may be replayed against a schema
-- that already has some or all of it (see CLAUDE.md).

CREATE TABLE IF NOT EXISTS organizations (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_keys (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id),
    environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
    key_sha256       bytea NOT NULL UNIQUE,
    prefix           text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    revoked_at       timestamptz
);

-- Per organisation and payer: whether it is enabled and its submission
-- overrides. Credentials live in the vault, referenced here, never inline.
CREATE TABLE IF NOT EXISTS payer_settings (
    organization_id  text NOT NULL REFERENCES organizations (id),
    payer_id         text NOT NULL,
    environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
    enabled          boolean NOT NULL DEFAULT true,
    credentials_ref  text,
    overrides        jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, payer_id, environment)
);

-- body holds the full canonical claim; the columns beside it are copies for
-- filtering and indexing. submission_queued is true while a submit job owns it.
CREATE TABLE IF NOT EXISTS claims (
    id                  text PRIMARY KEY,
    organization_id     text NOT NULL REFERENCES organizations (id),
    environment         text NOT NULL,
    payer_id            text NOT NULL,
    state               text NOT NULL,
    payer_claim_number  text,
    currency            text NOT NULL,
    total               bigint NOT NULL,
    submission_queued   boolean NOT NULL DEFAULT false,
    body                jsonb NOT NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS claims_org_created_idx ON claims (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS claims_org_state_idx ON claims (organization_id, state);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    organization_id  text NOT NULL,
    key              text NOT NULL,
    request_sha256   bytea NOT NULL,
    claim_id         text NOT NULL REFERENCES claims (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, key)
);

-- Append-only audit trail: every state change and submission attempt.
CREATE TABLE IF NOT EXISTS claim_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    claim_id    text NOT NULL REFERENCES claims (id),
    kind        text NOT NULL,
    from_state  text,
    to_state    text NOT NULL,
    detail      jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS claim_events_claim_idx ON claim_events (claim_id, id);
