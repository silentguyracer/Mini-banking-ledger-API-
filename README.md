# Advanced Banking Ledger Platform

A production-grade, double-entry financial ledger and transaction processing platform written in **Go 1.25** and **PostgreSQL 16**. Designed to emulate enterprise banking infrastructure (e.g. Stripe, Modern Treasury, Form3) with strict multi-account ACID guarantees, deterministic row-lock concurrency control, cryptographic tamper-evident audit trails, two-phase reservation holds, multi-party split payments, compensating reversals, and cross-currency foreign exchange (FX).

---

## 1. High-Level Architecture

```
                       +-----------------------------+
                       |    Client / HTTP Requests   |
                       +--------------+--------------+
                                      |
                                      v
                       +-----------------------------+
                       |   Chi Router & Middleware   |
                       |  - Request ID & RealIP      |
                       |  - Structured Logging (slog)|
                       |  - Prometheus Instrumentation|
                       +--------------+--------------+
                                      |
                                      v
                       +-----------------------------+
                       |       Ledger Service        |
                       |  - Business Rule Validation |
                       |  - Deterministic SHA-256    |
                       |  - Available Balance Logic  |
                       +--------------+--------------+
                                      |
                                      v
                       +-----------------------------+
                       |      PostgreSQL Store       |
                       |  - Idempotency Claiming     |
                       |  - Deterministic Row Locks  |
                       |  - Double-Entry Engine      |
                       |  - Hash-Chained Audit Trail |
                       |  - Holds & Settlement Engine|
                       +--------------+--------------+
                                      |
                                      v
                       +-----------------------------+
                       |        PostgreSQL 16        |
                       | accounts | transactions     |
                       | entries  | holds            |
                       | idempotency_keys            |
                       +-----------------------------+
```

---

## 2. Advanced Engineering Capabilities

### 🛡️ 1. Two-Phase Commit Authorizations (Holds, Captures & Voids)
Real-world payment processors (Visa, Mastercard, Stripe) process card payments in two distinct phases:
- **Phase 1: Authorization (Hold):** A merchant requests a reserve hold (`POST /accounts/{id}/holds`). The ledger verifies:
  $$\text{Available Balance} = \text{Balance} - \sum \text{Active Holds} \ge \text{Hold Amount}$$
  The amount is ring-fenced so the customer cannot double-spend it, but no ledger entries are posted yet.
- **Phase 2a: Capture (Settlement):** The merchant settles the transaction (`POST /holds/{id}/capture`) with the finalized amount ($\le \text{Hold Amount}$). The hold is marked `captured`, customer balance is debited, counterparty is credited, and double-entry entries are posted.
- **Phase 2b: Void (Cancellation):** If the purchase is cancelled (`POST /holds/{id}/void`), the hold is marked `voided`, instantly releasing the hold and restoring available balance.

### ⛓️ 2. Cryptographic Tamper-Evident Audit Trail (Hash Chaining)
To prevent internal fraud or rogue database tampering (e.g. `UPDATE entries SET amount = ...`), every entry is cryptographically linked to its predecessor in a hash chain:
$$\text{Entry Hash}_n = \text{SHA256}(\text{Entry Hash}_{n-1} \parallel \text{Account ID} \parallel \text{Direction} \parallel \text{Amount})$$
- The `GET /admin/verify-audit-chain` endpoint traverses the entire ledger and cryptographically verifies every link in the chain. Any manual mutation or deleted record breaks the cryptographic chain and triggers an instant alert.

### 🔄 3. Pure Compensating Reversals (Refunds / Voids)
- Accounting entries are strictly **append-only**. Rows in `entries` are never updated or deleted.
- Reversing a transaction (`POST /transfers/{id}/reversals`) automatically generates an inverse double-entry transaction (`kind = 'reversal'`) with `reversal_of_id`:
  - Every original debit leg becomes a credit leg.
  - Every original credit leg becomes a debit leg.
- The reversal locks all accounts involved in deterministic order, verifies balance sufficiency, and commits atomically.

### 💸 4. Multi-Party Split Payments & Fee Dedication
Supports arbitrary $N$-legged transactions (`POST /transactions`) where money can be routed across multiple stakeholders in a single atomic transaction (e.g. Marketplace Checkout):
- Buyer debited ₹1,000
- Merchant credited ₹950
- Platform fee account credited ₹50
- Strictly validates that:
  $$\sum_{i=1}^n \text{Debits}_i = \sum_{j=1}^m \text{Credits}_j$$

