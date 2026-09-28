package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"ledger/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) Migrate(ctx context.Context, sqlScript string) error {
	_, err := s.pool.Exec(ctx, sqlScript)
	return err
}

func (s *Store) CreateAccount(ctx context.Context, id uuid.UUID, owner, currency string) (*domain.Account, error) {
	if currency == "" {
		currency = "INR"
	}
	acc := &domain.Account{
		ID:               id,
		Owner:            owner,
		Type:             domain.AccountTypeCustomer,
		Currency:         currency,
		Balance:          0,
		AvailableBalance: 0,
	}

	query := `
		INSERT INTO accounts (id, owner, type, currency, balance)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING created_at`
	err := s.pool.QueryRow(ctx, query, acc.ID, acc.Owner, acc.Type, acc.Currency, acc.Balance).Scan(&acc.CreatedAt)
	if err != nil {
		return nil, err
	}
	return acc, nil
}

func (s *Store) GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	acc := &domain.Account{}
	var activeHolds int64
	query := `
		SELECT a.id, a.owner, a.type, a.currency, a.balance, a.created_at,
		       COALESCE(SUM(h.amount), 0) AS active_holds
		FROM accounts a
		LEFT JOIN holds h ON a.id = h.account_id AND h.status = 'active'
		WHERE a.id = $1
		GROUP BY a.id, a.owner, a.type, a.currency, a.balance, a.created_at`
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&acc.ID, &acc.Owner, &acc.Type, &acc.Currency, &acc.Balance, &acc.CreatedAt, &activeHolds,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}
	acc.AvailableBalance = acc.Balance - activeHolds
	return acc, nil
}

func (s *Store) GetEntries(ctx context.Context, accountID uuid.UUID, before int64, limit int) ([]domain.Entry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var rows pgx.Rows
	var err error
	if before > 0 {
		query := `
			SELECT id, transaction_id, account_id, direction, amount, prev_hash, entry_hash, created_at
			FROM entries
			WHERE account_id = $1 AND id < $2
			ORDER BY id DESC
			LIMIT $3`
		rows, err = s.pool.Query(ctx, query, accountID, before, limit)
	} else {
		query := `
			SELECT id, transaction_id, account_id, direction, amount, prev_hash, entry_hash, created_at
			FROM entries
			WHERE account_id = $1
			ORDER BY id DESC
			LIMIT $2`
		rows, err = s.pool.Query(ctx, query, accountID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]domain.Entry, 0)
	for rows.Next() {
		var e domain.Entry
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.AccountID, &e.Direction, &e.Amount, &e.PrevHash, &e.EntryHash, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// computeEntryHash calculates a cryptographic SHA-256 hash for tamper-evidence
func computeEntryHash(prevHash string, accountID uuid.UUID, direction string, amount int64) string {
	payload := fmt.Sprintf("%s|%s|%s|%d", prevHash, accountID.String(), direction, amount)
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum)
}

// postEntries enforces double-entry invariants, generates tamper-evident hash chains, and updates balances.
func (s *Store) postEntries(ctx context.Context, tx pgx.Tx, txnID uuid.UUID, kind string, legs []domain.Leg) error {
	var totalDebit, totalCredit int64
	for _, leg := range legs {
		if leg.Amount <= 0 {
			return domain.ErrInvalidAmount
		}
		switch leg.Direction {
		case domain.DirectionDebit:
			totalDebit += leg.Amount
		case domain.DirectionCredit:
			totalCredit += leg.Amount
		default:
			return fmt.Errorf("invalid entry direction: %s", leg.Direction)
		}
	}

	if totalDebit != totalCredit {
		return domain.ErrDoubleEntryImbalance
	}

	// 1. Insert transaction
	_, err := tx.Exec(ctx, `INSERT INTO transactions (id, kind) VALUES ($1, $2)`, txnID, kind)
	if err != nil {
		return err
	}

	// 2. Insert entries with tamper-evident cryptographic hash chaining & update balances
	for _, leg := range legs {
		// Fetch previous entry hash for this account
		var prevHash string
		err := tx.QueryRow(ctx,
			`SELECT entry_hash FROM entries WHERE account_id = $1 ORDER BY id DESC LIMIT 1`,
			leg.AccountID,
		).Scan(&prevHash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		entryHash := computeEntryHash(prevHash, leg.AccountID, leg.Direction, leg.Amount)

		_, err = tx.Exec(ctx,
			`INSERT INTO entries (transaction_id, account_id, direction, amount, prev_hash, entry_hash)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			txnID, leg.AccountID, leg.Direction, leg.Amount, prevHash, entryHash,
		)
		if err != nil {
			return err
		}

		if leg.Direction == domain.DirectionCredit {
			_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance + $2 WHERE id = $1`, leg.AccountID, leg.Amount)
		} else {
			_, err = tx.Exec(ctx, `UPDATE accounts SET balance = balance - $2 WHERE id = $1`, leg.AccountID, leg.Amount)
		}
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "no_negative" {
				return domain.ErrInsufficientFunds
			}
			return err
		}
	}

	return nil
}

