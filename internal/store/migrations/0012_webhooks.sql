-- Webhooks: an organisation's own system is told of every claim event.
-- A delivery row is written in the same transaction as the event it
-- carries, so no event is lost between saving and sending.
CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id),
    environment      text NOT NULL,
    url              text NOT NULL,
    description      text NOT NULL DEFAULT '',
    secret_sealed    bytea NOT NULL,
    enabled          boolean NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS webhook_endpoints_org_env ON webhook_endpoints (organization_id, environment) WHERE enabled;

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    endpoint_id      text NOT NULL REFERENCES webhook_endpoints (id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    payload          jsonb NOT NULL,
    status           text NOT NULL DEFAULT 'pending', -- pending, delivered, failed
    attempts         integer NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz NOT NULL DEFAULT now(),
    last_status      integer,
    last_error       text NOT NULL DEFAULT '',
    delivered_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS webhook_deliveries_due ON webhook_deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS webhook_deliveries_endpoint ON webhook_deliveries (endpoint_id, id DESC);
