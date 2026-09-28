package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"ledger/internal/domain"

	"github.com/google/uuid"
)

type MemoryStore struct {
	mu           sync.RWMutex
	accounts     map[uuid.UUID]*domain.Account
	transactions map[uuid.UUID]*domain.Transaction
	entries      []domain.Entry
	holds        map[uuid.UUID]*domain.Hold
	idempotency  map[string]*idemRecord
	entrySeq     int64
}

type idemRecord struct {
	hash   string
	txnID  uuid.UUID
	status int
	body   []byte
}

func NewMemoryStore() *MemoryStore {
	m := &MemoryStore{
		accounts:     make(map[uuid.UUID]*domain.Account),
		transactions: make(map[uuid.UUID]*domain.Transaction),
		entries:      make([]domain.Entry, 0),
		holds:        make(map[uuid.UUID]*domain.Hold),
		idempotency:  make(map[string]*idemRecord),
	}

	// Seed CASH counterparty account
	m.accounts[domain.SystemCashAccountID] = &domain.Account{
		ID:        domain.SystemCashAccountID,
		Owner:     "CASH",
		Type:      domain.AccountTypeSystem,
		Currency:  "INR",
		Balance:   0,
		CreatedAt: time.Now(),
	}

	// Seed FX_SETTLEMENT counterparty account
	m.accounts[domain.SystemFXAccountID] = &domain.Account{
		ID:        domain.SystemFXAccountID,
		Owner:     "FX_SETTLEMENT",
		Type:      domain.AccountTypeSystem,
		Currency:  "XXX",
		Balance:   0,
		CreatedAt: time.Now(),
	}

	return m
}

func (m *MemoryStore) Ping(ctx context.Context) error {
	return nil
}

func (m *MemoryStore) CreateAccount(ctx context.Context, id uuid.UUID, owner, currency string) (*domain.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

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
		CreatedAt:        time.Now(),
	}
	m.accounts[id] = acc
	return acc, nil
}

func (m *MemoryStore) GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	acc, ok := m.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}

	var activeHolds int64
	for _, h := range m.holds {
		if h.AccountID == id && h.Status == domain.HoldStatusActive {
			activeHolds += h.Amount
		}
	}

	cp := *acc
	cp.AvailableBalance = cp.Balance - activeHolds
	return &cp, nil
}

func (m *MemoryStore) GetEntries(ctx context.Context, accountID uuid.UUID, before int64, limit int) ([]domain.Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	accEntries := make([]domain.Entry, 0)
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.AccountID == accountID {
			if before > 0 && e.ID >= before {
				continue
			}
			accEntries = append(accEntries, e)
			if len(accEntries) >= limit {
				break
			}
		}
	}
	return accEntries, nil
}

func (m *MemoryStore) claimIdempotency(key, reqHash string) (*domain.Result, bool, error) {
	if rec, ok := m.idempotency[key]; ok {
		if rec.hash != reqHash {
			return nil, false, domain.ErrKeyReused
		}
		if rec.status == 0 {
			return nil, false, domain.ErrInProgress
		}
		return &domain.Result{Replayed: true, Status: rec.status, Body: rec.body}, false, nil
	}

	m.idempotency[key] = &idemRecord{hash: reqHash}
	return nil, true, nil
}

func (m *MemoryStore) saveIdempotency(key string, txnID uuid.UUID, status int, body []byte) {
	if rec, ok := m.idempotency[key]; ok {
		rec.txnID = txnID
		rec.status = status
		rec.body = body
	}
}

