package service_test

import (
	"context"
	"testing"
	"time"

	"ledger/internal/domain"
	"ledger/internal/service"
	"ledger/internal/store"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStore struct {
	accounts map[uuid.UUID]*domain.Account
	entries  map[uuid.UUID][]domain.Entry
	holds    map[uuid.UUID]*domain.Hold
	keys     map[string]struct {
		hash string
		body []byte
	}
}

func newMockStore() *mockStore {
	m := &mockStore{
		accounts: make(map[uuid.UUID]*domain.Account),
		entries:  make(map[uuid.UUID][]domain.Entry),
		holds:    make(map[uuid.UUID]*domain.Hold),
		keys: make(map[string]struct {
			hash string
			body []byte
		}),
	}
	m.accounts[domain.SystemCashAccountID] = &domain.Account{
		ID:       domain.SystemCashAccountID,
		Owner:    "CASH",
		Type:     domain.AccountTypeSystem,
		Currency: "INR",
		Balance:  0,
	}
	m.accounts[domain.SystemFXAccountID] = &domain.Account{
		ID:       domain.SystemFXAccountID,
		Owner:    "FX_SETTLEMENT",
		Type:     domain.AccountTypeSystem,
		Currency: "XXX",
		Balance:  0,
	}
	return m
}

func (m *mockStore) Ping(ctx context.Context) error { return nil }

func (m *mockStore) CreateAccount(ctx context.Context, id uuid.UUID, owner, currency string) (*domain.Account, error) {
	acc := &domain.Account{
		ID:               id,
		Owner:            owner,
		Type:             domain.AccountTypeCustomer,
		Currency:         currency,
		Balance:          0,
		AvailableBalance: 0,
	}
	m.accounts[id] = acc
	return acc, nil
}

func (m *mockStore) GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
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
	acc.AvailableBalance = acc.Balance - activeHolds
	return acc, nil
}

func (m *mockStore) GetEntries(ctx context.Context, accountID uuid.UUID, before int64, limit int) ([]domain.Entry, error) {
	return m.entries[accountID], nil
}

func (m *mockStore) Deposit(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	if stored, ok := m.keys[key]; ok {
		if stored.hash != reqHash {
			return nil, domain.ErrKeyReused
		}
		return &domain.Result{Replayed: true, Status: 200, Body: stored.body}, nil
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	acc.Balance += amount
	m.keys[key] = struct {
		hash string
		body []byte
	}{hash: reqHash, body: []byte(`{"status":"deposited"}`)}
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"deposited"}`)}, nil
}

func (m *mockStore) Withdraw(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	if stored, ok := m.keys[key]; ok {
		if stored.hash != reqHash {
			return nil, domain.ErrKeyReused
		}
		return &domain.Result{Replayed: true, Status: 200, Body: stored.body}, nil
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	if acc.Balance < amount {
		return nil, domain.ErrInsufficientFunds
	}
	acc.Balance -= amount
	m.keys[key] = struct {
		hash string
		body []byte
	}{hash: reqHash, body: []byte(`{"status":"withdrawn"}`)}
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"withdrawn"}`)}, nil
}

func (m *mockStore) Transfer(ctx context.Context, key, reqHash string, from, to uuid.UUID, amount int64) (*domain.Result, error) {
	if stored, ok := m.keys[key]; ok {
		if stored.hash != reqHash {
			return nil, domain.ErrKeyReused
		}
		return &domain.Result{Replayed: true, Status: 200, Body: stored.body}, nil
	}
	accFrom, ok := m.accounts[from]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	accTo, ok := m.accounts[to]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}

	var activeHolds int64
	for _, h := range m.holds {
		if h.AccountID == from && h.Status == domain.HoldStatusActive {
			activeHolds += h.Amount
		}
	}

	if accFrom.Balance-activeHolds < amount {
		return nil, domain.ErrInsufficientFunds
	}

	accFrom.Balance -= amount
	accTo.Balance += amount
	m.keys[key] = struct {
		hash string
		body []byte
	}{hash: reqHash, body: []byte(`{"status":"transferred"}`)}
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"transferred"}`)}, nil
}

func (m *mockStore) CreateHold(ctx context.Context, accountID uuid.UUID, amount int64, description string) (*domain.Hold, error) {
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

func (m *mockStore) CaptureHold(ctx context.Context, key, reqHash string, holdID uuid.UUID, captureAmount int64) (*domain.Result, error) {
	hold, ok := m.holds[holdID]
	if !ok {
		return nil, domain.ErrHoldNotFound
	}
	if hold.Status != domain.HoldStatusActive {
		return nil, domain.ErrHoldNotActive
	}
	if captureAmount > hold.Amount {
		return nil, domain.ErrHoldAmountExceeded
	}
	acc := m.accounts[hold.AccountID]
	acc.Balance -= captureAmount
	hold.Status = domain.HoldStatusCaptured
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"captured"}`)}, nil
}