### 🌐 5. Cross-Currency Foreign Exchange (FX Engine)
Transfers between accounts with different currencies (e.g. USD $\rightarrow$ INR) utilize the system counterparty account `FX_SETTLEMENT`:
- **Leg 1 (Send Currency):** Sender account debited \$100 USD; `FX_SETTLEMENT` credited \$100 USD.
- **Leg 2 (Receive Currency):** `FX_SETTLEMENT` debited ₹8,300 INR; Receiver account credited ₹8,300 INR.
- Verifies that within each individual currency, total debits strictly equal total credits.

### 🔒 6. Deterministic Deadlock Prevention
When transferring funds between accounts, concurrent transfers in opposite directions ($A \rightarrow B$ and $B \rightarrow A$) can cause PostgreSQL deadlocks. This platform prevents deadlocks by sorting all unique account IDs lexicographically and acquiring row-level locks in deterministic order:
```go
ids := []uuid.UUID{from, to}
sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
rows, err := tx.Query(ctx, `SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
```

### ⚡ 7. Idempotency with Request Hashing
Clients provide an `Idempotency-Key` header with mutation requests. The incoming payload is hashed with SHA-256 and claimed atomically via:
```sql
INSERT INTO idempotency_keys (key, request_hash) VALUES ($1, $2)
ON CONFLICT (key) DO NOTHING;
```
- **Same Key + Same Body:** Replays the original response with HTTP 200 and header `Idempotent-Replay: true`.
- **Same Key + Different Body:** Rejects with HTTP 422 (`ErrKeyReused`).
- **Concurrent In-Flight:** Blocks on the primary key lock until the first transaction commits, then discovers the conflict and safely replays.

### 📊 8. Prometheus Observability
Exposes real-time metrics on `/metrics`:
- `ledger_transactions_total{kind, status}`: Transaction counters.
- `ledger_transaction_duration_seconds`: High-resolution latency histograms.
- `ledger_active_holds_count`: Live gauge of outstanding authorization holds.
- `ledger_reconciliation_status`: Real-time ledger health status (1 = healthy, 0 = discrepancy).

---

## 3. Database Schema

```sql
CREATE TABLE accounts (
    id         UUID PRIMARY KEY,
    owner      TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('customer','system')),
    currency   CHAR(3) NOT NULL DEFAULT 'INR',
    balance    BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT no_negative CHECK (type = 'system' OR balance >= 0)
);