func (m *MemoryStore) postEntries(txnID uuid.UUID, kind string, legs []domain.Leg) error {
	var totalDebit, totalCredit int64
	for _, leg := range legs {
		if leg.Amount <= 0 {
			return domain.ErrInvalidAmount
		}
		if leg.Direction == domain.DirectionDebit {
			totalDebit += leg.Amount
		} else if leg.Direction == domain.DirectionCredit {
			totalCredit += leg.Amount
		} else {
			return fmt.Errorf("invalid direction: %s", leg.Direction)
		}
	}

	if totalDebit != totalCredit {
		return domain.ErrDoubleEntryImbalance
	}

	// Record transaction
	m.transactions[txnID] = &domain.Transaction{
		ID:        txnID,
		Kind:      kind,
		CreatedAt: time.Now(),
	}

	// Post entries with cryptographic hash chaining
	for _, leg := range legs {
		acc, ok := m.accounts[leg.AccountID]
		if !ok {
			return domain.ErrAccountNotFound
		}

		m.entrySeq++
		var prevHash string
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].AccountID == leg.AccountID {
				prevHash = m.entries[i].EntryHash
				break
			}
		}

		entryHash := computeEntryHash(prevHash, leg.AccountID, leg.Direction, leg.Amount)

		entry := domain.Entry{
			ID:            m.entrySeq,
			TransactionID: txnID,
			AccountID:     leg.AccountID,
			Direction:     leg.Direction,
			Amount:        leg.Amount,
			PrevHash:      prevHash,
			EntryHash:     entryHash,
			CreatedAt:     time.Now(),
		}
		m.entries = append(m.entries, entry)

		if leg.Direction == domain.DirectionCredit {
			acc.Balance += leg.Amount
		} else {
			if acc.Type != domain.AccountTypeSystem && acc.Balance-leg.Amount < 0 {
				return domain.ErrInsufficientFunds
			}
			acc.Balance -= leg.Amount
		}
	}

	return nil
}

func (m *MemoryStore) Deposit(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	acc, ok := m.accounts[accountID]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrAccountNotFound
	}

	txnID := uuid.New()
	cashID := domain.SystemCashAccountID
	legs := []domain.Leg{
		{AccountID: cashID, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: accountID, Direction: domain.DirectionCredit, Amount: amount},
	}

	if err := m.postEntries(txnID, domain.TxKindDeposit, legs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{
		"transaction_id": txnID,
		"account_id":     accountID,
		"amount":         amount,
		"balance":        acc.Balance,
	})
	m.saveIdempotency(key, txnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) Withdraw(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	acc, ok := m.accounts[accountID]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrAccountNotFound
	}

	var activeHolds int64
	for _, h := range m.holds {
		if h.AccountID == accountID && h.Status == domain.HoldStatusActive {
			activeHolds += h.Amount
		}
	}
	if acc.Balance-activeHolds < amount {
		delete(m.idempotency, key)
		return nil, domain.ErrInsufficientFunds
	}

	txnID := uuid.New()
	cashID := domain.SystemCashAccountID
	legs := []domain.Leg{
		{AccountID: accountID, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: cashID, Direction: domain.DirectionCredit, Amount: amount},
	}

	if err := m.postEntries(txnID, domain.TxKindWithdrawal, legs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{
		"transaction_id": txnID,
		"account_id":     accountID,
		"amount":         amount,
		"balance":        acc.Balance,
	})
	m.saveIdempotency(key, txnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) Transfer(ctx context.Context, key, reqHash string, from, to uuid.UUID, amount int64) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	fromAcc, ok := m.accounts[from]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrAccountNotFound
	}
	_, ok = m.accounts[to]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrAccountNotFound
	}

	var activeHolds int64
	for _, h := range m.holds {
		if h.AccountID == from && h.Status == domain.HoldStatusActive {
			activeHolds += h.Amount
		}
	}
	if fromAcc.Balance-activeHolds < amount {
		delete(m.idempotency, key)
		return nil, domain.ErrInsufficientFunds
	}

	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: from, Direction: domain.DirectionDebit, Amount: amount},
		{AccountID: to, Direction: domain.DirectionCredit, Amount: amount},
	}

	if err := m.postEntries(txnID, domain.TxKindTransfer, legs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{
		"transaction_id": txnID,
		"from":           from,
		"to":             to,
		"amount":         amount,
	})
	m.saveIdempotency(key, txnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) CreateHold(ctx context.Context, accountID uuid.UUID, amount int64, description string) (*domain.Hold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}

	var activeHolds int64
	for _, h := range m.holds {
		if h.AccountID == accountID && h.Status == domain.HoldStatusActive {
			activeHolds += h.Amount
		}
	}
	if acc.Balance-activeHolds < amount {
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
	m.holds[hold.ID] = hold
	return hold, nil
}

