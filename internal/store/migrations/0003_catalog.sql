-- Payers become one shared catalogue (internal/catalog). What stays per
-- organisation: its account on each platform or payer interface (entered once,
-- used for every payer reached through it), which payers it uses, and how its
-- own item codes map to the standard items. Idempotent like every migration.

CREATE TABLE IF NOT EXISTS connector_accounts (
    organization_id     text NOT NULL REFERENCES organizations (id),
    connector           text NOT NULL,
    environment         text NOT NULL CHECK (environment IN ('sandbox', 'live')),
    credentials_sealed  bytea NOT NULL,
    credential_fields   text[] NOT NULL DEFAULT '{}',
    checked_at          timestamptz,
    check_ok            boolean,
    check_message       text,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, connector, environment)
);

-- Credentials saved before the catalogue were keyed by connector under the
-- payer_id column, and sealed to "org/connector/env", which is also an
-- account's owner string, so they move across unchanged.
INSERT INTO connector_accounts (organization_id, connector, environment, credentials_sealed,
                                credential_fields, checked_at, check_ok, check_message, updated_at)
SELECT organization_id, payer_id, environment, credentials_sealed, credential_fields,
       checked_at, check_ok, check_message, updated_at
FROM payer_settings
WHERE credentials_sealed IS NOT NULL
ON CONFLICT DO NOTHING;

-- The answer to "do you have an account on this payer's platform?":
-- 'have', 'need' (not yet; we show how to get one) or null (not asked).
ALTER TABLE payer_settings ADD COLUMN IF NOT EXISTS account text;

-- An organisation's own item code mapped to a standard item, confirmed by a
-- person (or sent by the organisation's own system through the API).
CREATE TABLE IF NOT EXISTS item_mappings (
    organization_id  text NOT NULL REFERENCES organizations (id),
    local_code       text NOT NULL,
    local_name       text NOT NULL DEFAULT '',
    item_id          text NOT NULL,
    confirmed_by     text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, local_code)
);
CREATE INDEX IF NOT EXISTS item_mappings_item_idx ON item_mappings (organization_id, item_id);
