-- The shared catalogue moves into the database so operators can edit payers,
-- their settings and their codes without a deploy. The files in
-- internal/catalog/data stay as the seed: SeedCatalog inserts what is missing
-- and never overwrites an operator's edit. Idempotent like every migration.

ALTER TABLE users ADD COLUMN IF NOT EXISTS operator boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS catalog_payers (
    id               text PRIMARY KEY,
    name             text NOT NULL,
    country          text NOT NULL,
    connector        text NOT NULL,
    fields           jsonb NOT NULL DEFAULT '{}',
    notes            text NOT NULL DEFAULT '',
    generated_codes  text NOT NULL DEFAULT '',
    inactive         boolean NOT NULL DEFAULT false,
    updated_by       text NOT NULL DEFAULT 'seed',
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS catalog_items (
    id          text PRIMARY KEY,
    kind        text NOT NULL,
    body        jsonb NOT NULL,
    updated_by  text NOT NULL DEFAULT 'seed',
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- A payer's code for a standard item.
CREATE TABLE IF NOT EXISTS catalog_payer_codes (
    payer_id     text NOT NULL REFERENCES catalog_payers (id),
    item_id      text NOT NULL REFERENCES catalog_items (id),
    code         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    updated_by   text NOT NULL DEFAULT 'seed',
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (payer_id, item_id)
);

-- A payer's own code list (its tariff), as loaded by an operator.
CREATE TABLE IF NOT EXISTS catalog_payer_lists (
    payer_id  text NOT NULL REFERENCES catalog_payers (id),
    position  integer NOT NULL,
    code      text NOT NULL,
    name      text NOT NULL,
    grp       text NOT NULL DEFAULT '',
    price     bigint,
    PRIMARY KEY (payer_id, code)
);

-- Every operator change, for audit, and the version processes watch to know
-- when to reload their snapshot.
CREATE TABLE IF NOT EXISTS catalog_changes (
    id          bigserial PRIMARY KEY,
    payer_id    text,
    kind        text NOT NULL,
    detail      jsonb NOT NULL DEFAULT '{}',
    changed_by  text NOT NULL,
    changed_at  timestamptz NOT NULL DEFAULT now()
);
