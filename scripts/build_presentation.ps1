param()

$baseDir = "d:\Coding\Mini banking ledger API"
$screenshotsDir = Join-Path $baseDir "docs\screenshots"
$htmlPath = Join-Path $baseDir "docs\presentation.html"
$pdfPath = Join-Path $baseDir "docs\ApexLedger_Architecture_Presentation.pdf"

Write-Host "Encoding screenshots to Base64..."
function Get-B64($fileName) {
    $p = Join-Path $screenshotsDir $fileName
    if (Test-Path $p) {
        $bytes = [IO.File]::ReadAllBytes($p)
        return "data:image/png;base64," + [Convert]::ToBase64String($bytes)
    }
    return ""
}

$b64Full = Get-B64 "01_full_dashboard.png"
$b64Modal = Get-B64 "03_t_account_inspector_modal.png"
$b64Stress = Get-B64 "05_stress_simulator_tab.png"
$b64Drawer = Get-B64 "06_selection_drawer.png"

$html = @"
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <title>ApexLedger &bull; System Architecture Presentation</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@300;400;500;600;700;800&family=Playfair+Display:ital,wght@0,400;0,600;0,700;1,400&family=JetBrains+Mono:wght@400;500;700&display=swap" rel="stylesheet">
  <style>
    @page {
      size: 1920px 1080px;
      margin: 0;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: 'Plus Jakarta Sans', -apple-system, sans-serif;
      background: #0B1024;
      color: #FFFFFF;
      margin: 0;
      padding: 0;
      -webkit-print-color-adjust: exact;
      print-color-adjust: exact;
    }
    .slide {
      width: 1920px;
      height: 1080px;
      page-break-after: always;
      page-break-inside: avoid;
      padding: 70px 90px;
      position: relative;
      overflow: hidden;
      background: radial-gradient(circle at 85% 20%, #1F2B5E 0%, #0B1024 70%);
      display: flex;
      flex-direction: column;
      justify-content: space-between;
    }
    .slide::before {
      content: "";
      position: absolute;
      top: 0; left: 0; right: 0;
      height: 5px;
      background: linear-gradient(90deg, #D4AF37, #F6E6B4, #D4AF37);
    }
    .slide-header {
      display: flex;
      justify-content: space-between;
      align-items: flex-start;
      margin-bottom: 24px;
    }
    .eyebrow {
      font-size: 13px;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 3px;
      color: #D4AF37;
      margin-bottom: 8px;
      display: flex;
      align-items: center;
      gap: 10px;
    }
    .eyebrow::before {
      content: "";
      display: inline-block;
      width: 24px;
      height: 2px;
      background: #D4AF37;
    }
    .slide-title {
      font-family: 'Playfair Display', Georgia, serif;
      font-size: 44px;
      font-weight: 600;
      line-height: 1.15;
      color: #FFFFFF;
    }
    .slide-title em {
      font-style: italic;
      color: #F6E6B4;
    }
    .badge-counter {
      background: rgba(255,255,255,0.08);
      border: 1px solid rgba(212, 175, 55, 0.4);
      padding: 8px 18px;
      border-radius: 9999px;
      font-size: 13px;
      font-weight: 700;
      color: #D4AF37;
      font-family: 'JetBrains Mono', monospace;
    }
    .slide-body {
      flex: 1;
      display: flex;
      flex-direction: column;
      justify-content: center;
      margin-bottom: 20px;
    }
    .slide-footer {
      display: flex;
      justify-content: space-between;
      align-items: center;
      border-top: 1px solid rgba(255,255,255,0.12);
      padding-top: 20px;
      font-size: 13px;
      color: #94A3B8;
      text-transform: uppercase;
      letter-spacing: 1.5px;
    }
    .slide-footer .author { color: #F6E6B4; font-weight: 700; }
    .grid-2 {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 40px;
      align-items: center;
    }
    .grid-3 {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      gap: 30px;
    }
    .grid-4 {
      display: grid;
      grid-template-columns: repeat(4, 1fr);
      gap: 24px;
    }
    .card {
      background: rgba(255, 255, 255, 0.04);
      border: 1px solid rgba(255, 255, 255, 0.12);
      border-radius: 18px;
      padding: 28px;
      position: relative;
    }
    .card.highlight {
      border-color: #D4AF37;
      background: rgba(212, 175, 55, 0.05);
    }
    .card-num {
      font-family: 'Playfair Display', serif;
      font-size: 28px;
      color: #D4AF37;
      margin-bottom: 12px;
    }
    .card-h {
      font-size: 20px;
      font-weight: 700;
      color: #FFFFFF;
      margin-bottom: 10px;
    }
    .card-p {
      font-size: 14px;
      line-height: 1.6;
      color: #CBD5E1;
    }
    .code-snippet {
      background: #060914;
      border: 1px solid rgba(255,255,255,0.12);
      border-radius: 12px;
      padding: 18px 22px;
      font-family: 'JetBrains Mono', monospace;
      font-size: 13px;
      line-height: 1.6;
      color: #E2E8F0;
      white-space: pre;
    }
    .tag {
      display: inline-block;
      padding: 4px 10px;
      border-radius: 6px;
      font-size: 11px;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 1px;
    }
    .tag-gold { background: rgba(212, 175, 55, 0.2); color: #F6E6B4; border: 1px solid #D4AF37; }
    .tag-green { background: rgba(5, 150, 105, 0.2); color: #34D399; border: 1px solid #059669; }
    .tag-red { background: rgba(220, 38, 38, 0.2); color: #F87171; border: 1px solid #DC2626; }
    .preview-img {
      width: 100%;
      height: 480px;
      object-fit: cover;
      border-radius: 16px;
      border: 1px solid rgba(212, 175, 55, 0.4);
      box-shadow: 0 20px 40px rgba(0,0,0,0.6);
    }
  </style>
</head>
<body>

  <!-- SLIDE 1: COVER SLIDE -->
  <div class="slide">
    <div style="margin-top: 40px;">
      <div class="eyebrow">DISTRIBUTED SYSTEMS & FINTECH CASE STUDY</div>
      <h1 style="font-family: 'Playfair Display', Georgia, serif; font-size: 68px; font-weight: 400; line-height: 1.1; margin: 24px 0; max-width: 1400px;">
        Architecting an <em>Institutional-Grade</em> Double-Entry Banking Ledger
      </h1>
      <p style="font-size: 24px; color: #CBD5E1; max-width: 1100px; line-height: 1.6; font-weight: 300;">
        Why eventual consistency fails for money, and how we engineered mathematical correctness, deterministic deadlock-free locking, and cryptographic auditability in Go & PostgreSQL.
      </p>
    </div>

    <div style="display: flex; gap: 20px; margin: 30px 0;">
      <span class="tag tag-gold" style="font-size: 13px; padding: 8px 16px;">Go (Golang)</span>
      <span class="tag tag-gold" style="font-size: 13px; padding: 8px 16px;">PostgreSQL</span>
      <span class="tag tag-gold" style="font-size: 13px; padding: 8px 16px;">ACID Transactions</span>
      <span class="tag tag-gold" style="font-size: 13px; padding: 8px 16px;">Deadlock Prevention</span>
      <span class="tag tag-gold" style="font-size: 13px; padding: 8px 16px;">SHA-256 Audit Trail</span>
    </div>

    <div class="slide-footer">
      <div>ApexLedger &bull; Production Architecture</div>
      <div class="author">Presented by Sahil Kumar &bull; Software Engineer</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 2: THE PROBLEM -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">THE ENGINEERING PROBLEM</div>
        <h2 class="slide-title">Why Naive Financial Ledgers <em>Fail Catastrophically</em></h2>
      </div>
      <div class="badge-counter">02 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-4">
        <div class="card">
          <div class="card-num">01</div>
          <div class="card-h">IEEE-754 Float Traps</div>
          <div class="card-p">Using <code style="color:#F87171;">float64</code> causes binary rounding errors (<code style="color:#F87171;">0.1 + 0.2 = 0.30000000000000004</code>). Over millions of transactions, financial balance drifts and money vanishes into thin air.</div>
        </div>
        <div class="card">
          <div class="card-num">02</div>
          <div class="card-h">Deadlock Race Conditions</div>
          <div class="card-p">When User A transfers to B at the exact same millisecond that B transfers to A, random row lock acquisition causes database deadlocks and aborted transactions under high concurrency.</div>
        </div>
        <div class="card">
          <div class="card-num">03</div>
          <div class="card-h">Network Replay Double-Spend</div>
          <div class="card-p">Mobile apps and payment gateways retry timed-out requests. Without SHA-256 payload-aware idempotency, network retries double-debit customer accounts.</div>
        </div>
        <div class="card">
          <div class="card-num">04</div>
          <div class="card-h">Retroactive DB Tampering</div>
          <div class="card-p">Standard mutable databases allow rogue queries to alter historical transaction amounts without leaving a mathematical or cryptographic signature.</div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>ApexLedger &bull; Problem Definition</div>
      <div>Zero-Drift &bull; Zero-Deadlocks &bull; Zero-Loss</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 3: PILLAR 1 -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">PILLAR 01: ACCOUNTING INVARIANT</div>
        <h2 class="slide-title">Strict Double-Entry: <em>&Sigma; Debits &equiv; &Sigma; Credits</em></h2>
      </div>
      <div class="badge-counter">03 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div>
          <div class="card highlight" style="margin-bottom: 24px;">
            <div class="card-h" style="color:#F6E6B4;">The Universal Law of Value Conservation</div>
            <div class="card-p" style="font-size: 16px;">
              Money cannot be created or destroyed. In ApexLedger, every single transaction strictly satisfies:
            </div>
            <div style="font-family:'JetBrains Mono',monospace; font-size: 26px; color:#34D399; font-weight:700; margin: 16px 0;">
              &Sigma; Debit Legs &minus; &Sigma; Credit Legs &equiv; 0
            </div>
            <div class="card-p">
              Deposits and withdrawals do not create value from thin air; they transact against a designated counterparty: <strong>System CASH (00000000-0000-0000-0000-000000000001)</strong>.
            </div>
          </div>
          <div class="card">
            <div class="card-h">Dual Safety Net</div>
            <div class="card-p">
              Enforced at two independent layers: (1) Go Service Business Logic, and (2) PostgreSQL database <code style="color:#34D399;">CHECK (type = 'system' OR balance >= 0)</code> constraint.
            </div>
          </div>
        </div>

        <div>
          <div class="code-snippet">
<span style="color:#94A3B8;">// 1. Validate Double-Entry Balance</span>
var totalDebit, totalCredit int64
for _, leg := range legs {
    if leg.Direction == domain.DirectionDebit {
        totalDebit += leg.Amount
    } else {
        totalCredit += leg.Amount
    }
}
if totalDebit != totalCredit {
    return domain.ErrDoubleEntryImbalance <span style="color:#F87171;">// Strict Reject</span>
}

<span style="color:#94A3B8;">// 2. Deposit Execution (Counterparty CASH)</span>
legs := []domain.Leg{
    {AccountID: CASH_VAULT, Direction: "debit", Amount: amt},
    {AccountID: customerID, Direction: "credit", Amount: amt},
}
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Pillar 1: Double-Entry Correctness</div>
      <div>Balanced Bookkeeping &bull; System CASH Counterparty</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 4: PILLAR 2 -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">PILLAR 02: NUMERICAL PRECISION</div>
        <h2 class="slide-title">Minor-Unit Integer Arithmetic: <em>Never Use Float64</em></h2>
      </div>
      <div class="badge-counter">04 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div>
          <div class="card" style="margin-bottom: 24px;">
            <div class="card-h" style="color:#F87171;">The Dangerous IEEE-754 Floating-Point Flaw</div>
            <div class="card-p" style="font-size: 15px;">
              In binary floating-point representation, base-10 fractions (like 0.10 or 0.70) cannot be represented cleanly. Over recurring interest calculations, tax deductions, or batch splits, microscopic rounding errors accumulate into massive financial discrepancies.
            </div>
          </div>
          <div class="card highlight">
            <div class="card-h" style="color:#34D399;">The ApexLedger Standard: Signed Int64 Minor Units</div>
            <div class="card-p" style="font-size: 15px;">
              All currency in ApexLedger is represented strictly in atomic minor units:
              <br><br>
              &bull; <strong>Indian Rupee (INR):</strong> Stored as <em>Paise</em> (&times; 100)<br>
              &bull; <strong>US Dollar (USD):</strong> Stored as <em>Cents</em> (&times; 100)<br>
              &bull; <strong>Conversion Rule:</strong> Division by 100 happens strictly at the visual UI / API output boundary.
            </div>
          </div>
        </div>

        <div>
          <div class="code-snippet">
<span style="color:#94A3B8;">// PostgreSQL Schema: BIGINT Minor Units</span>
CREATE TABLE accounts (
    id         UUID PRIMARY KEY,
    owner      TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN ('customer','system')),
    currency   CHAR(3) NOT NULL DEFAULT 'INR',
    balance    BIGINT NOT NULL DEFAULT 0, <span style="color:#34D399;">-- Int64 Paise</span>
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT no_negative CHECK (type = 'system' OR balance >= 0)
);

CREATE TABLE entries (
    id             BIGSERIAL PRIMARY KEY,
    transaction_id UUID NOT NULL REFERENCES transactions(id),
    account_id     UUID NOT NULL REFERENCES accounts(id),
    direction      TEXT NOT NULL CHECK (direction IN ('debit','credit')),
    amount         BIGINT NOT NULL CHECK (amount > 0), <span style="color:#34D399;">-- Strict Positive</span>
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Pillar 2: Numerical Correctness</div>
      <div>64-Bit Integer Precision &bull; Zero IEEE-754 Drift</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 5: PILLAR 3 -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">PILLAR 03: CONCURRENCY & DEADLOCK FREEDOM</div>
        <h2 class="slide-title">Deterministic Row-Locking: <em>Eliminating Deadlocks</em></h2>
      </div>
      <div class="badge-counter">05 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div>
          <div class="card" style="margin-bottom: 20px;">
            <div class="card-h" style="color:#F87171;">The Classic Circular Wait Trap</div>
            <div class="card-p">
              If Goroutine 1 locks Account A and waits for B, while Goroutine 2 locks B and waits for A, PostgreSQL encounters an unresolved dependency cycle and forcibly aborts with a Deadlock Error.
            </div>
          </div>
          <div class="card highlight">
            <div class="card-h" style="color:#F6E6B4;">ApexLedger's Deterministic Total Order Lock</div>
            <div class="card-p">
              Before acquiring row locks, ApexLedger sorts all participating account UUIDs in strict lexicographical order:
              <br><br>
              <code style="color:#34D399; font-weight:700;">min(UUID_A, UUID_B) &rarr; max(UUID_A, UUID_B)</code>
              <br><br>
              Because every concurrent transaction acquires locks in the exact same sequence, <strong>circular wait conditions are mathematically impossible</strong>.
            </div>
          </div>
        </div>

        <div>
          <div class="code-snippet">
<span style="color:#94A3B8;">// 1. Sort Account IDs deterministically to prevent deadlocks</span>
ids := []uuid.UUID{from, to}
sort.Slice(ids, func(i, j int) bool {
    return ids[i].String() < ids[j].String()
})

<span style="color:#94A3B8;">// 2. Acquire Pessimistic Row Locks in strict order</span>
rows, err := tx.Query(ctx,
    `SELECT id, balance 
     FROM accounts 
     WHERE id = ANY($1) 
     ORDER BY id FOR UPDATE`, ids)
if err != nil {
    return nil, err
}
defer rows.Close()
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Pillar 3: High-Concurrency Engineering</div>
      <div>Deterministic Row Locking &bull; Zero Deadlocks</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 6: PILLAR 4 -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">PILLAR 04: CRYPTOGRAPHIC INTEGRITY</div>
        <h2 class="slide-title">Cryptographic Hash Chaining: <em>Tamper-Proof Audit Trail</em></h2>
      </div>
      <div class="badge-counter">06 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div>
          <div class="card highlight" style="margin-bottom: 24px;">
            <div class="card-h" style="color:#F6E6B4;">Mathematical Provability Over Blind Trust</div>
            <div class="card-p" style="font-size: 15px;">
              Every journal entry is cryptographically bound to its parent entry using a SHA-256 hash chain:
            </div>
            <div style="font-family:'JetBrains Mono',monospace; font-size: 15px; color:#34D399; background:#060914; padding:12px; border-radius:8px; margin: 16px 0;">
              entry_hash = SHA256(prev_hash + account_id + direction + amount)
            </div>
            <div class="card-p">
              If an attacker with database access alters historical amounts or inserts ghost records, the hash chain immediately breaks, triggering an automated reconciliation alarm.
            </div>
          </div>
          <div class="card">
            <div class="card-h">Automated Continuous Auditing</div>
            <div class="card-p">
              The platform exposes an automated audit verification endpoint (<code style="color:#34D399;">GET /admin/verify-audit-chain</code>) that recalculates and validates the entire cryptographic chain on demand.
            </div>
          </div>
        </div>

        <div>
          <div class="code-snippet">
<span style="color:#94A3B8;">// Cryptographic SHA-256 Hash Chaining Function</span>
func computeEntryHash(prevHash string, accID uuid.UUID, dir string, amt int64) string {
    record := fmt.Sprintf("%s:%s:%s:%d", prevHash, accID.String(), dir, amt)
    sum := sha256.Sum256([]byte(record))
    return hex.EncodeToString(sum[:])
}

<span style="color:#94A3B8;">// Audit Chain Verification</span>
func (s *Store) VerifyAuditChain(ctx context.Context) (*Report, error) {
    <span style="color:#94A3B8;">// Traverses all entries in order</span>
    <span style="color:#94A3B8;">// Recomputes expected SHA-256 hashes</span>
    <span style="color:#94A3B8;">// Flags any broken cryptographic link</span>
}
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Pillar 4: Cryptographic Auditing</div>
      <div>SHA-256 Merkle Chain &bull; Tamper-Proof Audit Trail</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 7: PILLAR 5 -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">PILLAR 05: NETWORK RESILIENCE</div>
        <h2 class="slide-title">Payload-Hashed Idempotency: <em>Safe Retries & Zero Double-Debits</em></h2>
      </div>
      <div class="badge-counter">07 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div>
          <div class="card" style="margin-bottom: 20px;">
            <div class="card-h" style="color:#F87171;">The Blind UUID Flaw</div>
            <div class="card-p">
              Most APIs only store an idempotency key string. If a malicious or buggy client sends the same key with a different amount or different recipient, naive systems replay the wrong data or cause state corruption.
            </div>
          </div>
          <div class="card highlight">
            <div class="card-h" style="color:#34D399;">The ApexLedger Solution: Payload Hashing</div>
            <div class="card-p">
              ApexLedger stores the key alongside the <strong>SHA-256 hash of the complete HTTP request body</strong>:
              <br><br>
              &bull; <strong>Exact Replay:</strong> Same key + Same body &rarr; Replays original response with <code style="color:#34D399;">Idempotent-Replay: true</code>.<br>
              &bull; <strong>Key Poisoning Rejection:</strong> Same key + Different body &rarr; Rejected with <code style="color:#F87171;">422 Unprocessable Entity</code>.<br>
              &bull; <strong>Concurrent Race:</strong> Second simultaneous request blocks on unique row lock, preventing race duplicates.
            </div>
          </div>
        </div>

        <div>
          <div class="code-snippet">
<span style="color:#94A3B8;">// Atomically claim idempotency key</span>
tag, err := tx.Exec(ctx,
    `INSERT INTO idempotency_keys (key, request_hash)
     VALUES ($1, $2)
     ON CONFLICT (key) DO NOTHING`, key, reqHash)

if tag.RowsAffected() == 0 {
    <span style="color:#94A3B8;">// Key already claimed: Check stored hash</span>
    if storedHash != reqHash {
        return nil, domain.ErrKeyReused <span style="color:#F87171;">// 422 Conflict</span>
    }
    if status == nil {
        return nil, domain.ErrInProgress <span style="color:#F87171;">// 409 In Flight</span>
    }
    return &Result{Replayed: true, Status: *status, Body: body}, nil
}
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Pillar 5: Idempotency Engineering</div>
      <div>Payload-Hashed Retries &bull; Zero Double-Debits</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 8: ADVANCED PRIMITIVES -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">ADVANCED FINANCIAL PRIMITIVES</div>
        <h2 class="slide-title">Two-Phase Holds &amp; <em>Multi-Party Split Settlements</em></h2>
      </div>
      <div class="badge-counter">08 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div class="card highlight">
          <div class="card-num">01</div>
          <div class="card-h" style="color:#F6E6B4;">Two-Phase Commit Pre-Auth Holds</div>
          <div class="card-p" style="font-size: 15px; margin-bottom: 16px;">
            In card processing and hospitality, funds must be reserved without moving settled ledger capital:
          </div>
          <div style="background:#060914; padding:14px; border-radius:10px; font-family:'JetBrains Mono',monospace; font-size:13px; color:#34D399; margin-bottom:16px;">
            Available Balance = Total Balance &minus; Active Holds
          </div>
          <div class="card-p">
            &bull; <strong>Create Hold:</strong> Ring-fences available balance immediately.<br>
            &bull; <strong>Capture Hold:</strong> Turns hold into settled debit transaction.<br>
            &bull; <strong>Void Hold:</strong> Cancels reservation; frees ring-fenced liquidity.
          </div>
        </div>

        <div class="card highlight">
          <div class="card-num">02</div>
          <div class="card-h" style="color:#F6E6B4;">Atomic Multi-Party Split Settlements</div>
          <div class="card-p" style="font-size: 15px; margin-bottom: 16px;">
            Modern marketplace checkouts require 1 buyer payment to disburse across multiple vendors atomically:
          </div>
          <div class="card-p" style="line-height: 1.8;">
            &bull; <strong>Buyer Account:</strong> Debited &minus;&#8377;1,000.00<br>
            &bull; <strong>Merchant Account:</strong> Credited +&#8377;950.00 (95% Net Revenue)<br>
            &bull; <strong>Platform Fee Escrow:</strong> Credited +&#8377;50.00 (5% Platform Fee)<br>
            <span style="color:#34D399; font-weight:700;">&rArr; Net Transaction Imbalance &equiv; 0.00</span>
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Advanced Ledger Primitives</div>
      <div>Two-Phase Commit Holds &bull; Marketplace Split Settlements</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 9: LIVE STUDIO & VERIFICATION -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">LIVE VERIFICATION & OBSERVABILITY</div>
        <h2 class="slide-title">Interactive Studio &amp; <em>High-Concurrency Race Simulator</em></h2>
      </div>
      <div class="badge-counter">09 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-2">
        <div>
          <img src="$b64Full" class="preview-img" alt="ApexLedger Full Dashboard">
        </div>
        <div>
          <div class="card highlight" style="margin-bottom: 20px;">
            <div class="card-h" style="color:#F6E6B4;">Live Stress Simulator Results</div>
            <div class="card-p" style="font-size: 15px;">
              &bull; <strong>10 Simultaneous Goroutines:</strong> Alternating cross-transfers between Alice and Bob.<br>
              &bull; <strong>Zero Deadlocks:</strong> Deterministic row lock order eliminates contention.<br>
              &bull; <strong>Zero Drift:</strong> Automated global reconciliation proves debits strictly equal credits.<br>
              &bull; <strong>Visual T-Account Inspector:</strong> Click any journal entry to inspect balanced legs, SHA-256 hashes, and trigger 1-click reversals.
            </div>
          </div>
          <div class="card">
            <div class="card-h">Full Observability Stack</div>
            <div class="card-p">
              Prometheus metrics exposed at <code style="color:#34D399;">/metrics</code> tracking transaction duration histograms, total TPS counters, active holds, and structured logs via Go's <code style="color:#34D399;">log/slog</code>.
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>Live Verification Studio</div>
      <div>Deterministic Stress Testing &bull; Visual T-Account Inspection</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

  <!-- SLIDE 10: CONCLUSION & REPO -->
  <div class="slide">
    <div class="slide-header">
      <div>
        <div class="eyebrow">PROJECT SUMMARY & OPEN SOURCE</div>
        <h2 class="slide-title">Production Architecture &amp; <em>Key Takeaways</em></h2>
      </div>
      <div class="badge-counter">10 / 10</div>
    </div>

    <div class="slide-body">
      <div class="grid-3" style="margin-bottom: 30px;">
        <div class="card">
          <div class="card-h" style="color:#D4AF37;">Mathematical Guarantees</div>
          <div class="card-p">
            Financial software cannot rely on eventual consistency. Using strict double-entry and minor-unit int64 arithmetic ensures 100% balance preservation.
          </div>
        </div>
        <div class="card">
          <div class="card-h" style="color:#D4AF37;">Deterministic Concurrency</div>
          <div class="card-p">
            Sorting account UUIDs before issuing <code style="color:#34D399;">FOR UPDATE</code> locks eliminates circular wait deadlocks under high multi-threaded traffic.
          </div>
        </div>
        <div class="card">
          <div class="card-h" style="color:#D4AF37;">Immutable Cryptographic Seal</div>
          <div class="card-p">
            Append-only journal entries with SHA-256 block hash chaining turn the database into an immutable, tamper-evident audit record.
          </div>
        </div>
      </div>

      <div class="card highlight" style="text-align: center; padding: 36px;">
        <div style="font-family:'Playfair Display',serif; font-size:32px; color:#FFFFFF; margin-bottom: 12px;">
          Explore the Full Open-Source Repository
        </div>
        <div style="font-family:'JetBrains Mono',monospace; font-size:18px; color:#F6E6B4; margin-bottom: 18px;">
          https://github.com/silentguyracer/Mini-banking-ledger-API-
        </div>
        <div style="font-size: 15px; color: #CBD5E1;">
          Engineered by <strong>Sahil Kumar</strong> &bull; Connect with me on LinkedIn for distributed systems & backend architecture discussions!
        </div>
      </div>
    </div>

    <div class="slide-footer">
      <div>ApexLedger &bull; Production Banking Ledger</div>
      <div class="author">Sahil Kumar &bull; Software Engineer</div>
      <div>github.com/silentguyracer/Mini-banking-ledger-API-</div>
    </div>
  </div>

</body>
</html>
"@

Set-Content -Path $htmlPath -Value $html -Encoding utf8
Write-Host "Rendering PDF with headless browser (1920x1080 Widescreen)..."
$htmlUri = "file:///" + ($htmlPath.Replace("\", "/"))
Start-Process -FilePath "C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe" `
  -ArgumentList "--headless=new", "--disable-gpu", "--no-pdf-header-footer", "--virtual-time-budget=4000", "--print-to-pdf=""$pdfPath""", "$htmlUri" -Wait

if (Test-Path $pdfPath) {
    $item = Get-Item $pdfPath
    Write-Host "SUCCESS! PDF Presentation generated:"
    Write-Host "File: $($item.FullName)"
    Write-Host "Size: $([math]::Round($item.Length / 1KB, 2)) KB"
} else {
    Write-Error "Failed to generate PDF."
}
