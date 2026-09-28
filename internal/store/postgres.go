package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

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

// Migrate executes raw SQL migration script
func (s *Store) Migrate(ctx context.Context, sqlScript string) error {
	_, err := s.pool.Exec(ctx, sqlScript)
	return err
}

func (s *Store) CreateAccount(ctx context.Context, id uuid.UUID, owner, currency string) (*domain.Account, error) {
	if currency == "" {
		currency = "INR"
	}
	acc := &domain.Account{
		ID:       id,
		Owner:    owner,
		Type:     domain.AccountTypeCustomer,
		Currency: currency,
		Balance:  0,
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
	query := `
		SELECT id, owner, type, currency, balance, created_at
		FROM accounts
		WHERE id = $1`
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&acc.ID, &acc.Owner, &acc.Type, &acc.Currency, &acc.Balance, &acc.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrAccountNotFound
		}
		return nil, err
	}
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
			SELECT id, transaction_id, account_id, direction, amount, created_at
			FROM entries
			WHERE account_id = $1 AND id < $2
			ORDER BY id DESC
			LIMIT $3`
		rows, err = s.pool.Query(ctx, query, accountID, before, limit)
	} else {
		query := `
			SELECT id, transaction_id, account_id, direction, amount, created_at
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
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.AccountID, &e.Direction, &e.Amount, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// postEntries enforces double-entry invariants, records transaction and legs, and updates account balances.
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

	// 2. Insert entries & update balances
	for _, leg := range legs {
		_, err = tx.Exec(ctx,
			`INSERT INTO entries (transaction_id, account_id, direction, amount) VALUES ($1, $2, $3, $4)`,
			txnID, leg.AccountID, leg.Direction, leg.Amount,
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

// claimIdempotencyKey claims an idempotency key or returns replayed response / rejection
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

	// 1. Idempotency: claim the key
	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	// 2. Lock both accounts in a deterministic order (prevents deadlocks)
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

	// 3. Business rule
	if balances[from] < amount {
		return nil, domain.ErrInsufficientFunds
	}

	// 4. Write transaction and double-entry legs
	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: from, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: to, Direction: domain.DirectionCredit, Amount: amount},
	}
	if err := s.postEntries(ctx, tx, txnID, domain.TxKindTransfer, legs); err != nil {
		return nil, err
	}

	// 5. Store response for future retries
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

	// 1. Idempotency: claim key
	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	// 2. Lock accounts in sorted order (Customer account and CASH account)
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

	// 3. Post entries: Customer credited, CASH debited
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

	// 1. Idempotency: claim key
	result, claimed, err := s.claimIdempotencyKey(ctx, tx, key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return result, nil
	}

	// 2. Lock accounts in sorted order
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
	if bal < amount {
		return nil, domain.ErrInsufficientFunds
	}

	// 3. Post entries: Customer debited, CASH credited
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

// SumEntries calculates balance directly from append-only entries ledger
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
	Status           string               `json:"status"`
	TotalDebits      int64                `json:"total_debits"`
	TotalCredits     int64                `json:"total_credits"`
	AccountsChecked  int                  `json:"accounts_checked"`
	Discrepancies    []AccountDiscrepancy `json:"discrepancies"`
}

// Reconcile verifies global double-entry conservation and account cached balances vs ledger entries
func (s *Store) Reconcile(ctx context.Context) (*ReconciliationReport, error) {
	report := &ReconciliationReport{
		Status:        "OK",
		Discrepancies: make([]AccountDiscrepancy, 0),
	}

	// 1. Global debits vs credits check
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

	// 2. Check each account's cached balance against SUM(entries)
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
