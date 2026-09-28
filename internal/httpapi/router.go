package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(handler *Handler, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	// Middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(StructuredLogger(logger))
	r.Use(middleware.Recoverer)

	// Liveness / health probe
	r.Get("/healthz", handler.Healthz)

	// Account management
	r.Post("/accounts", handler.CreateAccount)
	r.Get("/accounts/{id}", handler.GetAccount)
	r.Get("/accounts/{id}/entries", handler.GetEntries)

	// Money operations
	r.Post("/accounts/{id}/deposits", handler.Deposit)
	r.Post("/accounts/{id}/withdrawals", handler.Withdraw)
	r.Post("/transfers", handler.Transfer)

	// Admin operations
	r.Get("/admin/reconcile", handler.Reconcile)

	return r
}
