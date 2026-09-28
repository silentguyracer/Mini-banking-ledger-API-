package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Standard system account ID for double-entry cash counterparty
var SystemCashAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

// Account types
const (
	AccountTypeCustomer = "customer"
	AccountTypeSystem   = "system"
)

// Transaction kinds
const (
	TxKindDeposit    = "deposit"
	TxKindWithdrawal = "withdrawal"
	TxKindTransfer   = "transfer"
)

// Entry directions
const (
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
)

// Domain models

type Account struct {
	ID        uuid.UUID `json:"id"`
	Owner     string    `json:"owner"`
	Type      string    `json:"type"`
	Currency  string    `json:"currency"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
}

type Transaction struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
}

type Entry struct {
	ID            int64     `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Direction     string    `json:"direction"`
	Amount        int64     `json:"amount"`
	CreatedAt     time.Time `json:"created_at"`
}

type Leg struct {
	AccountID uuid.UUID
	Direction string
	Amount    int64
}

type Result struct {
	Replayed bool            `json:"replayed"`
	Status   int             `json:"status"`
	Body     []byte          `json:"body"`
}

// Domain errors
var (
	ErrAccountNotFound       = errors.New("account not found")
	ErrInsufficientFunds     = errors.New("insufficient funds")
	ErrSameAccount           = errors.New("cannot transfer to the same account")
	ErrInvalidAmount         = errors.New("amount must be greater than zero")
	ErrInvalidInput          = errors.New("invalid request input")
	ErrMissingIdempotencyKey = errors.New("missing Idempotency-Key header")
	ErrKeyReused             = errors.New("idempotency key reused with different request payload")
	ErrInProgress            = errors.New("request currently in progress")
	ErrDoubleEntryImbalance  = errors.New("double-entry imbalance: debits do not equal credits")
)