CREATE TABLE transactions (
    id             UUID PRIMARY KEY,
    kind           TEXT NOT NULL CHECK (kind IN ('deposit','withdrawal','transfer','reversal','split_payment','fx_transfer','hold_capture')),
    reversal_of_id UUID REFERENCES transactions(id),
    metadata       JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE entries (
    id             BIGSERIAL PRIMARY KEY,
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    account_id     UUID NOT NULL REFERENCES accounts(id),
    direction      TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount         BIGINT NOT NULL CHECK (amount > 0),
    prev_hash      TEXT NOT NULL DEFAULT '',
    entry_hash     TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_entries_account ON entries(account_id, id DESC);

CREATE TABLE holds (
    id          UUID PRIMARY KEY,
    account_id  UUID NOT NULL REFERENCES accounts(id),
    amount      BIGINT NOT NULL CHECK (amount > 0),
    status      TEXT NOT NULL CHECK (status IN ('active', 'captured', 'voided')),
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    captured_at TIMESTAMPTZ,
    voided_at   TIMESTAMPTZ
);
CREATE INDEX idx_holds_account_active ON holds(account_id) WHERE status = 'active';

CREATE TABLE idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    transaction_id  UUID REFERENCES transactions(id),
    response_status INT,
    response_body   JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

---

## 4. REST API Reference

| Method | Endpoint | Headers | Description |
|---|---|---|---|
| `GET` | `/healthz` | - | Liveness and DB healthcheck |
| `GET` | `/metrics` | - | Prometheus metrics endpoint |
| `POST` | `/accounts` | - | Create account (`owner`, `currency`) |
| `GET` | `/accounts/{id}` | - | Fetch account balance & available balance |
| `GET` | `/accounts/{id}/entries` | - | Account statement with cursor pagination (`limit`, `before`) |
| `POST` | `/accounts/{id}/deposits` | `Idempotency-Key` | Deposit cash |
| `POST` | `/accounts/{id}/withdrawals` | `Idempotency-Key` | Withdraw cash |
| `POST` | `/transfers` | `Idempotency-Key` | Peer-to-peer transfer (`from`, `to`, `amount`) |
| `POST` | `/transfers/fx` | `Idempotency-Key` | Cross-currency FX transfer |
| `POST` | `/transfers/{id}/reversals` | `Idempotency-Key` | Create compensating transaction reversal |
| `POST` | `/transactions` | `Idempotency-Key` | Multi-party split payments (arbitrary $N$ legs) |
| `POST` | `/accounts/{id}/holds` | - | Authorize and reserve funds |
| `POST` | `/holds/{id}/capture` | `Idempotency-Key` | Capture/settle authorized hold |
| `POST` | `/holds/{id}/void` | - | Cancel/void authorized hold |
| `GET` | `/admin/reconcile` | - | Global $\sum \text{debits} = \sum \text{credits}$ verification |
| `GET` | `/admin/verify-audit-chain`| - | Cryptographically verify SHA-256 hash chains |

---

## 5. Quickstart & Verification Walkthrough

### 1. Launch Services
```bash
docker compose up -d --build
```

### 2. End-to-End Walkthrough via cURL

```bash
# 1. Create Buyer and Merchant accounts
BUYER=$(curl -s -X POST http://localhost:8080/accounts \
  -H "Content-Type: application/json" \
  -d '{"owner":"Alice","currency":"INR"}' | jq -r .id)

MERCHANT=$(curl -s -X POST http://localhost:8080/accounts \
  -H "Content-Type: application/json" \
  -d '{"owner":"Bookstore","currency":"INR"}' | jq -r .id)

FEE=$(curl -s -X POST http://localhost:8080/accounts \
  -H "Content-Type: application/json" \
  -d '{"owner":"PlatformFee","currency":"INR"}' | jq -r .id)

# 2. Deposit ₹1,000 (100,000 paise)
curl -s -X POST "http://localhost:8080/accounts/$BUYER/deposits" \
  -H "Idempotency-Key: dep-101" \
  -H "Content-Type: application/json" \
  -d '{"amount": 100000}' | jq .

# 3. Two-Phase Authorization: Place a Hold for ₹500 (50,000 paise)
HOLD_ID=$(curl -s -X POST "http://localhost:8080/accounts/$BUYER/holds" \
  -H "Content-Type: application/json" \
  -d '{"amount": 50000, "description": "Checkout Authorization"}' | jq -r .id)

# 4. Check Available Balance (Total: 100,000; Available: 50,000)
curl -s "http://localhost:8080/accounts/$BUYER" | jq .

# 5. Capture the Hold for ₹450 (settlement)
curl -s -X POST "http://localhost:8080/holds/$HOLD_ID/capture" \
  -H "Idempotency-Key: cap-101" \
  -H "Content-Type: application/json" \
  -d '{"amount": 45000}' | jq .

# 6. Execute a Split Payment
curl -s -X POST http://localhost:8080/transactions \
  -H "Idempotency-Key: split-101" \
  -H "Content-Type: application/json" \
  -d "{
    \"kind\": \"split_payment\",
    \"legs\": [
      {\"account_id\": \"$BUYER\", \"direction\": \"debit\", \"amount\": 20000},
      {\"account_id\": \"$MERCHANT\", \"direction\": \"credit\", \"amount\": 19000},
      {\"account_id\": \"$FEE\", \"direction\": \"credit\", \"amount\": 1000}
    ]
  }" | jq .

# 7. Verify Cryptographic Audit Trail
curl -s http://localhost:8080/admin/verify-audit-chain | jq .

# 8. Reconcile Balance Invariants
curl -s http://localhost:8080/admin/reconcile | jq .

# 9. Scrape Prometheus Metrics
curl -s http://localhost:8080/metrics | grep "ledger_"
```

---

## 6. Concurrency & Invariants Testing

```bash
# Run unit and API tests
go test -v ./...

# Run PostgreSQL concurrency tests with Race Detector
go test -v -race ./...
```

The test suite systematically verifies:
- **Deadlock Immunity:** 100 goroutines executing cross transfers concurrently.
- **Idempotency Race:** 20 identical requests hitting the API at the exact same instant; exactly 1 executes, 19 replay cleanly.
- **Overdraft Immunity:** 50 concurrent withdrawal attempts against a balance of 1,000; exactly 10 succeed, 40 fail with `ErrInsufficientFunds`.
- **Conservation of Value:** $\sum \text{Debits} = \sum \text{Credits}$ verified after all concurrency runs.

---

## License

MIT
