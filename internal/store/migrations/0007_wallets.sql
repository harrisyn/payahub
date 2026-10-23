-- Wallets: a double-entry ledger. Every movement of money is one
-- transaction whose entries sum to zero; an account's balance is the sum of
-- its entries. Organisations hold a wallet each; the platform holds its
-- revenue wallet and, per payment provider, the cash it has collected and the
-- fees it has paid. Payment providers only fund wallets and pay withdrawals
-- out of them.
CREATE TABLE IF NOT EXISTS ledger_accounts (
    id              text PRIMARY KEY,  -- org:<org>:wallet:GHS, platform:revenue:GHS, platform:cash:paystack:GHS
    owner           text NOT NULL,     -- 'platform' or an organisation id
    kind            text NOT NULL,     -- wallet | revenue | cash | fees | payouts_pending | adjustments
    currency        text NOT NULL,
    -- Kept on wallets, which are checked before they are debited. Platform
    -- accounts take an entry for every claim, so their balance is summed on
    -- read instead of being a row every charge would lock.
    keeps_balance   boolean NOT NULL,
    balance         bigint NOT NULL DEFAULT 0,  -- credits minus debits
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ledger_accounts_owner_idx ON ledger_accounts (owner);

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id               text PRIMARY KEY,
    key              text NOT NULL,     -- what it is for; one transaction per key, ever
    kind             text NOT NULL,     -- funding | charge | fee | withdrawal | withdrawal_paid | withdrawal_returned | adjustment
    organization_id  text REFERENCES organizations (id),
    memo             text NOT NULL DEFAULT '',
    detail           jsonb,
    created_by       text,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS ledger_transactions_key_idx ON ledger_transactions (key);
CREATE INDEX IF NOT EXISTS ledger_transactions_org_idx ON ledger_transactions (organization_id, created_at DESC);

CREATE TABLE IF NOT EXISTS ledger_entries (
    transaction_id  text NOT NULL REFERENCES ledger_transactions (id),
    account_id      text NOT NULL REFERENCES ledger_accounts (id),
    amount          bigint NOT NULL CHECK (amount <> 0),  -- credit positive, debit negative
    balance_after   bigint,                               -- on accounts that keep a balance
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (transaction_id, account_id)
);
CREATE INDEX IF NOT EXISTS ledger_entries_account_idx ON ledger_entries (account_id, created_at DESC);

-- A funding is one checkout at a payment provider. It moves money only when
-- the provider confirms it, and then exactly once (key funding:<id>).
CREATE TABLE IF NOT EXISTS wallet_fundings (
    id                  text PRIMARY KEY,
    organization_id     text NOT NULL REFERENCES organizations (id),
    provider            text NOT NULL,
    amount              bigint NOT NULL CHECK (amount > 0),
    currency            text NOT NULL,
    status              text NOT NULL DEFAULT 'pending',  -- pending | succeeded | failed | abandoned
    paid_amount         bigint,
    fee                 bigint,
    provider_reference  text,
    checkout_url        text,
    message             text,
    idempotency_key     text,
    created_by          text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    completed_at        timestamptz
);
CREATE INDEX IF NOT EXISTS wallet_fundings_org_idx ON wallet_fundings (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS wallet_fundings_pending_idx ON wallet_fundings (created_at) WHERE status = 'pending';
CREATE UNIQUE INDEX IF NOT EXISTS wallet_fundings_idem_idx ON wallet_fundings (organization_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Where an organisation's withdrawals go.
CREATE TABLE IF NOT EXISTS payout_accounts (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id),
    provider         text NOT NULL,
    type             text NOT NULL,   -- bank | mobile_money
    name             text NOT NULL,
    bank_code        text NOT NULL,
    bank_name        text NOT NULL DEFAULT '',
    account_number   text NOT NULL,
    currency         text NOT NULL,
    recipient_code   text NOT NULL,   -- the provider's code for it
    removed_at       timestamptz,
    created_by       text,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS payout_accounts_org_idx ON payout_accounts (organization_id) WHERE removed_at IS NULL;

-- A withdrawal holds its amount when requested (key withdrawal:<id>), is
-- paid once an operator approves it, and is either settled or returned to
-- the wallet.
CREATE TABLE IF NOT EXISTS wallet_withdrawals (
    id                  text PRIMARY KEY,
    organization_id     text NOT NULL REFERENCES organizations (id),
    payout_account_id   text NOT NULL REFERENCES payout_accounts (id),
    provider            text NOT NULL,
    amount              bigint NOT NULL CHECK (amount > 0),
    currency            text NOT NULL,
    status              text NOT NULL DEFAULT 'requested',  -- requested | approved | paying | paid | failed | rejected
    reason              text NOT NULL DEFAULT '',
    provider_reference  text,
    message             text,
    idempotency_key     text,
    requested_by        text,
    decided_by          text,
    decided_at          timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    completed_at        timestamptz
);
CREATE INDEX IF NOT EXISTS wallet_withdrawals_org_idx ON wallet_withdrawals (organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS wallet_withdrawals_open_idx ON wallet_withdrawals (status, created_at)
    WHERE status IN ('requested', 'approved', 'paying');
CREATE UNIQUE INDEX IF NOT EXISTS wallet_withdrawals_idem_idx ON wallet_withdrawals (organization_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Entries are numbered in the order they were written. A wallet's entries
-- are written under its row lock, so this is the order of its balances.
ALTER TABLE ledger_entries ADD COLUMN IF NOT EXISTS seq bigserial;
CREATE INDEX IF NOT EXISTS ledger_entries_account_seq_idx ON ledger_entries (account_id, seq DESC);