func (m *mockStore) VoidHold(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error) {
	hold, ok := m.holds[holdID]
	if !ok {
		return nil, domain.ErrHoldNotFound
	}
	if hold.Status != domain.HoldStatusActive {
		return nil, domain.ErrHoldNotActive
	}
	hold.Status = domain.HoldStatusVoided
	return hold, nil
}

func (m *mockStore) ReverseTransaction(ctx context.Context, key, reqHash string, transactionID uuid.UUID) (*domain.Result, error) {
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"reversed"}`)}, nil
}

func (m *mockStore) SplitPayment(ctx context.Context, key, reqHash string, kind string, legs []domain.Leg) (*domain.Result, error) {
	for _, leg := range legs {
		acc, ok := m.accounts[leg.AccountID]
		if !ok {
			return nil, domain.ErrAccountNotFound
		}
		if leg.Direction == domain.DirectionDebit {
			acc.Balance -= leg.Amount
		} else {
			acc.Balance += leg.Amount
		}
	}
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"split_payment_done"}`)}, nil
}

func (m *mockStore) TransferFX(ctx context.Context, key, reqHash string, from, to uuid.UUID, sendAmount, receiveAmount int64) (*domain.Result, error) {
	accFrom, ok := m.accounts[from]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	accTo, ok := m.accounts[to]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	if accFrom.Balance < sendAmount {
		return nil, domain.ErrInsufficientFunds
	}
	accFrom.Balance -= sendAmount
	accTo.Balance += receiveAmount
	return &domain.Result{Replayed: false, Status: 201, Body: []byte(`{"status":"fx_transfer_done"}`)}, nil
}

func (m *mockStore) VerifyAuditChain(ctx context.Context) (*domain.AuditVerificationReport, error) {
	return &domain.AuditVerificationReport{Verified: true, TotalEntriesChecked: 5, Message: "verified"}, nil
}

func (m *mockStore) SumEntries(ctx context.Context, accountID uuid.UUID) (int64, error) {
	if acc, ok := m.accounts[accountID]; ok {
		return acc.Balance, nil
	}
	return 0, domain.ErrAccountNotFound
}

func (m *mockStore) Reconcile(ctx context.Context) (*store.ReconciliationReport, error) {
	return &store.ReconciliationReport{Status: "OK"}, nil
}