func (m *MemoryStore) CaptureHold(ctx context.Context, key, reqHash string, holdID uuid.UUID, captureAmount int64) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	hold, ok := m.holds[holdID]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrHoldNotFound
	}
	if hold.Status != domain.HoldStatusActive {
		delete(m.idempotency, key)
		return nil, domain.ErrHoldNotActive
	}
	if captureAmount > hold.Amount {
		delete(m.idempotency, key)
		return nil, domain.ErrHoldAmountExceeded
	}

	txnID := uuid.New()
	cashID := domain.SystemCashAccountID
	legs := []domain.Leg{
		{AccountID: hold.AccountID, Direction: domain.DirectionDebit, Amount: captureAmount},
		{AccountID: cashID, Direction: domain.DirectionCredit, Amount: captureAmount},
	}

	if err := m.postEntries(txnID, domain.TxKindHoldCapture, legs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	now := time.Now()
	hold.Status = domain.HoldStatusCaptured
	hold.CapturedAt = &now

	body, _ := json.Marshal(map[string]any{
		"transaction_id": txnID,
		"hold_id":        holdID,
		"account_id":     hold.AccountID,
		"amount":         captureAmount,
		"status":         domain.HoldStatusCaptured,
	})
	m.saveIdempotency(key, txnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) VoidHold(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	hold, ok := m.holds[holdID]
	if !ok {
		return nil, domain.ErrHoldNotFound
	}
	if hold.Status != domain.HoldStatusActive {
		return nil, domain.ErrHoldNotActive
	}

	now := time.Now()
	hold.Status = domain.HoldStatusVoided
	hold.VoidedAt = &now
	return hold, nil
}

func (m *MemoryStore) ReverseTransaction(ctx context.Context, key, reqHash string, transactionID uuid.UUID) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	origTxn, ok := m.transactions[transactionID]
	if !ok {
		delete(m.idempotency, key)
		return nil, errors.New("transaction not found")
	}

	for _, tx := range m.transactions {
		if tx.ReversalOfID != nil && *tx.ReversalOfID == transactionID {
			delete(m.idempotency, key)
			return nil, domain.ErrAlreadyReversed
		}
	}

	var reversedLegs []domain.Leg
	for _, e := range m.entries {
		if e.TransactionID == transactionID {
			invDir := domain.DirectionDebit
			if e.Direction == domain.DirectionDebit {
				invDir = domain.DirectionCredit
			}
			reversedLegs = append(reversedLegs, domain.Leg{
				AccountID: e.AccountID,
				Direction: invDir,
				Amount:    e.Amount,
			})
		}
	}

	newTxnID := uuid.New()
	if err := m.postEntries(newTxnID, domain.TxKindReversal, reversedLegs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	m.transactions[newTxnID].ReversalOfID = &origTxn.ID

	body, _ := json.Marshal(map[string]any{
		"transaction_id": newTxnID,
		"reversal_of_id": transactionID,
		"kind":           domain.TxKindReversal,
		"legs_reversed":  len(reversedLegs),
	})
	m.saveIdempotency(key, newTxnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) SplitPayment(ctx context.Context, key, reqHash string, kind string, legs []domain.Leg) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	txnID := uuid.New()
	if err := m.postEntries(txnID, kind, legs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{
		"transaction_id": txnID,
		"kind":           kind,
		"legs_count":     len(legs),
	})
	m.saveIdempotency(key, txnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) TransferFX(ctx context.Context, key, reqHash string, from, to uuid.UUID, sendAmount, receiveAmount int64) (*domain.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	res, claimed, err := m.claimIdempotency(key, reqHash)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return res, nil
	}

	fromAcc, ok := m.accounts[from]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrAccountNotFound
	}
	toAcc, ok := m.accounts[to]
	if !ok {
		delete(m.idempotency, key)
		return nil, domain.ErrAccountNotFound
	}

	if fromAcc.Balance < sendAmount {
		delete(m.idempotency, key)
		return nil, domain.ErrInsufficientFunds
	}

	fxID := domain.SystemFXAccountID
	txnID := uuid.New()
	legs := []domain.Leg{
		{AccountID: from, Direction: domain.DirectionDebit, Amount: sendAmount},
		{AccountID: fxID, Direction: domain.DirectionCredit, Amount: sendAmount},
		{AccountID: fxID, Direction: domain.DirectionDebit, Amount: receiveAmount},
		{AccountID: to, Direction: domain.DirectionCredit, Amount: receiveAmount},
	}

	if err := m.postEntries(txnID, domain.TxKindFXTransfer, legs); err != nil {
		delete(m.idempotency, key)
		return nil, err
	}

	body, _ := json.Marshal(map[string]any{
		"transaction_id":   txnID,
		"from":             from,
		"to":               to,
		"send_amount":      sendAmount,
		"send_currency":    fromAcc.Currency,
		"receive_amount":   receiveAmount,
		"receive_currency": toAcc.Currency,
	})
	m.saveIdempotency(key, txnID, 201, body)

	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *MemoryStore) VerifyAuditChain(ctx context.Context) (*domain.AuditVerificationReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lastSeenHash := map[uuid.UUID]string{}
	totalChecked := 0

	for _, e := range m.entries {
		totalChecked++
		expectedPrev := lastSeenHash[e.AccountID]
		if e.PrevHash != expectedPrev {
			return &domain.AuditVerificationReport{
				Verified:            false,
				TotalEntriesChecked: totalChecked,
				FailedEntryID:       &e.ID,
				Message:             fmt.Sprintf("broken hash chain on entry %d", e.ID),
			}, nil
		}
		expectedHash := computeEntryHash(e.PrevHash, e.AccountID, e.Direction, e.Amount)
		if e.EntryHash != expectedHash {
			return &domain.AuditVerificationReport{
				Verified:            false,
				TotalEntriesChecked: totalChecked,
				FailedEntryID:       &e.ID,
				Message:             fmt.Sprintf("hash tampering on entry %d", e.ID),
			}, nil
		}
		lastSeenHash[e.AccountID] = e.EntryHash
	}

	return &domain.AuditVerificationReport{
		Verified:            true,
		TotalEntriesChecked: totalChecked,
		Message:             "all cryptographic entry hashes verified successfully",
	}, nil
}

