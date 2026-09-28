-- Migration: 001_init.sql
-- Mini Banking Ledger Schema (Advanced Edition)

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
    id             UUID PRIMARY KEY,
    kind           TEXT NOT NULL CHECK (kind IN ('deposit','withdrawal','transfer','reversal','split_payment','fx_transfer','hold_capture')),
    reversal_of_id UUID REFERENCES transactions(id),
    metadata       JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS entries (
    id             BIGSERIAL PRIMARY KEY,
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    account_id     UUID NOT NULL REFERENCES accounts(id),
    direction      TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount         BIGINT NOT NULL CHECK (amount > 0),
    prev_hash      TEXT NOT NULL DEFAULT '',
    entry_hash     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_entries_account ON entries(account_id, id DESC);

CREATE TABLE IF NOT EXISTS holds (
    id          UUID PRIMARY KEY,
    account_id  UUID NOT NULL REFERENCES accounts(id),
    amount      BIGINT NOT NULL CHECK (amount > 0),
    status      TEXT NOT NULL CHECK (status IN ('active', 'captured', 'voided')),
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    captured_at TIMESTAMPTZ,
    voided_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_holds_account_active ON holds(account_id) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    transaction_id  UUID REFERENCES transactions(id),
    response_status INT,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed system counterparty accounts
-- 1. CASH: counterparty for physical cash deposits & withdrawals
INSERT INTO accounts (id, owner, type, currency, balance)
VALUES ('00000000-0000-0000-0000-000000000001', 'CASH', 'system', 'INR', 0)
ON CONFLICT (id) DO NOTHING;

-- 2. FX_SETTLEMENT: counterparty bridge for cross-currency transfers
INSERT INTO accounts (id, owner, type, currency, balance)
VALUES ('00000000-0000-0000-0000-000000000002', 'FX_SETTLEMENT', 'system', 'XXX', 0)
ON CONFLICT (id) DO NOTHING;