func (s *Store) claimIdempotencyKey(ctx context.Context, tx pgx.Tx, key, reqHash string) (*domain.Result, bool, error) {
	tag, err := tx.Exec(ctx,
		`INSERT INTO idempotency_keys (key, request_hash) VALUES ($1, $2)
		 ON CONFLICT (key) DO NOTHING`, key, reqHash)
	if err != nil {
		return nil, false, err
	}

	if tag.RowsAffected() == 0 {
		var storedHash string
		var status *int
		var body []byte
		err = tx.QueryRow(ctx,
			`SELECT request_hash, response_status, response_body
			 FROM idempotency_keys WHERE key = $1`, key).Scan(&storedHash, &status, &body)
		if err != nil {
			return nil, false, err
		}
		if storedHash != reqHash {
			return nil, false, domain.ErrKeyReused
		}
		if status == nil {
			return nil, false, domain.ErrInProgress
		}
		return &domain.Result{Replayed: true, Status: *status, Body: body}, false, nil
	}

	return nil, true, nil
}

func (s *Store) saveIdempotencyResponse(ctx context.Context, tx pgx.Tx, key string, txnID uuid.UUID, status int, body []byte) error {
	_, err := tx.Exec(ctx,
		`UPDATE idempotency_keys SET transaction_id = $2, response_status = $3, response_body = $4
		 WHERE key = $1`, key, txnID, status, body)
	return err
}

