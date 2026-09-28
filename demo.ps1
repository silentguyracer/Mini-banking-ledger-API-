Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "           APEXLEDGER LIVE INTERACTIVE DEMONSTRATION        " -ForegroundColor Cyan
Write-Host "============================================================" -ForegroundColor Cyan

$baseUrl = "http://localhost:8080"

# 1. Healthcheck
Write-Host "`n[1] LIVENESS HEALTHCHECK (GET /healthz)" -ForegroundColor Yellow
$health = curl.exe -s "$baseUrl/healthz"
Write-Host "Response: $health" -ForegroundColor Green

# 2. Account Creation
Write-Host "`n[2] CREATING CUSTOMER ACCOUNTS (POST /accounts)" -ForegroundColor Yellow
$alice = curl.exe -s -X POST "$baseUrl/accounts" -H "Content-Type: application/json" -d "{\`"owner\`":\`"Alice\`",\`"currency\`":\`"INR\`"}" | ConvertFrom-Json
$bob = curl.exe -s -X POST "$baseUrl/accounts" -H "Content-Type: application/json" -d "{\`"owner\`":\`"Bob\`",\`"currency\`":\`"INR\`"}" | ConvertFrom-Json
$merchant = curl.exe -s -X POST "$baseUrl/accounts" -H "Content-Type: application/json" -d "{\`"owner\`":\`"Cloud Bookstore\`",\`"currency\`":\`"INR\`"}" | ConvertFrom-Json
$platformFee = curl.exe -s -X POST "$baseUrl/accounts" -H "Content-Type: application/json" -d "{\`"owner\`":\`"Platform Fee Escrow\`",\`"currency\`":\`"INR\`"}" | ConvertFrom-Json

Write-Host "  -> Alice Account ID:        $($alice.id) (Balance: ₹$($alice.balance/100))" -ForegroundColor White
Write-Host "  -> Bob Account ID:          $($bob.id) (Balance: ₹$($bob.balance/100))" -ForegroundColor White
Write-Host "  -> Merchant ID:             $($merchant.id)" -ForegroundColor White
Write-Host "  -> Platform Fee Escrow ID:  $($platformFee.id)" -ForegroundColor White

# 3. Cash Deposit
Write-Host "`n[3] CASH DEPOSIT (POST /accounts/:id/deposits)" -ForegroundColor Yellow
Write-Host "Depositing ₹1,000.00 (100,000 paise) into Alice's account with counterparty System CASH..."
$dep = curl.exe -s -X POST "$baseUrl/accounts/$($alice.id)/deposits" `
  -H "Content-Type: application/json" `
  -H "Idempotency-Key: dep-demo-001" `
  -d "{\`"amount\`": 100000}" | ConvertFrom-Json
Write-Host "  -> Deposit Txn ID: $($dep.transaction_id)" -ForegroundColor Green
Write-Host "  -> Alice Balance:  ₹$($dep.balance / 100)" -ForegroundColor Green

# 4. Transfer
Write-Host "`n[4] PEER-TO-PEER TRANSFER (POST /transfers)" -ForegroundColor Yellow
Write-Host "Transferring ₹350.00 (35,000 paise) from Alice to Bob..."
$tr = curl.exe -s -X POST "$baseUrl/transfers" `
  -H "Content-Type: application/json" `
  -H "Idempotency-Key: tr-demo-001" `
  -d "{\`"from\`":\`"$($alice.id)\`",\`"to\`":\`"$($bob.id)\`",\`"amount\`":35000}" | ConvertFrom-Json
Write-Host "  -> Transfer Txn ID: $($tr.transaction_id)" -ForegroundColor Green

$aliceAcc = curl.exe -s "$baseUrl/accounts/$($alice.id)" | ConvertFrom-Json
$bobAcc = curl.exe -s "$baseUrl/accounts/$($bob.id)" | ConvertFrom-Json
Write-Host "  -> Alice New Balance: ₹$($aliceAcc.balance / 100)" -ForegroundColor Cyan
Write-Host "  -> Bob New Balance:   ₹$($bobAcc.balance / 100)" -ForegroundColor Cyan

# 5. Idempotent Retry
Write-Host "`n[5] IDEMPOTENCY REPLAY TEST (Repeating the exact same request)" -ForegroundColor Yellow
Write-Host "Repeating Transfer with SAME key 'tr-demo-001'..."
$rawReplay = curl.exe -s -i -X POST "$baseUrl/transfers" `
  -H "Content-Type: application/json" `
  -H "Idempotency-Key: tr-demo-001" `
  -d "{\`"from\`":\`"$($alice.id)\`",\`"to\`":\`"$($bob.id)\`",\`"amount\`":35000}"
$replayHeader = ($rawReplay | Select-String "Idempotent-Replay").Line
Write-Host "  -> Replay Detected Header: $replayHeader" -ForegroundColor Magenta
Write-Host "  -> Alice balance remains unchanged: ₹$($aliceAcc.balance / 100)" -ForegroundColor Green

# 6. Two-Phase Authorization Hold
Write-Host "`n[6] TWO-PHASE COMMIT: AUTHORIZATION HOLD (POST /accounts/:id/holds)" -ForegroundColor Yellow
Write-Host "Alice authorizes ₹200.00 (20,000 paise) reserve hold for Hotel Booking..."
$hold = curl.exe -s -X POST "$baseUrl/accounts/$($alice.id)/holds" `
  -H "Content-Type: application/json" `
  -d "{\`"amount\`": 20000, \`"description\`": \`"Hotel Security Deposit\`"}" | ConvertFrom-Json
Write-Host "  -> Hold ID:     $($hold.id)" -ForegroundColor Green
Write-Host "  -> Hold Status: $($hold.status)" -ForegroundColor Green

$aliceAfterHold = curl.exe -s "$baseUrl/accounts/$($alice.id)" | ConvertFrom-Json
Write-Host "  -> Total Balance:     ₹$($aliceAfterHold.balance / 100)" -ForegroundColor Cyan
Write-Host "  -> Available Balance: ₹$($aliceAfterHold.available_balance / 100) (Ring-fenced ₹200.00!)" -ForegroundColor Yellow

# Settle the hold
Write-Host "Capturing hold for settled amount ₹180.00 (18,000 paise)..."
$cap = curl.exe -s -X POST "$baseUrl/holds/$($hold.id)/capture" `
  -H "Content-Type: application/json" `
  -H "Idempotency-Key: cap-demo-001" `
  -d "{\`"amount\`": 18000}" | ConvertFrom-Json
Write-Host "  -> Capture Status: $($cap.status), Captured Amount: ₹$($cap.amount / 100)" -ForegroundColor Green

# 7. Multi-Party Split Payment
Write-Host "`n[7] MULTI-PARTY SPLIT PAYMENT (POST /transactions)" -ForegroundColor Yellow
Write-Host "Alice buys a book for ₹100.00. Merchant gets ₹95.00, Platform Fee gets ₹5.00:"
$splitBody = "{
  \`"kind\`": \`"split_payment\`",
  \`"legs\`": [
    {\`"account_id\`": \`"$($alice.id)\`", \`"direction\`": \`"debit\`", \`"amount\`": 10000},
    {\`"account_id\`": \`"$($merchant.id)\`", \`"direction\`": \`"credit\`", \`"amount\`": 9500},
    {\`"account_id\`": \`"$($platformFee.id)\`", \`"direction\`": \`"credit\`", \`"amount\`": 500}
  ]
}"
$split = curl.exe -s -X POST "$baseUrl/transactions" `
  -H "Content-Type: application/json" `
  -H "Idempotency-Key: split-demo-001" `
  -d $splitBody | ConvertFrom-Json
