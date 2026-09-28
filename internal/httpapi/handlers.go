package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"ledger/internal/domain"
	"ledger/internal/metrics"
	"ledger/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	service *service.LedgerService
	logger  *slog.Logger
}

func NewHandler(service *service.LedgerService, logger *slog.Logger) *Handler {
	return &Handler{
		service: service,
		logger:  logger,
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var status int
	switch {
	case errors.Is(err, domain.ErrAccountNotFound), errors.Is(err, domain.ErrHoldNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrInsufficientFunds),
		errors.Is(err, domain.ErrKeyReused),
		errors.Is(err, domain.ErrHoldNotActive),
		errors.Is(err, domain.ErrHoldAmountExceeded),
		errors.Is(err, domain.ErrAlreadyReversed),
		errors.Is(err, domain.ErrAuditChainBroken):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrInProgress):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrSameAccount),
		errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidInput),
		errors.Is(err, domain.ErrCurrencyMismatch),
		errors.Is(err, domain.ErrMissingIdempotencyKey):
		status = http.StatusBadRequest
	default:
		h.logger.Error("internal server error", slog.String("error", err.Error()))
		status = http.StatusInternalServerError
	}

	h.writeJSON(w, status, errorResponse{Error: err.Error()})
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	if err := h.service.Ping(r.Context()); err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createAccountRequest struct {
	Owner    string `json:"owner"`
	Currency string `json:"currency"`
}

func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req createAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	acc, err := h.service.CreateAccount(r.Context(), req.Owner, req.Currency)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, acc)
}

func (h *Handler) GetAccount(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrAccountNotFound)
		return
	}

	acc, err := h.service.GetAccount(r.Context(), id)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, acc)
}

func (h *Handler) GetEntries(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrAccountNotFound)
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	var before int64
	if b := r.URL.Query().Get("before"); b != "" {
		if parsed, err := strconv.ParseInt(b, 10, 64); err == nil && parsed > 0 {
			before = parsed
		}
	}

	entries, err := h.service.GetEntries(r.Context(), id, before, limit)
	if err != nil {
		h.writeError(w, err)
		return
	}

	var nextCursor *int64
	if len(entries) == limit {
		lastID := entries[len(entries)-1].ID
		nextCursor = &lastID
	}

	resp := map[string]any{
		"account_id":  id,
		"entries":     entries,
		"next_cursor": nextCursor,
	}
	h.writeJSON(w, http.StatusOK, resp)
}

type amountRequest struct {
	Amount int64 `json:"amount"`
}

func (h *Handler) Deposit(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrAccountNotFound)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	var req amountRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	res, err := h.service.Deposit(r.Context(), key, rawBody, id, req.Amount)
	metrics.TransactionDuration.WithLabelValues(domain.TxKindDeposit).Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.TransactionsTotal.WithLabelValues(domain.TxKindDeposit, "error").Inc()
		h.writeError(w, err)
		return
	}

	metrics.TransactionsTotal.WithLabelValues(domain.TxKindDeposit, "success").Inc()
	h.respondWithResult(w, res)
}

func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	idStr := chi.URLParam(r, "id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrAccountNotFound)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	var req amountRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	res, err := h.service.Withdraw(r.Context(), key, rawBody, id, req.Amount)
	metrics.TransactionDuration.WithLabelValues(domain.TxKindWithdrawal).Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.TransactionsTotal.WithLabelValues(domain.TxKindWithdrawal, "error").Inc()
		h.writeError(w, err)
		return
	}

	metrics.TransactionsTotal.WithLabelValues(domain.TxKindWithdrawal, "success").Inc()
	h.respondWithResult(w, res)
}

type transferRequest struct {
	From   uuid.UUID `json:"from"`
	To     uuid.UUID `json:"to"`
	Amount int64     `json:"amount"`
}

func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	var req transferRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	res, err := h.service.Transfer(r.Context(), key, rawBody, req.From, req.To, req.Amount)
	metrics.TransactionDuration.WithLabelValues(domain.TxKindTransfer).Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.TransactionsTotal.WithLabelValues(domain.TxKindTransfer, "error").Inc()
		h.writeError(w, err)
		return
	}

	metrics.TransactionsTotal.WithLabelValues(domain.TxKindTransfer, "success").Inc()
	h.respondWithResult(w, res)
}

type holdRequest struct {
	Amount      int64  `json:"amount"`
	Description string `json:"description"`
}

func (h *Handler) CreateHold(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	accountID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrAccountNotFound)
		return
	}

	var req holdRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	hold, err := h.service.CreateHold(r.Context(), accountID, req.Amount, req.Description)
	if err != nil {
		h.writeError(w, err)
		return
	}

	metrics.ActiveHoldsCount.Inc()
	h.writeJSON(w, http.StatusCreated, hold)
}

func (h *Handler) CaptureHold(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	holdID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrHoldNotFound)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	var req amountRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	res, err := h.service.CaptureHold(r.Context(), key, rawBody, holdID, req.Amount)
	if err != nil {
		h.writeError(w, err)
		return
	}

	metrics.ActiveHoldsCount.Dec()
	h.respondWithResult(w, res)
}

func (h *Handler) VoidHold(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	holdID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrHoldNotFound)
		return
	}

	hold, err := h.service.VoidHold(r.Context(), holdID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	metrics.ActiveHoldsCount.Dec()
	h.writeJSON(w, http.StatusOK, hold)
}

func (h *Handler) ReverseTransaction(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	txnID, err := uuid.Parse(idStr)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, _ := io.ReadAll(r.Body)
	res, err := h.service.ReverseTransaction(r.Context(), key, rawBody, txnID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.respondWithResult(w, res)
}

type splitPaymentRequest struct {
	Kind string       `json:"kind"`
	Legs []domain.Leg `json:"legs"`
}

func (h *Handler) SplitPayment(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	var req splitPaymentRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	res, err := h.service.SplitPayment(r.Context(), key, rawBody, req.Kind, req.Legs)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.respondWithResult(w, res)
}

type fxTransferRequest struct {
	From          uuid.UUID `json:"from"`
	To            uuid.UUID `json:"to"`
	SendAmount    int64     `json:"send_amount"`
	ReceiveAmount int64     `json:"receive_amount"`
}

func (h *Handler) TransferFX(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		h.writeError(w, domain.ErrMissingIdempotencyKey)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	var req fxTransferRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		h.writeError(w, domain.ErrInvalidInput)
		return
	}

	res, err := h.service.TransferFX(r.Context(), key, rawBody, req.From, req.To, req.SendAmount, req.ReceiveAmount)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.respondWithResult(w, res)
}

func (h *Handler) VerifyAuditChain(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.VerifyAuditChain(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}

	status := http.StatusOK
	if !report.Verified {
		status = http.StatusUnprocessableEntity
	}
	h.writeJSON(w, status, report)
}

func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Reconcile(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}

	status := http.StatusOK
	if report.Status != "OK" {
		metrics.ReconciliationStatus.Set(0)
		status = http.StatusInternalServerError
	} else {
		metrics.ReconciliationStatus.Set(1)
	}
	h.writeJSON(w, status, report)
}

func (h *Handler) respondWithResult(w http.ResponseWriter, res *domain.Result) {
	w.Header().Set("Content-Type", "application/json")
	status := res.Status
	if res.Replayed {
		w.Header().Set("Idempotent-Replay", "true")
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(res.Body)
}
