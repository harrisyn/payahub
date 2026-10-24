-- Payer rules as data. Each code in a payer's own list may carry what the
-- payer allows for it (most per claim, only for one sex or an age range, prior
-- authorisation); the payer's policy says whether a price above its tariff or
-- a code outside its list stops a claim or only warns. Idempotent.

ALTER TABLE catalog_payers ADD COLUMN IF NOT EXISTS rules jsonb NOT NULL DEFAULT '{}';

ALTER TABLE catalog_payer_lists ADD COLUMN IF NOT EXISTS max_quantity bigint;
ALTER TABLE catalog_payer_lists ADD COLUMN IF NOT EXISTS sex text NOT NULL DEFAULT '';
ALTER TABLE catalog_payer_lists ADD COLUMN IF NOT EXISTS min_age integer;
ALTER TABLE catalog_payer_lists ADD COLUMN IF NOT EXISTS max_age integer;
ALTER TABLE catalog_payer_lists ADD COLUMN IF NOT EXISTS needs_authorization boolean NOT NULL DEFAULT false;