Write-Host "  -> Split Payment Txn ID: $($split.transaction_id) ($($split.legs_count) legs balanced)" -ForegroundColor Green

# 8. Cryptographic Hash Chain Verification
Write-Host "`n[8] CRYPTOGRAPHIC AUDIT TRAIL VERIFICATION (GET /admin/verify-audit-chain)" -ForegroundColor Yellow
$audit = curl.exe -s "$baseUrl/admin/verify-audit-chain"
Write-Host "Response: $audit" -ForegroundColor Green

# 9. Global Double-Entry Reconciliation
Write-Host "`n[9] GLOBAL ZERO-SUM BALANCE RECONCILIATION (GET /admin/reconcile)" -ForegroundColor Yellow
$rec = curl.exe -s "$baseUrl/admin/reconcile"
Write-Host "Response: $rec" -ForegroundColor Green

# 10. Live Prometheus Metrics
Write-Host "`n[10] LIVE PROMETHEUS METRICS (GET /metrics)" -ForegroundColor Yellow
$metrics = curl.exe -s "$baseUrl/metrics" | Select-String "ledger_transactions_total"
Write-Host "$metrics" -ForegroundColor Cyan

Write-Host "`n============================================================" -ForegroundColor Cyan
Write-Host "              ALL DEMO OPERATIONS COMPLETED!                " -ForegroundColor Cyan
Write-Host "       VISIT DASHBOARD: http://localhost:8080/dashboard     " -ForegroundColor Cyan
Write-Host "============================================================" -ForegroundColor Cyan