func (s *Store) Transfer(ctx context.Context, key, reqHash string, from, to uuid.UUID, amount int64) (*domain.Result, error) {
	if from == to {
		return nil, domain.ErrSameAccount
	}
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	// Lock accounts in deterministic order
	ids := []uuid.UUID{from, to}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	rows, err := tx.Query(ctx,
		`SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	balances := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var b int64
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		balances[id] = b
	}
	rows.Close()
	if len(balances) != 2 {
		return nil, domain.ErrAccountNotFound
	}

	// Verify available balance (taking active holds into account)
	var activeHolds int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM holds WHERE account_id = $1 AND status = 'active'`,
		from,
	).Scan(&activeHolds)
	if err != nil {
		return nil, err
	}

	available := balances[from] - activeHolds
	if available < amount {
		return nil, domain.ErrInsufficientFunds
	}

	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: from, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: to, Direction: domain.DirectionCredit, Amount: amount},
	}
	if err := s.postEntries(ctx, tx, txnID, domain.TxKindTransfer, legs); err != nil {
		return nil, err
	}

	respMap := map[string]any{
		"transaction_id": txnID,
		"from":           from,
		"to":             to,
		"amount":         amount,
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, txnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (s *Store) Deposit(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	cashID := domain.SystemCashAccountID
	ids := []uuid.UUID{accountID, cashID}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	rows, err := tx.Query(ctx,
		`SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	balances := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var b int64
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		balances[id] = b
	}
	rows.Close()
	if _, ok := balances[accountID]; !ok {
		return nil, domain.ErrAccountNotFound
	}

	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: cashID, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: accountID, Direction: domain.DirectionCredit, Amount: amount},
	}
	if err := s.postEntries(ctx, tx, txnID, domain.TxKindDeposit, legs); err != nil {
		return nil, err
	}

	newBalance := balances[accountID] + amount
	respMap := map[string]any{
		"transaction_id": txnID,
		"account_id":     accountID,
		"amount":         amount,
		"balance":        newBalance,
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, txnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (s *Store) Withdraw(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	cashID := domain.SystemCashAccountID
	ids := []uuid.UUID{accountID, cashID}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	rows, err := tx.Query(ctx,
		`SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	balances := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var b int64
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		balances[id] = b
	}
	rows.Close()

	bal, ok := balances[accountID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}

	var activeHolds int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM holds WHERE account_id = $1 AND status = 'active'`,
		accountID,
	).Scan(&activeHolds)
	if err != nil {
		return nil, err
	}

	available := bal - activeHolds
	if available < amount {
		return nil, domain.ErrInsufficientFunds
	}

	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: accountID, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: cashID, Direction: domain.DirectionCredit, Amount: amount},
	}
	if err := s.postEntries(ctx, tx, txnID, domain.TxKindWithdrawal, legs); err != nil {
		return nil, err
	}

	newBalance := bal - amount
	respMap := map[string]any{
		"transaction_id": txnID,
		"account_id":     accountID,
		"amount":         amount,
		"balance":        newBalance,
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, txnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

// CreateHold places a reserve authorization hold on an account
func (s *Store) CreateHold(ctx context.Context, accountID uuid.UUID, amount int64, description string) (*domain.Hold, error) {
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var balance int64
	err = tx.QueryRow(ctx,
		`SELECT balance FROM accounts WHERE id = $1 FOR UPDATE`, accountID,
	).Scan(&balance)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}

	var activeHolds int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM holds WHERE account_id = $1 AND status = 'active'`,
		accountID,
	).Scan(&activeHolds)
	if err != nil {
		return nil, err
	}

	if balance-activeHolds < amount {
		return nil, domain.ErrInsufficientFunds
	}

	hold := &domain.Hold{
		ID:          uuid.New(),
		AccountID:   accountID,
		Amount:      amount,
		Status:      domain.HoldStatusActive,
		Description: description,
		CreatedAt:   time.Now(),
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO holds (id, account_id, amount, status, description, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		hold.ID, hold.AccountID, hold.Amount, hold.Status, hold.Description, hold.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return hold, nil
}

// CaptureHold commits the hold into settled entries (e.g. card settlement)
func (s *Store) CaptureHold(ctx context.Context, key, reqHash string, holdID uuid.UUID, captureAmount int64) (*domain.Result, error) {
	if captureAmount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	var hold domain.Hold
	err = tx.QueryRow(ctx,
		`SELECT id, account_id, amount, status FROM holds WHERE id = $1 FOR UPDATE`, holdID,
	).Scan(&hold.ID, &hold.AccountID, &hold.Amount, &hold.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrHoldNotFound
		}
		return nil, err
	}

	if hold.Status != domain.HoldStatusActive {
		return nil, domain.ErrHoldNotActive
	}
	if captureAmount > hold.Amount {
		return nil, domain.ErrHoldAmountExceeded
	}

	// Settle transaction: customer debited, CASH credited
	cashID := domain.SystemCashAccountID
	ids := []uuid.UUID{hold.AccountID, cashID}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	rows, err := tx.Query(ctx,
		`SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	balances := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var b int64
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		balances[id] = b
	}
	rows.Close()

	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: hold.AccountID, Direction: domain.DirectionDebit, Amount: captureAmount},
		{AccountID: cashID, Direction: domain.DirectionCredit, Amount: captureAmount},
	}
	if err := s.postEntries(ctx, tx, txnID, domain.TxKindHoldCapture, legs); err != nil {
		return nil, err
	}

	// Mark hold as captured
	now := time.Now()
	_, err = tx.Exec(ctx,
		`UPDATE holds SET status = 'captured', captured_at = $2 WHERE id = $1`, holdID, now)
	if err != nil {
		return nil, err
	}

	respMap := map[string]any{
		"transaction_id": txnID,
		"hold_id":        holdID,
		"account_id":     hold.AccountID,
		"amount":         captureAmount,
		"status":         domain.HoldStatusCaptured,
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, txnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

// VoidHold releases a reserve hold without debiting the customer
func (s *Store) VoidHold(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var hold domain.Hold
	err = tx.QueryRow(ctx,
		`SELECT id, account_id, amount, status FROM holds WHERE id = $1 FOR UPDATE`, holdID,
	).Scan(&hold.ID, &hold.AccountID, &hold.Amount, &hold.Status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrHoldNotFound
		}
		return nil, err
	}

	if hold.Status != domain.HoldStatusActive {
		return nil, domain.ErrHoldNotActive
	}

	now := time.Now()
	_, err = tx.Exec(ctx,
		`UPDATE holds SET status = 'voided', voided_at = $2 WHERE id = $1`, holdID, now)
	if err != nil {
		return nil, err
	}

	hold.Status = domain.HoldStatusVoided
	hold.VoidedAt = &now

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &hold, nil
}

// ReverseTransaction creates compensating inverse entries for an existing transaction
func (s *Store) ReverseTransaction(ctx context.Context, key, reqHash string, transactionID uuid.UUID) (*domain.Result, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	// 1. Verify original transaction exists
	var origKind string
	err = tx.QueryRow(ctx, `SELECT kind FROM transactions WHERE id = $1`, transactionID).Scan(&origKind)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("transaction to reverse not found")
		}
		return nil, err
	}

	// 2. Ensure it hasn't already been reversed
	var existingReversalID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT id FROM transactions WHERE reversal_of_id = $1`, transactionID,
	).Scan(&existingReversalID)
	if err == nil {
		return nil, domain.ErrAlreadyReversed
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// 3. Fetch original entries
	rows, err := tx.Query(ctx,
		`SELECT account_id, direction, amount FROM entries WHERE transaction_id = $1`, transactionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reversedLegs []domain.Leg
	uniqueAccountMap := map[uuid.UUID]bool{}
	for rows.Next() {
		var accID uuid.UUID
		var dir string
		var amt int64
		if err := rows.Scan(&accID, &dir, &amt); err != nil {
			return nil, err
		}
		uniqueAccountMap[accID] = true

		// Invert direction: debit becomes credit, credit becomes debit
		invDir := domain.DirectionDebit
		if dir == domain.DirectionDebit {
			invDir = domain.DirectionCredit
		}
		reversedLegs = append(reversedLegs, domain.Leg{
			AccountID: accID,
			Direction: invDir,
			Amount:    amt,
		})
	}
	rows.Close()

	if len(reversedLegs) == 0 {
		return nil, errors.New("no entries found to reverse")
	}

	// 4. Lock involved accounts in deterministic order
	ids := make([]uuid.UUID, 0, len(uniqueAccountMap))
	for id := range uniqueAccountMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	lockRows, err := tx.Query(ctx,
		`SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	for lockRows.Next() {
		var id uuid.UUID
		var b int64
		_ = lockRows.Scan(&id, &b)
	}
	lockRows.Close()

	// 5. Post compensating entries
	newTxnID := uuid.New()
	if err := s.postEntries(ctx, tx, newTxnID, domain.TxKindReversal, reversedLegs); err != nil {
		return nil, err
	}

	// Link reversal
	_, err = tx.Exec(ctx,
		`UPDATE transactions SET reversal_of_id = $2 WHERE id = $1`, newTxnID, transactionID)
	if err != nil {
		return nil, err
	}

	respMap := map[string]any{
		"transaction_id": newTxnID,
		"reversal_of_id": transactionID,
		"kind":           domain.TxKindReversal,
		"legs_reversed":  len(reversedLegs),
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, newTxnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

// SplitPayment executes arbitrary N-legged multi-party split payments atomically
func (s *Store) SplitPayment(ctx context.Context, key, reqHash string, kind string, legs []domain.Leg) (*domain.Result, error) {
	if len(legs) < 2 {
		return nil, errors.New("split payment requires at least 2 legs")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	// Collect and sort all distinct account IDs
	uniqueMap := map[uuid.UUID]bool{}
	for _, leg := range legs {
		uniqueMap[leg.AccountID] = true
	}
	ids := make([]uuid.UUID, 0, len(uniqueMap))
	for id := range uniqueMap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	rows, err := tx.Query(ctx,
		`SELECT id, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	balances := map[uuid.UUID]int64{}
	for rows.Next() {
		var id uuid.UUID
		var b int64
		if err := rows.Scan(&id, &b); err != nil {
			rows.Close()
			return nil, err
		}
		balances[id] = b
	}
	rows.Close()

	if len(balances) != len(ids) {
		return nil, domain.ErrAccountNotFound
	}

	txnID := uuid.New()
	if err := s.postEntries(ctx, tx, txnID, kind, legs); err != nil {
		return nil, err
	}

	respMap := map[string]any{
		"transaction_id": txnID,
		"kind":           kind,
		"legs_count":     len(legs),
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, txnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

// TransferFX transfers money between accounts in different currencies via FX_SETTLEMENT counterparty
func (s *Store) TransferFX(ctx context.Context, key, reqHash string, from, to uuid.UUID, sendAmount, receiveAmount int64) (*domain.Result, error) {
	if from == to {
		return nil, domain.ErrSameAccount
	}
	if sendAmount <= 0 || receiveAmount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	fxID := domain.SystemFXAccountID
	ids := []uuid.UUID{from, to, fxID}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })

	rows, err := tx.Query(ctx,
		`SELECT id, currency, balance FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	type accInfo struct {
		currency string
		balance  int64
	}
	accs := map[uuid.UUID]accInfo{}
	for rows.Next() {
		var id uuid.UUID
		var curr string
		var b int64
		if err := rows.Scan(&id, &curr, &b); err != nil {
			rows.Close()
			return nil, err
		}
		accs[id] = accInfo{currency: curr, balance: b}
	}
	rows.Close()

	if _, ok := accs[from]; !ok {
		return nil, domain.ErrAccountNotFound
	}
	if _, ok := accs[to]; !ok {
		return nil, domain.ErrAccountNotFound
	}

	fromAcc := accs[from]
	var activeHolds int64
	err = tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM holds WHERE account_id = $1 AND status = 'active'`, from,
	).Scan(&activeHolds)
	if err != nil {
		return nil, err
	}
	if fromAcc.balance-activeHolds < sendAmount {
		return nil, domain.ErrInsufficientFunds
	}

	// Multi-legged FX double-entry:
	// Send Currency: Sender debited sendAmount, FX credited sendAmount
	// Receive Currency: FX debited receiveAmount, Receiver credited receiveAmount
	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: from, Direction: domain.DirectionDebit, Amount: sendAmount},
		{AccountID: fxID, Direction: domain.DirectionCredit, Amount: sendAmount},
		{AccountID: fxID, Direction: domain.DirectionDebit, Amount: receiveAmount},
		{AccountID: to, Direction: domain.DirectionCredit, Amount: receiveAmount},
	}
	if err := s.postEntries(ctx, tx, txnID, domain.TxKindFXTransfer, legs); err != nil {
		return nil, err
	}

	respMap := map[string]any{
		"transaction_id":   txnID,
		"from":             from,
		"to":               to,
		"send_amount":      sendAmount,
		"send_currency":    fromAcc.currency,
		"receive_amount":   receiveAmount,
		"receive_currency": accs[to].currency,
	}
	body, _ := json.Marshal(respMap)
	if err := s.saveIdempotencyResponse(ctx, tx, key, txnID, 201, body); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

// VerifyAuditChain cryptographically verifies the tamper-evident hash chain across all entries
func (s *Store) VerifyAuditChain(ctx context.Context) (*domain.AuditVerificationReport, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, account_id, direction, amount, prev_hash, entry_hash
		 FROM entries
		 ORDER BY account_id, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lastSeenHash := map[uuid.UUID]string{}
	totalChecked := 0

	for rows.Next() {
		var id int64
		var accID uuid.UUID
		var dir, prevHash, entryHash string
		var amount int64

		if err := rows.Scan(&id, &accID, &dir, &amount, &prevHash, &entryHash); err != nil {
			return nil, err
		}
		totalChecked++

		expectedPrev := lastSeenHash[accID]
		if prevHash != expectedPrev {
			return &domain.AuditVerificationReport{
				Verified:            false,
				TotalEntriesChecked: totalChecked,
				FailedEntryID:       &id,
				Message:             fmt.Sprintf("broken hash chain on entry %d for account %s: expected prev_hash %s, got %s", id, accID, expectedPrev, prevHash),
			}, nil
		}

		expectedHash := computeEntryHash(prevHash, accID, dir, amount)
		if entryHash != expectedHash {
			return &domain.AuditVerificationReport{
				Verified:            false,
				TotalEntriesChecked: totalChecked,
				FailedEntryID:       &id,
				Message:             fmt.Sprintf("entry tampering detected on entry %d: hash mismatch", id),
			}, nil
		}

		lastSeenHash[accID] = entryHash
	}

	return &domain.AuditVerificationReport{
		Verified:            true,
		TotalEntriesChecked: totalChecked,
		Message:             "all cryptographic entry hashes verified successfully",
	}, rows.Err()
}

func (s *Store) SumEntries(ctx context.Context, accountID uuid.UUID) (int64, error) {
	query := `
		SELECT COALESCE(SUM(
			CASE 
				WHEN direction = 'credit' THEN amount 
				WHEN direction = 'debit' THEN -amount 
				ELSE 0 
			END
		), 0)
		FROM entries
		WHERE account_id = $1`
	var sum int64
	err := s.pool.QueryRow(ctx, query, accountID).Scan(&sum)
	return sum, err
}

type AccountDiscrepancy struct {
	AccountID     uuid.UUID `json:"account_id"`
	CachedBalance int64     `json:"cached_balance"`
	SumEntries    int64     `json:"sum_entries"`
	Diff          int64     `json:"diff"`
}

type ReconciliationReport struct {
	Status          string               `json:"status"`
	TotalDebits     int64                `json:"total_debits"`
	TotalCredits    int64                `json:"total_credits"`
	AccountsChecked int                  `json:"accounts_checked"`
	Discrepancies   []AccountDiscrepancy `json:"discrepancies"`
}

func (s *Store) Reconcile(ctx context.Context) (*ReconciliationReport, error) {
	report := &ReconciliationReport{
		Status:        "OK",
		Discrepancies: make([]AccountDiscrepancy, 0),
	}

	queryGlobal := `
		SELECT 
			COALESCE(SUM(CASE WHEN direction = 'debit' THEN amount ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount ELSE 0 END), 0)
		FROM entries`
	err := s.pool.QueryRow(ctx, queryGlobal).Scan(&report.TotalDebits, &report.TotalCredits)
	if err != nil {
		return nil, err
	}

	if report.TotalDebits != report.TotalCredits {
		report.Status = "IMBALANCE_DETECTED"
	}

	queryAccounts := `
		SELECT a.id, a.balance,
			COALESCE(SUM(
				CASE 
					WHEN e.direction = 'credit' THEN e.amount 
					WHEN e.direction = 'debit' THEN -e.amount 
					ELSE 0 
				END
			), 0) as calculated_sum
		FROM accounts a
		LEFT JOIN entries e ON a.id = e.account_id
		GROUP BY a.id, a.balance`
	rows, err := s.pool.Query(ctx, queryAccounts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id uuid.UUID
		var cached, calculated int64
		if err := rows.Scan(&id, &cached, &calculated); err != nil {
			return nil, err
		}
		report.AccountsChecked++
		if cached != calculated {
			report.Status = "DISCREPANCY_DETECTED"
			report.Discrepancies = append(report.Discrepancies, AccountDiscrepancy{
				AccountID:     id,
				CachedBalance: cached,
				SumEntries:    calculated,
				Diff:          cached - calculated,
			})
		}
	}

	return report, rows.Err()
}
