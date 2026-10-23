-- Enquiries from the public site: a clinic asking to be contacted, an insurer
-- or platform that is not listed yet, a payer someone wants us to add, or a
-- developer asking for the interface. Operators read them in the console.
CREATE TABLE IF NOT EXISTS leads (
    id               text PRIMARY KEY,
    kind             text NOT NULL CHECK (kind IN ('contact', 'platform', 'payer', 'specs')),
    name             text NOT NULL DEFAULT '',
    email            text NOT NULL,
    phone            text NOT NULL DEFAULT '',
    organization     text NOT NULL DEFAULT '',
    payer_name       text NOT NULL DEFAULT '',
    monthly_claims   integer CHECK (monthly_claims IS NULL OR monthly_claims >= 0),
    payer_contact    text NOT NULL DEFAULT '',
    message          text NOT NULL DEFAULT '',
    idempotency_key  text,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS leads_idempotency_key ON leads (idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS leads_created_at ON leads (created_at DESC);