func TestServiceValidationRules(t *testing.T) {
	ctx := context.Background()
	mock := newMockStore()
	svc := service.NewLedgerService(mock)

	t.Run("CreateAccount validation", func(t *testing.T) {
		_, err := svc.CreateAccount(ctx, "", "INR")
		assert.ErrorIs(t, err, domain.ErrInvalidInput)

		_, err = svc.CreateAccount(ctx, "Alice", "INVALID")
		assert.ErrorIs(t, err, domain.ErrInvalidInput)

		acc, err := svc.CreateAccount(ctx, "Alice", "inr")
		require.NoError(t, err)
		assert.Equal(t, "Alice", acc.Owner)
		assert.Equal(t, "INR", acc.Currency)
		assert.Equal(t, int64(0), acc.Balance)
	})

	t.Run("Deposit increases balance", func(t *testing.T) {
		acc, _ := svc.CreateAccount(ctx, "Bob", "INR")
		res, err := svc.Deposit(ctx, "dep-1", []byte(`{"amount":5000}`), acc.ID, 5000)
		require.NoError(t, err)
		assert.False(t, res.Replayed)
		assert.Equal(t, 201, res.Status)

		bal, err := svc.Balance(ctx, acc.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(5000), bal)
	})

	t.Run("Withdrawal above balance returns ErrInsufficientFunds", func(t *testing.T) {
		acc, _ := svc.CreateAccount(ctx, "Charlie", "INR")
		_, _ = svc.Deposit(ctx, "dep-c", []byte(`{"amount":1000}`), acc.ID, 1000)

		_, err := svc.Withdraw(ctx, "w-1", []byte(`{"amount":2000}`), acc.ID, 2000)
		assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

		bal, _ := svc.Balance(ctx, acc.ID)
		assert.Equal(t, int64(1000), bal, "balance must be unchanged after failed withdrawal")
	})

	t.Run("Two-phase holds and available balance", func(t *testing.T) {
		acc, _ := svc.CreateAccount(ctx, "HoldUser", "INR")
		_, _ = svc.Deposit(ctx, "dep-hold", []byte(`{"amount":10000}`), acc.ID, 10000)

		// Authorize hold of 6,000
		hold, err := svc.CreateHold(ctx, acc.ID, 6000, "Hotel reservation")
		require.NoError(t, err)
		assert.Equal(t, domain.HoldStatusActive, hold.Status)

		// Balance is 10,000, but AvailableBalance is 4,000
		avail, err := svc.AvailableBalance(ctx, acc.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(4000), avail)

		// Attempt to transfer 5,000 should fail because only 4,000 is available
		other, _ := svc.CreateAccount(ctx, "OtherUser", "INR")
		_, err = svc.Transfer(ctx, "tr-exceed", nil, acc.ID, other.ID, 5000)
		assert.ErrorIs(t, err, domain.ErrInsufficientFunds)

		// Capture hold of 5,000 (settlement)
		_, err = svc.CaptureHold(ctx, "cap-1", nil, hold.ID, 5000)
		require.NoError(t, err)

		bal, _ := svc.Balance(ctx, acc.ID)
		assert.Equal(t, int64(5000), bal)
	})

	t.Run("Split payments require debits equal credits", func(t *testing.T) {
		cust, _ := svc.CreateAccount(ctx, "Buyer", "INR")
		merch, _ := svc.CreateAccount(ctx, "Merchant", "INR")
		feeAcc, _ := svc.CreateAccount(ctx, "PlatformFee", "INR")
		_, _ = svc.Deposit(ctx, "dep-buyer", nil, cust.ID, 10000)

		// Imbalanced split payment rejected
		imbalancedLegs := []domain.Leg{
			{AccountID: cust.ID, Direction: domain.DirectionDebit, Amount: 10000},
			{AccountID: merch.ID, Direction: domain.DirectionCredit, Amount: 9000},
			// missing 1000 fee!
		}
		_, err := svc.SplitPayment(ctx, "split-bad", nil, domain.TxKindSplitPayment, imbalancedLegs)
		assert.ErrorIs(t, err, domain.ErrDoubleEntryImbalance)

		// Balanced split payment succeeds
		balancedLegs := []domain.Leg{
			{AccountID: cust.ID, Direction: domain.DirectionDebit, Amount: 10000},
			{AccountID: merch.ID, Direction: domain.DirectionCredit, Amount: 9500},
			{AccountID: feeAcc.ID, Direction: domain.DirectionCredit, Amount: 500},
		}
		_, err = svc.SplitPayment(ctx, "split-good", nil, domain.TxKindSplitPayment, balancedLegs)
		require.NoError(t, err)

		merchBal, _ := svc.Balance(ctx, merch.ID)
		feeBal, _ := svc.Balance(ctx, feeAcc.ID)
		assert.Equal(t, int64(9500), merchBal)
		assert.Equal(t, int64(500), feeBal)
	})
}
