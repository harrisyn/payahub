-- Who is waiting on a claim or member check right now. A save sends its
-- claim_saved notification only when a row is here, because every NOTIFY
-- takes one database-wide lock at commit and so puts all saving commits in a
-- single line. Unlogged: after a crash the waiting requests are gone too.
-- Idempotent.
CREATE UNLOGGED TABLE IF NOT EXISTS claim_waits (
    id     text NOT NULL,
    waiter text NOT NULL,
    until  timestamptz NOT NULL,
    PRIMARY KEY (id, waiter)
);
