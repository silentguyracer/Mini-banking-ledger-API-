package service_test

import (
	"context"
	"errors"
	"testing"

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
	keys     map[string]struct {
		hash string
		body []byte
	}
}

func newMockStore() *mockStore {
	m := &mockStore{
		accounts: make(map[uuid.UUID]*domain.Account),
		entries:  make(map[uuid.UUID][]domain.Entry),
		keys: make(map[string]struct {
			hash string
			body []byte
		}),
	}
	// Seed CASH system account
	m.accounts[domain.SystemCashAccountID] = &domain.Account{
		ID:       domain.SystemCashAccountID,
		Owner:    "CASH",
		Type:     domain.AccountTypeSystem,
		Currency: "INR",
		Balance:  0,
	}
	return m
}

func (m *mockStore) Ping(ctx context.Context) error {
	return nil
}

func (m *mockStore) CreateAccount(ctx context.Context, id uuid.UUID, owner, currency string) (*domain.Account, error) {
	acc := &domain.Account{
		ID:       id,
		Owner:    owner,
		Type:     domain.AccountTypeCustomer,
		Currency: currency,
		Balance:  0,
	}
	m.accounts[id] = acc
	return acc, nil
}

func (m *mockStore) GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	acc, ok := m.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
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

	if accFrom.Balance < amount {
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

	t.Run("Transfer validation rules", func(t *testing.T) {
		acc1, _ := svc.CreateAccount(ctx, "User1", "INR")
		acc2, _ := svc.CreateAccount(ctx, "User2", "INR")

		// Same account transfer rejected
		_, err := svc.Transfer(ctx, "tr-same", nil, acc1.ID, acc1.ID, 100)
		assert.ErrorIs(t, err, domain.ErrSameAccount)

		// Zero amount rejected
		_, err = svc.Transfer(ctx, "tr-zero", nil, acc1.ID, acc2.ID, 0)
		assert.ErrorIs(t, err, domain.ErrInvalidAmount)

		// Negative amount rejected
		_, err = svc.Transfer(ctx, "tr-neg", nil, acc1.ID, acc2.ID, -100)
		assert.ErrorIs(t, err, domain.ErrInvalidAmount)

		// Missing idempotency key rejected
		_, err = svc.Transfer(ctx, "", nil, acc1.ID, acc2.ID, 100)
		assert.ErrorIs(t, err, domain.ErrMissingIdempotencyKey)
	})

	t.Run("Idempotency: same key + same body replays response without repeating debit", func(t *testing.T) {
		acc1, _ := svc.CreateAccount(ctx, "Sender", "INR")
		acc2, _ := svc.CreateAccount(ctx, "Receiver", "INR")
		_, _ = svc.Deposit(ctx, "dep-s", []byte(`{"amount":10000}`), acc1.ID, 10000)

		payload := []byte(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":2000}`)

		// First execution
		res1, err := svc.Transfer(ctx, "idem-key-1", payload, acc1.ID, acc2.ID, 2000)
		require.NoError(t, err)
		assert.False(t, res1.Replayed)

		bal1, _ := svc.Balance(ctx, acc1.ID)
		bal2, _ := svc.Balance(ctx, acc2.ID)
		assert.Equal(t, int64(8000), bal1)
		assert.Equal(t, int64(2000), bal2)

		// Second execution with same key and same body
		res2, err := svc.Transfer(ctx, "idem-key-1", payload, acc1.ID, acc2.ID, 2000)
		require.NoError(t, err)
		assert.True(t, res2.Replayed)

		bal1After, _ := svc.Balance(ctx, acc1.ID)
		bal2After, _ := svc.Balance(ctx, acc2.ID)
		assert.Equal(t, int64(8000), bal1After, "sender balance should not be deducted again")
		assert.Equal(t, int64(2000), bal2After, "receiver balance should not be added again")
	})

	t.Run("Idempotency: same key + different body returns ErrKeyReused", func(t *testing.T) {
		acc1, _ := svc.CreateAccount(ctx, "Sender2", "INR")
		acc2, _ := svc.CreateAccount(ctx, "Receiver2", "INR")
		_, _ = svc.Deposit(ctx, "dep-s2", []byte(`{"amount":10000}`), acc1.ID, 10000)

		payload1 := []byte(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":1000}`)
		payload2 := []byte(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":2000}`)

		_, err := svc.Transfer(ctx, "reused-key", payload1, acc1.ID, acc2.ID, 1000)
		require.NoError(t, err)

		_, err = svc.Transfer(ctx, "reused-key", payload2, acc1.ID, acc2.ID, 2000)
		assert.True(t, errors.Is(err, domain.ErrKeyReused))
	})
}
