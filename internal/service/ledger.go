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
	CreateHold(ctx context.Context, accountID uuid.UUID, amount int64, description string) (*domain.Hold, error)
	CaptureHold(ctx context.Context, key, reqHash string, holdID uuid.UUID, captureAmount int64) (*domain.Result, error)
	VoidHold(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error)
	ReverseTransaction(ctx context.Context, key, reqHash string, transactionID uuid.UUID) (*domain.Result, error)
	SplitPayment(ctx context.Context, key, reqHash string, kind string, legs []domain.Leg) (*domain.Result, error)
	TransferFX(ctx context.Context, key, reqHash string, from, to uuid.UUID, sendAmount, receiveAmount int64) (*domain.Result, error)
	VerifyAuditChain(ctx context.Context) (*domain.AuditVerificationReport, error)
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

func (s *LedgerService) AvailableBalance(ctx context.Context, id uuid.UUID) (int64, error) {
	acc, err := s.GetAccount(ctx, id)
	if err != nil {
		return 0, err
	}
	return acc.AvailableBalance, nil
}

func (s *LedgerService) GetEntries(ctx context.Context, accountID uuid.UUID, before int64, limit int) ([]domain.Entry, error) {
	if accountID == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}
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

// CreateHold authorizes and reserves funds
func (s *LedgerService) CreateHold(ctx context.Context, accountID uuid.UUID, amount int64, description string) (*domain.Hold, error) {
	if accountID == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}
	if amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	return s.store.CreateHold(ctx, accountID, amount, description)
}

// CaptureHold commits a reserved hold into settled ledger entries
func (s *LedgerService) CaptureHold(ctx context.Context, key string, rawBody []byte, holdID uuid.UUID, captureAmount int64) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if holdID == uuid.Nil {
		return nil, domain.ErrHoldNotFound
	}
	if captureAmount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	reqHash := computeHash(rawBody, map[string]any{"hold_id": holdID, "amount": captureAmount})
	return s.store.CaptureHold(ctx, key, reqHash, holdID, captureAmount)
}

// VoidHold cancels a reserved hold
func (s *LedgerService) VoidHold(ctx context.Context, holdID uuid.UUID) (*domain.Hold, error) {
	if holdID == uuid.Nil {
		return nil, domain.ErrHoldNotFound
	}
	return s.store.VoidHold(ctx, holdID)
}

// ReverseTransaction issues compensating entries for an existing transaction
func (s *LedgerService) ReverseTransaction(ctx context.Context, key string, rawBody []byte, transactionID uuid.UUID) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if transactionID == uuid.Nil {
		return nil, fmt.Errorf("%w: invalid transaction id", domain.ErrInvalidInput)
	}

	reqHash := computeHash(rawBody, map[string]any{"transaction_id": transactionID})
	return s.store.ReverseTransaction(ctx, key, reqHash, transactionID)
}

// SplitPayment executes multi-party split payments atomically
func (s *LedgerService) SplitPayment(ctx context.Context, key string, rawBody []byte, kind string, legs []domain.Leg) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if len(legs) < 2 {
		return nil, fmt.Errorf("%w: at least 2 legs required", domain.ErrInvalidInput)
	}
	if kind == "" {
		kind = domain.TxKindSplitPayment
	}

	var debits, credits int64
	for _, leg := range legs {
		if leg.Amount <= 0 {
			return nil, domain.ErrInvalidAmount
		}
		if leg.AccountID == uuid.Nil {
			return nil, domain.ErrAccountNotFound
		}
		if leg.Direction == domain.DirectionDebit {
			debits += leg.Amount
		} else if leg.Direction == domain.DirectionCredit {
			credits += leg.Amount
		} else {
			return nil, fmt.Errorf("%w: invalid direction %s", domain.ErrInvalidInput, leg.Direction)
		}
	}

	if debits != credits {
		return nil, domain.ErrDoubleEntryImbalance
	}

	reqHash := computeHash(rawBody, map[string]any{"kind": kind, "legs": legs})
	return s.store.SplitPayment(ctx, key, reqHash, kind, legs)
}

// TransferFX transfers between different currencies
func (s *LedgerService) TransferFX(ctx context.Context, key string, rawBody []byte, from, to uuid.UUID, sendAmount, receiveAmount int64) (*domain.Result, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, domain.ErrMissingIdempotencyKey
	}
	if from == to {
		return nil, domain.ErrSameAccount
	}
	if sendAmount <= 0 || receiveAmount <= 0 {
		return nil, domain.ErrInvalidAmount
	}
	if from == uuid.Nil || to == uuid.Nil {
		return nil, domain.ErrAccountNotFound
	}

	reqHash := computeHash(rawBody, map[string]any{
		"from":           from,
		"to":             to,
		"send_amount":    sendAmount,
		"receive_amount": receiveAmount,
	})
	return s.store.TransferFX(ctx, key, reqHash, from, to, sendAmount, receiveAmount)
}

func (s *LedgerService) VerifyAuditChain(ctx context.Context) (*domain.AuditVerificationReport, error) {
	return s.store.VerifyAuditChain(ctx)
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
