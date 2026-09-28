package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"ledger/internal/domain"
	"ledger/internal/store"

	"github.com/google/uuid"
)

type Store interface {
	Ping(ctx context.Context) error
	CreateAccount(ctx context.Context, id uuid.UUID, owner, currency string) (*domain.Account, error)
	GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error)
	GetEntries(ctx context.Context, accountID uuid.UUID, before int64, limit int) ([]domain.Entry, error)
	Deposit(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error)
	Withdraw(ctx context.Context, key, reqHash string, accountID uuid.UUID, amount int64) (*domain.Result, error)
	Transfer(ctx context.Context, key, reqHash string, from, to uuid.UUID, amount int64) (*domain.Result, error)
	SumEntries(ctx context.Context, accountID uuid.UUID) (int64, error)
	Reconcile(ctx context.Context) (*store.ReconciliationReport, error)
}

type LedgerService struct {
	store Store
}

func NewLedgerService(store Store) *LedgerService {
	return &LedgerService{store: store}
}

func (s *LedgerService) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

func (s *LedgerService) CreateAccount(ctx context.Context, owner, currency string) (*domain.Account, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, fmt.Errorf("%w: owner is required", domain.ErrInvalidInput)
	}
	currency = strings.TrimSpace(strings.ToUpper(currency))
	if currency == "" {
		currency = "INR"
	}
	if len(currency) != 3 {
		return nil, fmt.Errorf("%w: currency must be a 3-letter ISO code", domain.ErrInvalidInput)
	}

	id := uuid.New()
	return s.store.CreateAccount(ctx, id, owner, currency)
}

func (s *LedgerService) GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	if id == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}
	return s.store.GetAccount(ctx, id)
}

func (s *LedgerService) Balance(ctx context.Context, id uuid.UUID) (int64, error) {
	acc, err := s.GetAccount(ctx, id)
	if err != nil {
		return 0, err
	}
	return acc.Balance, nil
}

func (s *LedgerService) GetEntries(ctx context.Context, accountID uuid.UUID, before int64, limit int) ([]domain.Entry, error) {
	if accountID == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}
	// Verify account exists
	if _, err := s.store.GetAccount(ctx, accountID); err != nil {
		return nil, err
	}
	return s.store.GetEntries(ctx, accountID, before, limit)
}

func (s *LedgerService) Deposit(ctx context.Context, key string, rawBody []byte, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	if accountID == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}

	reqHash := computeHash(rawBody, map[string]any{"account_id": accountID, "amount": amount})
	return s.store.Deposit(ctx, key, reqHash, accountID, amount)
}

func (s *LedgerService) Withdraw(ctx context.Context, key string, rawBody []byte, accountID uuid.UUID, amount int64) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	if accountID == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}

	reqHash := computeHash(rawBody, map[string]any{"account_id": accountID, "amount": amount})
	return s.store.Withdraw(ctx, key, reqHash, accountID, amount)
}

func (s *LedgerService) Transfer(ctx context.Context, key string, rawBody []byte, from, to uuid.UUID, amount int64) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if from == to {
		return nil, domain.ErrSameAccount
	}
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	if from == uuid.Nil || to == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}

	reqHash := computeHash(rawBody, map[string]any{"from": from, "to": to, "amount": amount})
	return s.store.Transfer(ctx, key, reqHash, from, to, amount)
}

func (s *LedgerService) SumEntries(ctx context.Context, accountID uuid.UUID) (int64, error) {
	return s.store.SumEntries(ctx, accountID)
}

func (s *LedgerService) Reconcile(ctx context.Context) (*store.ReconciliationReport, error) {
	return s.store.Reconcile(ctx)
}

func computeHash(rawBody []byte, fallbackObj any) string {
	if len(rawBody) > 0 {
		return fmt.Sprintf("%x", sha256.Sum256(rawBody))
	}
	data, _ := json.Marshal(fallbackObj)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
