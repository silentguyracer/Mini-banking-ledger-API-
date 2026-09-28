package domain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Standard system account IDs
var (
	SystemCashAccountID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	SystemFXAccountID   = uuid.MustParse("00000000-0000-0000-0000-000000000002")
)

// Account types
const (
	AccountTypeCustomer = "customer"
	AccountTypeSystem   = "system"
)

// Transaction kinds
const (
	TxKindDeposit      = "deposit"
	TxKindWithdrawal   = "withdrawal"
	TxKindTransfer     = "transfer"
	TxKindReversal     = "reversal"
	TxKindSplitPayment = "split_payment"
	TxKindFXTransfer   = "fx_transfer"
	TxKindHoldCapture  = "hold_capture"
)

// Entry directions
const (
	DirectionDebit  = "debit"
	DirectionCredit = "credit"
)

// Hold statuses
const (
	HoldStatusActive   = "active"
	HoldStatusCaptured = "captured"
	HoldStatusVoided   = "voided"
)

// Domain models

type Account struct {
	ID               uuid.UUID `json:"id"`
	Owner            string    `json:"owner"`
	Type             string    `json:"type"`
	Currency         string    `json:"currency"`
	Balance          int64     `json:"balance"`
	AvailableBalance int64     `json:"available_balance"`
	CreatedAt        time.Time `json:"created_at"`
}

type Transaction struct {
	ID           uuid.UUID      `json:"id"`
	Kind         string         `json:"kind"`
	ReversalOfID *uuid.UUID     `json:"reversal_of_id,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Entry struct {
	ID            int64     `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`
	AccountID     uuid.UUID `json:"account_id"`
	Direction     string    `json:"direction"`
	Amount        int64     `json:"amount"`
	PrevHash      string    `json:"prev_hash"`
	EntryHash     string    `json:"entry_hash"`
	CreatedAt     time.Time `json:"created_at"`
}

type Leg struct {
	AccountID uuid.UUID `json:"account_id"`
	Direction string    `json:"direction"`
	Amount    int64     `json:"amount"`
}

type Hold struct {
	ID          uuid.UUID  `json:"id"`
	AccountID   uuid.UUID  `json:"account_id"`
	Amount      int64      `json:"amount"`
	Status      string     `json:"status"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	CapturedAt  *time.Time `json:"captured_at,omitempty"`
	VoidedAt    *time.Time `json:"voided_at,omitempty"`
}

type Result struct {
	Replayed bool   `json:"replayed"`
	Status   int    `json:"status"`
	Body     []byte `json:"body"`
}

type AuditVerificationReport struct {
	Verified            bool   `json:"verified"`
	TotalEntriesChecked int    `json:"total_entries_checked"`
	FailedEntryID       *int64 `json:"failed_entry_id,omitempty"`
	Message             string `json:"message"`
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
	ErrHoldNotFound          = errors.New("hold not found")
	ErrHoldNotActive         = errors.New("hold is not active")
	ErrHoldAmountExceeded    = errors.New("capture amount exceeds authorized hold amount")
	ErrAlreadyReversed       = errors.New("transaction has already been reversed")
	ErrCurrencyMismatch      = errors.New("currency mismatch between accounts in single-currency transfer")
	ErrAuditChainBroken      = errors.New("cryptographic audit trail verification failed: data tampering detected")
)
