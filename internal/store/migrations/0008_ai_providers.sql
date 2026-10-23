-- Model providers the platform uses (Claude, OpenAI, OpenAI-compatible
-- endpoints), set by operators. Keys are sealed with the vault key and never
-- returned; key_hint is the last four characters, to tell keys apart.
CREATE TABLE IF NOT EXISTS ai_providers (
    id              text PRIMARY KEY,
    kind            text NOT NULL,      -- anthropic | openai | openai_compatible
    name            text NOT NULL,
    base_url        text NOT NULL DEFAULT '',
    model           text NOT NULL,
    effort          text NOT NULL DEFAULT '',
    json_mode       text NOT NULL DEFAULT '',
    tasks           text[] NOT NULL DEFAULT '{}',
    priority        integer NOT NULL DEFAULT 100,  -- lower answers first
    enabled         boolean NOT NULL DEFAULT true,
    api_key_sealed  bytea,
    key_hint        text NOT NULL DEFAULT '',
    checked_at      timestamptz,
    check_ok        boolean,
    check_message   text,
    updated_by      text,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
