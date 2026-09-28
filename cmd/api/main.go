package main

import (
	"context"
	_ "embed"
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

	logger.Info("starting mini banking ledger API",
		slog.String("port", cfg.Port),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to PostgreSQL with retry logic
	poolConfig, err := pgxpool.ParseConfig(cfg.DBURL)
	if err != nil {
		logger.Error("failed to parse DB_URL", slog.String("error", err.Error()))
		os.Exit(1)
	}

	poolConfig.MaxConns = 30
	poolConfig.MinConns = 5
	poolConfig.MaxConnLifetime = 1 * time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute

	var pool *pgxpool.Pool
	for attempts := 1; attempts <= 15; attempts++ {
		connectCtx, connectCancel := context.WithTimeout(ctx, 3*time.Second)
		pool, err = pgxpool.NewWithConfig(connectCtx, poolConfig)
		if err == nil {
			err = pool.Ping(connectCtx)
		}
		connectCancel()

		if err == nil {
			logger.Info("connected to PostgreSQL successfully")
			break
		}

		logger.Warn("waiting for database connection...",
			slog.Int("attempt", attempts),
			slog.String("error", err.Error()),
		)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		logger.Error("could not connect to PostgreSQL after retries", slog.String("error", err.Error()))
		os.Exit(1)
	}
	defer pool.Close()

	pgStore := store.NewPostgresStore(pool)

	// Run migration
	migrateCtx, migrateCancel := context.WithTimeout(ctx, 10*time.Second)
	if err := pgStore.Migrate(migrateCtx, migrations.InitSQL); err != nil {
		migrateCancel()
		logger.Error("database migration failed", slog.String("error", err.Error()))
		os.Exit(1)
	}
	migrateCancel()
	logger.Info("database migration applied successfully")

	ledgerService := service.NewLedgerService(pgStore)
	handler := httpapi.NewHandler(ledgerService, logger)
	router := httpapi.NewRouter(handler, logger)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Server shutdown channel
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

	logger.Info(fmt.Sprintf("server is listening on port %s", cfg.Port))
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", slog.String("error", err.Error()))
		os.Exit(1)
	}

	if err := <-shutdownErr; err != nil {
		logger.Error("error during server shutdown", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("server shutdown gracefully")
}
