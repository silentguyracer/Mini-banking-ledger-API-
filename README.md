# Mini Banking Ledger API

A production-grade, double-entry banking ledger API written in **Go 1.24** and **PostgreSQL 16**. Built for strict transactional correctness, deterministic concurrency control, idempotent request replay, and complete auditability.

---

## Architecture Overview

```
                      +-----------------------------+
                      |   Client / HTTP Request     |
                      +--------------+--------------+
                                     |
                                     v
                      +-----------------------------+
                      |   Chi Router & Middleware   |
                      |   (ReqID, RealIP, Slog)     |
                      +--------------+--------------+
                                     |
                                     v
                      +-----------------------------+
                      |      Ledger Service         |
                      |   (Validations, SHA-256)    |
                      +--------------+--------------+
                                     |
                                     v
                      +-----------------------------+
                      |      PostgreSQL Store       |
                      | - Idempotency Claiming      |
                      | - Deterministic Row Locks   |
                      | - Double-Entry Engine       |
                      +--------------+--------------+
                                     |
                                     v
                      +-----------------------------+
                      |       PostgreSQL 16         |
                      | accounts | transactions     |
                      | entries  | idempotency_keys |
                      +-----------------------------+
```

---

## Key Design Decisions

### 1. PostgreSQL over Cassandra
- **The Problem with Cassandra:** Apache Cassandra is an AP (Available / Partition-tolerant) distributed NoSQL database designed for high-throughput writes. It lacks cross-row ACID transactions, strict multi-row locking primitives, and foreign key integrity. Enforcing double-entry invariants (where debits must strictly equal credits across different accounts) under concurrent conditions without distributed consensus or two-phase locking is exceedingly error-prone.
- **Why Postgres:** PostgreSQL provides ACID compliance at `Read Committed` and `Serializable` isolation levels, deterministic `SELECT ... FOR UPDATE` row-level locks, and table constraints (`CHECK`, `FOREIGN KEY`). This allows us to guarantee balance correctness, prevent race conditions, and guarantee that debits equal credits in a single atomic transaction.

### 2. Money as `int64` in Minor Units
- Floating-point representations (`float32`, `float64`) suffer from IEEE 754 precision issues (e.g. `0.1 + 0.2 = 0.30000000000000004`), leading to catastrophic rounding discrepancies in financial systems.
- All monetary amounts in this ledger are represented as **64-bit signed integers** in minor currency units (e.g., paise for INR, cents for USD). ₹100.50 is stored as `10050`.

### 3. Pure Double-Entry Accounting
- Every financial movement creates a balanced set of entries where **total debits equal total credits**:
  $$\sum \text{Debits} = \sum \text{Credits}$$
- For customer accounts, balance is derived as:
  $$\text{Balance} = \sum \text{Credits} - \sum \text{Debits}$$
- Deposits and withdrawals are not one-sided: they utilize a reserved system counterparty account named `CASH` (`00000000-0000-0000-0000-000000000001`).
  - **Deposit:** Customer is *credited*; `CASH` is *debited*.
  - **Withdrawal:** Customer is *debited*; `CASH` is *credited*.
  - **Transfer:** Sender is *debited*; Receiver is *credited*.

### 4. Non-Negative Balances Enforced at Two Layers
- **Application Layer:** Pre-flight balance checks inside the locked transaction.
- **Database Layer:** A database check constraint ensures customer accounts can never drop below zero:
  ```sql
  CONSTRAINT no_negative CHECK (type = 'system' OR balance >= 0)
  ```
  Only system counterparty accounts (such as `CASH`) are permitted to carry negative balances.

### 5. Append-Only Ledger Entries
- The `entries` table is strictly append-only. Rows are never mutated or deleted.
- If a correction or refund is required, it must be executed as a new compensating transaction.

