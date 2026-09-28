package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"ledger/internal/domain"
	"ledger/internal/httpapi"
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
	m.accounts[domain.SystemCashAccountID] = &domain.Account{
		ID:       domain.SystemCashAccountID,
		Owner:    "CASH",
		Type:     domain.AccountTypeSystem,
		Currency: "INR",
		Balance:  0,
	}
	return m
}

func (m *mockStore) Ping(ctx context.Context) error { return nil }

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
		return &domain.Result{Replayed: true, Status: 201, Body: stored.body}, nil
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	acc.Balance += amount
	body := []byte(`{"transaction_id":"` + uuid.New().String() + `","amount":` + string(rune(amount)) + `}`)
	m.keys[key] = struct {
		hash string
		body []byte
	}{hash: reqHash, body: body}
	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *mockStore) Withdraw(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	if stored, ok := m.keys[key]; ok {
		if stored.hash != reqHash {
			return nil, domain.ErrKeyReused
		}
		return &domain.Result{Replayed: true, Status: 201, Body: stored.body}, nil
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	if acc.Balance < amount {
		return nil, domain.ErrInsufficientFunds
	}
	acc.Balance -= amount
	body := []byte(`{"transaction_id":"` + uuid.New().String() + `"}`)
	m.keys[key] = struct {
		hash string
		body []byte
	}{hash: reqHash, body: body}
	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *mockStore) Transfer(ctx context.Context, key, reqHash string, from, to uuid.UUID, amount int64) (*domain.Result, error) {
	if stored, ok := m.keys[key]; ok {
		if stored.hash != reqHash {
			return nil, domain.ErrKeyReused
		}
		return &domain.Result{Replayed: true, Status: 201, Body: stored.body}, nil
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
	body := []byte(`{"transaction_id":"` + uuid.New().String() + `","from":"` + from.String() + `","to":"` + to.String() + `","amount":` + string(rune(amount)) + `}`)
	m.keys[key] = struct {
		hash string
		body []byte
	}{hash: reqHash, body: body}
	return &domain.Result{Replayed: false, Status: 201, Body: body}, nil
}

func (m *mockStore) SumEntries(ctx context.Context, accountID uuid.UUID) (int64, error) {
	return 0, nil
}

func (m *mockStore) Reconcile(ctx context.Context) (*store.ReconciliationReport, error) {
	return &store.ReconciliationReport{Status: "OK", TotalDebits: 0, TotalCredits: 0}, nil
}

func setupTestServer() (http.Handler, *mockStore) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mock := newMockStore()
	svc := service.NewLedgerService(mock)
	handler := httpapi.NewHandler(svc, logger)
	router := httpapi.NewRouter(handler, logger)
	return router, mock
}

func TestHealthz(t *testing.T) {
	router, _ := setupTestServer()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestCreateAndGetAccount(t *testing.T) {
	router, _ := setupTestServer()

	// POST /accounts
	body := bytes.NewBufferString(`{"owner":"Alice","currency":"INR"}`)
	req := httptest.NewRequest(http.MethodPost, "/accounts", body)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	var created domain.Account
	err := json.Unmarshal(rec.Body.Bytes(), &created)
	require.NoError(t, err)
	assert.Equal(t, "Alice", created.Owner)
	assert.Equal(t, "INR", created.Currency)
	assert.Equal(t, int64(0), created.Balance)

	// GET /accounts/{id}
	getReq := httptest.NewRequest(http.MethodGet, "/accounts/"+created.ID.String(), nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusOK, getRec.Code)
	var fetched domain.Account
	err = json.Unmarshal(getRec.Body.Bytes(), &fetched)
	require.NoError(t, err)
	assert.Equal(t, created.ID, fetched.ID)

	// GET /accounts/invalid
	notFoundReq := httptest.NewRequest(http.MethodGet, "/accounts/"+uuid.New().String(), nil)
	notFoundRec := httptest.NewRecorder()
	router.ServeHTTP(notFoundRec, notFoundReq)
	assert.Equal(t, http.StatusNotFound, notFoundRec.Code)
}

func TestTransferHTTP(t *testing.T) {
	router, store := setupTestServer()
	ctx := context.Background()

	acc1, _ := store.CreateAccount(ctx, uuid.New(), "User1", "INR")
	acc2, _ := store.CreateAccount(ctx, uuid.New(), "User2", "INR")
	acc1.Balance = 10000

	t.Run("Missing Idempotency-Key returns 400", func(t *testing.T) {
		body := bytes.NewBufferString(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":500}`)
		req := httptest.NewRequest(http.MethodPost, "/transfers", body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("Successful transfer returns 201", func(t *testing.T) {
		body := bytes.NewBufferString(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":1000}`)
		req := httptest.NewRequest(http.MethodPost, "/transfers", body)
		req.Header.Set("Idempotency-Key", "tx-key-1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Empty(t, rec.Header().Get("Idempotent-Replay"))
	})

	t.Run("Idempotent replay returns 200 with Idempotent-Replay header", func(t *testing.T) {
		body := bytes.NewBufferString(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":1000}`)
		req := httptest.NewRequest(http.MethodPost, "/transfers", body)
		req.Header.Set("Idempotency-Key", "tx-key-1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "true", rec.Header().Get("Idempotent-Replay"))
	})

	t.Run("Reused key with different payload returns 422", func(t *testing.T) {
		body := bytes.NewBufferString(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":9999}`)
		req := httptest.NewRequest(http.MethodPost, "/transfers", body)
		req.Header.Set("Idempotency-Key", "tx-key-1")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	})

	t.Run("Insufficient funds returns 422", func(t *testing.T) {
		body := bytes.NewBufferString(`{"from":"` + acc1.ID.String() + `","to":"` + acc2.ID.String() + `","amount":500000}`)
		req := httptest.NewRequest(http.MethodPost, "/transfers", body)
		req.Header.Set("Idempotency-Key", "tx-key-insufficient")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	})

	t.Run("Transfer to self returns 400", func(t *testing.T) {
		body := bytes.NewBufferString(`{"from":"` + acc1.ID.String() + `","to":"` + acc1.ID.String() + `","amount":100}`)
		req := httptest.NewRequest(http.MethodPost, "/transfers", body)
		req.Header.Set("Idempotency-Key", "tx-key-self")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestReconcileEndpoint(t *testing.T) {
	router, _ := setupTestServer()
	req := httptest.NewRequest(http.MethodGet, "/admin/reconcile", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var report store.ReconciliationReport
	err := json.Unmarshal(rec.Body.Bytes(), &report)
	require.NoError(t, err)
	assert.Equal(t, "OK", report.Status)
}
