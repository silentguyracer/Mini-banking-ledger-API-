package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"ledger/internal/domain"
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
	case errors.Is(err, domain.ErrAccountNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrInsufficientFunds), errors.Is(err, domain.ErrKeyReused):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrInProgress):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrSameAccount),
		errors.Is(err, domain.ErrInvalidAmount),
		errors.Is(err, domain.ErrInvalidInput),
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
	if req.Amount <= 0 {
		h.writeError(w, domain.ErrInvalidAmount)
		return
	}

	res, err := h.service.Deposit(r.Context(), key, rawBody, id, req.Amount)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.respondWithResult(w, res)
}

func (h *Handler) Withdraw(w http.ResponseWriter, r *http.Request) {
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
	if req.Amount <= 0 {
		h.writeError(w, domain.ErrInvalidAmount)
		return
	}

	res, err := h.service.Withdraw(r.Context(), key, rawBody, id, req.Amount)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.respondWithResult(w, res)
}

type transferRequest struct {
	From   uuid.UUID `json:"from"`
	To     uuid.UUID `json:"to"`
	Amount int64     `json:"amount"`
}

func (h *Handler) Transfer(w http.ResponseWriter, r *http.Request) {
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
	if req.From == req.To {
		h.writeError(w, domain.ErrSameAccount)
		return
	}
	if req.Amount <= 0 {
		h.writeError(w, domain.ErrInvalidAmount)
		return
	}

	res, err := h.service.Transfer(r.Context(), key, rawBody, req.From, req.To, req.Amount)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.respondWithResult(w, res)
}

func (h *Handler) Reconcile(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Reconcile(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}

	status := http.StatusOK
	if report.Status != "OK" {
		status = http.StatusInternalServerError
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