### 6. Deterministic Lock Ordering (Deadlock Prevention)
- When transferring funds between Account A and Account B, two concurrent reverse transfers (A &rarr; B and B &rarr; A) could easily deadlock if locks are acquired arbitrarily.
- We eliminate deadlocks by sorting account UUIDs lexicographically and locking them in deterministic order:
  ```go
  ids := []uuid.UUID{from, to}
  sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
  rows, err := tx.Query(ctx,
      `SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
  ```

### 7. Idempotency with Request Hashing
- Clients supply an `Idempotency-Key` header with mutation requests.
- The service hashes the incoming request body with SHA-256 and attempts to claim the key atomically:
  ```sql
  INSERT INTO idempotency_keys (key, request_hash) VALUES ($1, $2)
  ON CONFLICT (key) DO NOTHING;
  ```
- **Same key + same body:** The transaction returns the cached response with an `Idempotent-Replay: true` header (HTTP 200).
- **Same key + different body:** Rejected with HTTP 422 (`ErrKeyReused`).
- **Concurrent identical requests:** In PostgreSQL, the second `INSERT` blocks on the primary key index row lock until the first transaction commits, then discovers the conflict and safely replays the response.

---

## Project Structure

```
ledger/
├── cmd/
│   └── api/
│       └── main.go                 # App entrypoint, graceful shutdown, DB pool setup
├── internal/
│   ├── config/
│   │   └── config.go               # Environment configuration (PORT, DB_URL)
│   ├── domain/
│   │   └── domain.go               # Core domain entities, models, and domain errors
│   ├── store/
│   │   └── postgres.go             # PostgreSQL store, double-entry engine, row locking
│   ├── service/
│   │   ├── ledger.go               # Business services, validation, request hashing
│   │   ├── ledger_unit_test.go     # Table-driven unit tests (runs without DB)
│   │   └── ledger_test.go          # Concurrent integration tests (transfers, race conditions)
│   └── httpapi/
│       ├── router.go               # Chi router setup
│       ├── handlers.go             # HTTP handlers & REST response mapping
│       ├── handlers_test.go        # HTTP endpoints & status code tests
│       └── middleware.go           # Structured logging (log/slog), Request ID
├── migrations/
│   ├── 001_init.sql                # DDL migration & system account seed
│   └── migrations.go               # Embedded SQL migration
├── .github/
│   └── workflows/
│       └── ci.yml                  # GitHub Actions CI (vet, test -race with PostgreSQL)
├── docker-compose.yml              # Container definitions (Postgres 16 + API)
├── Dockerfile                      # Multi-stage distroless production container
├── Makefile                        # Lifecycle shortcuts
├── go.mod
├── go.sum
└── README.md
```

---

## REST API Specification

| Method | Path | Headers | Description | Status Codes |
|---|---|---|---|---|
| `GET` | `/healthz` | - | Liveness and DB health probe | `200` |
| `POST` | `/accounts` | - | Create new customer account | `201`, `400` |
| `GET` | `/accounts/{id}` | - | Fetch account details and balance | `200`, `404` |
| `GET` | `/accounts/{id}/entries` | - | Statement entries (cursor pagination via `limit`, `before`) | `200`, `404` |
| `POST` | `/accounts/{id}/deposits` | `Idempotency-Key` | Deposit money (credited to account, debited to `CASH`) | `201`, `200`, `400`, `404`, `409`, `422` |
| `POST` | `/accounts/{id}/withdrawals` | `Idempotency-Key` | Withdraw money (debited from account, credited to `CASH`) | `201`, `200`, `400`, `404`, `409`, `422` |
| `POST` | `/transfers` | `Idempotency-Key` | Transfer money between two customer accounts | `201`, `200`, `400`, `404`, `409`, `422` |
| `GET` | `/admin/reconcile` | - | Reconciles global ledger invariants and account balances | `200`, `500` |

### HTTP Status Code Semantics
- **`201 Created`**: New transaction executed and committed successfully.
- **`200 OK`**: Replayed idempotent request (contains `Idempotent-Replay: true` header).
- **`400 Bad Request`**: Malformed JSON, missing idempotency key, invalid amount, transfer to self.
- **`404 Not Found`**: Target account ID not found.
- **`409 Conflict`**: Request currently in progress.
- **`422 Unprocessable Entity`**: Insufficient funds or idempotency key reused with mismatched body.
- **`500 Internal Server Error`**: Unexpected database failure or reconciliation discrepancy.

---

## Quickstart & Demo

### 1. Launch with Docker Compose

```bash
docker compose up -d --build
```

Verify service health:
```bash
curl -s http://localhost:8080/healthz
# {"status":"ok"}
```

### 2. Walkthrough: Create Accounts, Deposit & Transfer

```bash
# 1. Create Alice's account
ALICE=$(curl -s -X POST http://localhost:8080/accounts \
  -H "Content-Type: application/json" \
  -d '{"owner":"Alice","currency":"INR"}' | jq -r .id)
echo "Alice ID: $ALICE"

# 2. Create Bob's account
BOB=$(curl -s -X POST http://localhost:8080/accounts \
  -H "Content-Type: application/json" \
  -d '{"owner":"Bob","currency":"INR"}' | jq -r .id)
echo "Bob ID: $BOB"

# 3. Deposit 50,000 paise (₹500.00) into Alice's account
curl -i -X POST "http://localhost:8080/accounts/$ALICE/deposits" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: dep-alice-001" \
  -d '{"amount": 50000}'

# 4. Transfer 15,000 paise (₹150.00) from Alice to Bob
curl -i -X POST http://localhost:8080/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: tr-001" \
  -d "{\"from\": \"$ALICE\", \"to\": \"$BOB\", \"amount\": 15000}"

# 5. Idempotent Retry: Repeat the EXACT SAME transfer request
# Notice the response returns HTTP 200 with 'Idempotent-Replay: true' header
curl -i -X POST http://localhost:8080/transfers \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: tr-001" \
  -d "{\"from\": \"$ALICE\", \"to\": \"$BOB\", \"amount\": 15000}"

# 6. Check Alice's balance (should be 35,000)
curl -s "http://localhost:8080/accounts/$ALICE" | jq .

# 7. Check Bob's balance (should be 15,000)
curl -s "http://localhost:8080/accounts/$BOB" | jq .

# 8. Check Alice's statement entries
curl -s "http://localhost:8080/accounts/$ALICE/entries?limit=10" | jq .

# 9. Verify global ledger integrity and reconciliation
curl -s http://localhost:8080/admin/reconcile | jq .
```

---

## Testing & Concurrency Verification

### Running Tests

```bash
# Run unit and API tests
go test -v ./...

# Run with race detector
go test -v -race ./...
```

### Concurrency Test Scenarios (`internal/service/ledger_test.go`)
1. **`TestConcurrentTransfers`**:
   - Accounts A and B each start with 100,000.
   - 100 concurrent goroutines transfer 1,000 alternating directions ($A \rightarrow B$ and $B \rightarrow A$).
   - Verifies **no deadlocks**, **total balance conserved** ($A + B = 200,000$), no negative balances, and `balance == SUM(entries)`.
2. **`TestConcurrentIdempotencyRace`**:
   - 20 goroutines fire concurrently using the **identical idempotency key**.
   - Exactly **1 execution succeeds** (HTTP 201), the other 19 receive replayed responses (HTTP 200), and sender balance is deducted exactly once.
3. **`TestConcurrentOverdraftRace`**:
   - Account has a balance of 1,000.
   - 50 goroutines concurrently attempt to withdraw 100.
   - Exactly **10 succeed**, **40 fail** with `ErrInsufficientFunds`, and final balance is strictly 0.

---

## License

MIT
