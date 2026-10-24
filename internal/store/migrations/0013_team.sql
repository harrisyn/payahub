-- Team members and roles. Everyone who signed up before roles existed
-- created their organisation, so they are its owners.
ALTER TABLE users ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'owner';
ALTER TABLE users ADD COLUMN IF NOT EXISTS removed_at timestamptz;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_role_check') THEN
        ALTER TABLE users ADD CONSTRAINT users_role_check
            CHECK (role IN ('owner', 'admin', 'claims', 'finance', 'developer', 'viewer'));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS users_org_idx ON users (organization_id, created_at);

-- An invitation is accepted once, by the person holding the link, within a week.
CREATE TABLE IF NOT EXISTS invitations (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id),
    email            text NOT NULL,
    name             text NOT NULL DEFAULT '',
    role             text NOT NULL CHECK (role IN ('owner', 'admin', 'claims', 'finance', 'developer', 'viewer')),
    token_sha256     bytea NOT NULL UNIQUE,
    invited_by       text NOT NULL REFERENCES users (id),
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    accepted_at      timestamptz,
    revoked_at       timestamptz
);
CREATE INDEX IF NOT EXISTS invitations_org_idx ON invitations (organization_id, created_at);
-- One open invitation per address in an organisation.
CREATE UNIQUE INDEX IF NOT EXISTS invitations_open_idx ON invitations (organization_id, lower(email))
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
