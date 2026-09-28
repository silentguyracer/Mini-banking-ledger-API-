package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func NewRouter(handler *Handler, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	// Middlewares
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(StructuredLogger(logger))
	r.Use(middleware.Recoverer)

	// Liveness & observability
	r.Get("/healthz", handler.Healthz)
	r.Handle("/metrics", promhttp.Handler())

	// Account management
	r.Post("/accounts", handler.CreateAccount)
	r.Get("/accounts/{id}", handler.GetAccount)
	r.Get("/accounts/{id}/entries", handler.GetEntries)

	// Money operations
	r.Post("/accounts/{id}/deposits", handler.Deposit)
	r.Post("/accounts/{id}/withdrawals", handler.Withdraw)
	r.Post("/transfers", handler.Transfer)
	r.Post("/transfers/fx", handler.TransferFX)
	r.Post("/transfers/{id}/reversals", handler.ReverseTransaction)
	r.Post("/transactions", handler.SplitPayment)

	// Two-Phase Authorizations & Holds
	r.Post("/accounts/{id}/holds", handler.CreateHold)
	r.Post("/holds/{id}/capture", handler.CaptureHold)
	r.Post("/holds/{id}/void", handler.VoidHold)

	// Admin, Integrity & Cryptographic Auditing
	r.Get("/admin/reconcile", handler.Reconcile)
	r.Get("/admin/verify-audit-chain", handler.VerifyAuditChain)

	return r
}
