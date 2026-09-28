package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ledger/internal/config"
	"ledger/internal/httpapi"
	"ledger/internal/service"
	"ledger/internal/store"
	"ledger/migrations"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := config.Load()

	logger.Info("starting ApexLedger Banking Platform",
		slog.String("port", cfg.Port),
		slog.String("configured_db_url", cfg.DBURL),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var ledgerStore service.Store

	// Attempt PostgreSQL connection with short timeout
	pgConnCtx, pgCancel := context.WithTimeout(ctx, 2*time.Second)
	poolConfig, err := pgxpool.ParseConfig(cfg.DBURL)
	var pool *pgxpool.Pool
	if err == nil {
		poolConfig.MaxConns = 30
		poolConfig.MinConns = 5
		pool, err = pgxpool.NewWithConfig(pgConnCtx, poolConfig)
		if err == nil {
			err = pool.Ping(pgConnCtx)
		}
	}
	pgCancel()

	if err == nil && pool != nil {
		logger.Info("connected to PostgreSQL successfully", slog.String("db", "PostgreSQL 16"))
		pgStore := store.NewPostgresStore(pool)

		// Run migration
		migrateCtx, migrateCancel := context.WithTimeout(ctx, 5*time.Second)
		if err := pgStore.Migrate(migrateCtx, migrations.InitSQL); err != nil {
			migrateCancel()
			logger.Error("database migration failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		migrateCancel()
		logger.Info("database migration applied successfully")
		ledgerStore = pgStore
		defer pool.Close()
	} else {
		logger.Warn("PostgreSQL not reachable, falling back to embedded ACID transactional store",
			slog.String("engine", "In-Memory Dual-Entry Store with SHA-256 Hash Chaining"),
			slog.String("reason", err.Error()),
		)
		ledgerStore = store.NewMemoryStore()
	}

	ledgerService := service.NewLedgerService(ledgerStore)
	handler := httpapi.NewHandler(ledgerService, logger)
	router := httpapi.NewRouter(handler, logger)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	shutdownErr := make(chan error, 1)
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		sig := <-quit

		logger.Info("initiating graceful shutdown", slog.String("signal", sig.String()))

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		shutdownErr <- server.Shutdown(shutdownCtx)
	}()

	logger.Info(fmt.Sprintf("🚀 ApexLedger API is LIVE and running on http://localhost:%s", cfg.Port))
	logger.Info(fmt.Sprintf("📊 Interactive Web Dashboard: http://localhost:%s/dashboard", cfg.Port))
	logger.Info(fmt.Sprintf("📈 Prometheus Metrics: http://localhost:%s/metrics", cfg.Port))

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := <-shutdownErr; err != nil {
		logger.Error("error during server shutdown", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("server shutdown cleanly")
}
