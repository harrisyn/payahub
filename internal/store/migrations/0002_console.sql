-- Accounts, sessions, onboarding and payer credentials for the web console.
-- Idempotent like every migration here.

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS country text NOT NULL DEFAULT 'GH';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS plan text NOT NULL DEFAULT 'trial';
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS trial_ends_at timestamptz;
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS profile jsonb;

CREATE TABLE IF NOT EXISTS users (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id),
    email            text NOT NULL,
    name             text NOT NULL,
    password_hash    text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS users_email_idx ON users (lower(email));

CREATE TABLE IF NOT EXISTS sessions (
    token_sha256     bytea PRIMARY KEY,
    user_id          text NOT NULL REFERENCES users (id),
    organization_id  text NOT NULL REFERENCES organizations (id),
    expires_at       timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS name text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS api_keys_org_idx ON api_keys (organization_id, created_at);

-- Credentials are sealed with the vault key (AES-GCM) and never returned;
-- credential_fields lists which fields are set so the console can show them.
ALTER TABLE payer_settings ADD COLUMN IF NOT EXISTS credentials_sealed bytea;
ALTER TABLE payer_settings ADD COLUMN IF NOT EXISTS credential_fields text[] NOT NULL DEFAULT '{}';
ALTER TABLE payer_settings ADD COLUMN IF NOT EXISTS checked_at timestamptz;
ALTER TABLE payer_settings ADD COLUMN IF NOT EXISTS check_ok boolean;
ALTER TABLE payer_settings ADD COLUMN IF NOT EXISTS check_message text;

-- Billable claims per organisation and month are counted from claims; this
-- index keeps that count cheap.
CREATE INDEX IF NOT EXISTS claims_org_env_state_idx ON claims (organization_id, environment, state, updated_at);
