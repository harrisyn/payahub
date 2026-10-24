-- A model's proposed code from a payer's own list for each standard item,
-- kept until an operator accepts it or asks again. Never used for a claim on
-- its own: an operator saves it as the payer's code. Idempotent.
CREATE TABLE IF NOT EXISTS payer_code_picks (
    payer_id       text NOT NULL,
    item_id        text NOT NULL,
    code           text NOT NULL DEFAULT '', -- empty: the model found no code
    name           text NOT NULL DEFAULT '',
    confidence     double precision NOT NULL,
    reason         text NOT NULL DEFAULT '',
    model          text NOT NULL DEFAULT '',
    prompt_version text NOT NULL DEFAULT '',
    asked_by       text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (payer_id, item_id)
);