func (m *MemoryStore) SumEntries(ctx context.Context, accountID uuid.UUID) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sum int64
	for _, e := range m.entries {
		if e.AccountID == accountID {
			if e.Direction == domain.DirectionCredit {
				sum += e.Amount
			} else {
				sum -= e.Amount
			}
		}
	}
	return sum, nil
}

func (m *MemoryStore) Reconcile(ctx context.Context) (*ReconciliationReport, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	report := &ReconciliationReport{
		Status:        "OK",
		Discrepancies: make([]AccountDiscrepancy, 0),
	}

	for _, e := range m.entries {
		if e.Direction == domain.DirectionDebit {
			report.TotalDebits += e.Amount
		} else {
			report.TotalCredits += e.Amount
		}
	}

	if report.TotalDebits != report.TotalCredits {
		report.Status = "IMBALANCE_DETECTED"
	}

	for id, acc := range m.accounts {
		report.AccountsChecked++
		var sum int64
		for _, e := range m.entries {
			if e.AccountID == id {
				if e.Direction == domain.DirectionCredit {
					sum += e.Amount
				} else {
					sum -= e.Amount
				}
			}
		}
		if acc.Balance != sum {
			report.Status = "DISCREPANCY_DETECTED"
			report.Discrepancies = append(report.Discrepancies, AccountDiscrepancy{
				AccountID:     id,
				CachedBalance: acc.Balance,
				SumEntries:    sum,
				Diff:          acc.Balance - sum,
			})
		}
	}

	return report, nil
}
