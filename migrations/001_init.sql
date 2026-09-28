-- Migration: 001_init.sql
-- Mini Banking Ledger Schema

CREATE TABLE IF NOT EXISTS accounts (
    id         UUID PRIMARY KEY,
    owner      TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('customer','system')),
    currency   CHAR(3) NOT NULL DEFAULT 'INR',
    balance    BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT no_negative CHECK (type = 'system' OR balance >= 0)
);

CREATE TABLE IF NOT EXISTS transactions (
    id         UUID PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('deposit','withdrawal','transfer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS entries (
    id             BIGSERIAL PRIMARY KEY,
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    account_id     UUID NOT NULL REFERENCES accounts(id),
    direction      TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount         BIGINT NOT NULL CHECK (amount > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_entries_account ON entries(account_id, id DESC);

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    transaction_id  UUID REFERENCES transactions(id),
    response_status INT,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the system counterparty account for deposits and withdrawals
INSERT INTO accounts (id, owner, type, currency, balance)
VALUES ('00000000-0000-0000-0000-000000000001', 'CASH', 'system', 'INR', 0)
ON CONFLICT (id) DO NOTHING;
