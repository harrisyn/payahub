-- A member check asks the payer whether a member is covered, or to send the
-- member a one-time code. The worker makes the payer call; the API waits
-- for the answer. The code a member gives is never stored here.
CREATE TABLE IF NOT EXISTS member_checks (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id),
    environment      text NOT NULL CHECK (environment IN ('sandbox', 'live')),
    payer_id         text NOT NULL,
    member_number    text NOT NULL,
    action           text NOT NULL CHECK (action IN ('verify', 'send_otp')),
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
    result           jsonb,
    error_code       text NOT NULL DEFAULT '',
    error_message    text NOT NULL DEFAULT '',
    created_by       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    finished_at      timestamptz
);
CREATE INDEX IF NOT EXISTS member_checks_org_idx ON member_checks (organization_id, created_at DESC);
ALTER TABLE member_checks ADD COLUMN IF NOT EXISTS idempotency_key text;
ALTER TABLE member_checks ADD COLUMN IF NOT EXISTS request_sha256 bytea;
CREATE UNIQUE INDEX IF NOT EXISTS member_checks_idem_idx ON member_checks (organization_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
