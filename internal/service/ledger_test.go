package service_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ledger/internal/domain"
	"ledger/internal/service"
	"ledger/internal/store"
	"ledger/migrations"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func setupPostgres(t *testing.T) (*service.LedgerService, *store.Store, *pgxpool.Pool) {
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = os.Getenv("DB_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://ledger:ledger@localhost:5432/ledger?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Skipf("skipping Postgres integration test: invalid DB_URL: %v", err)
	}
	poolConfig.MaxConns = 35

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Skipf("skipping Postgres integration test: cannot connect to pool: %v", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("skipping Postgres integration test: database not reachable at %s: %v", dbURL, err)
	}

	pgStore := store.NewPostgresStore(pool)

	// Run migration
	migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer migrateCancel()
	if err := pgStore.Migrate(migrateCtx, migrations.InitSQL); err != nil {
		pool.Close()
		t.Fatalf("failed to apply migrations: %v", err)
	}

	svc := service.NewLedgerService(pgStore)
	return svc, pgStore, pool
}

func TestConcurrentTransfers(t *testing.T) {
	svc, _, pool := setupPostgres(t)
	defer pool.Close()
	ctx := context.Background()

	// 1. Create two accounts and deposit 100,000 in each
	accA, err := svc.CreateAccount(ctx, "Alice", "INR")
	require.NoError(t, err)
	accB, err := svc.CreateAccount(ctx, "Bob", "INR")
	require.NoError(t, err)

	_, err = svc.Deposit(ctx, "dep-init-a-"+accA.ID.String(), nil, accA.ID, 100_000)
	require.NoError(t, err)
	_, err = svc.Deposit(ctx, "dep-init-b-"+accB.ID.String(), nil, accB.ID, 100_000)
	require.NoError(t, err)

	// 2. Launch 100 concurrent transfers alternating directions
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			from, to := accA.ID, accB.ID
			if idx%2 == 1 {
				from, to = accB.ID, accA.ID
			}
			key := fmt.Sprintf("tr-concurrent-%s-%d", accA.ID.String(), idx)
			_, err := svc.Transfer(ctx, key, nil, from, to, 1_000)
			require.NoError(t, err)
		}(i)
	}
	wg.Wait()

	// 3. Verify invariants
	balA, err := svc.Balance(ctx, accA.ID)
	require.NoError(t, err)
	balB, err := svc.Balance(ctx, accB.ID)
	require.NoError(t, err)

	require.Equal(t, int64(200_000), balA+balB, "total money must be conserved")
	require.GreaterOrEqual(t, balA, int64(0))
	require.GreaterOrEqual(t, balB, int64(0))

	sumA, err := svc.SumEntries(ctx, accA.ID)
	require.NoError(t, err)
	sumB, err := svc.SumEntries(ctx, accB.ID)
	require.NoError(t, err)

	require.Equal(t, balA, sumA, "cached balance must match sum of entries for Alice")
	require.Equal(t, balB, sumB, "cached balance must match sum of entries for Bob")

	// Global reconciliation check
	report, err := svc.Reconcile(ctx)
	require.NoError(t, err)
	require.Equal(t, "OK", report.Status)
}

func TestConcurrentIdempotencyRace(t *testing.T) {
	svc, _, pool := setupPostgres(t)
	defer pool.Close()
	ctx := context.Background()

	accA, err := svc.CreateAccount(ctx, "IdemSender", "INR")
	require.NoError(t, err)
	accB, err := svc.CreateAccount(ctx, "IdemReceiver", "INR")
	require.NoError(t, err)

	_, err = svc.Deposit(ctx, "dep-idem-"+accA.ID.String(), nil, accA.ID, 50_000)
	require.NoError(t, err)

	key := "shared-idem-key-" + uuid.New().String()
	payload := []byte(`{"from":"` + accA.ID.String() + `","to":"` + accB.ID.String() + `","amount":5000}`)

	var (
		wg              sync.WaitGroup
		successCount    int64
		replayedCount   int64
		unexpectedCount int64
	)

	// Launch 20 goroutines firing with the exact same idempotency key at the same time
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.Transfer(ctx, key, payload, accA.ID, accB.ID, 5000)
			if err != nil {
				atomic.AddInt64(&unexpectedCount, 1)
				return
			}
			if res.Replayed {
				atomic.AddInt64(&replayedCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int64(0), unexpectedCount, "no unexpected errors allowed")
	require.Equal(t, int64(1), successCount, "exactly one transfer should be executed initially")
	require.Equal(t, int64(19), replayedCount, "the remaining 19 requests should be replayed")

	balA, err := svc.Balance(ctx, accA.ID)
	require.NoError(t, err)
	balB, err := svc.Balance(ctx, accB.ID)
	require.NoError(t, err)

	require.Equal(t, int64(45_000), balA, "sender balance should be debited exactly once")
	require.Equal(t, int64(5_000), balB, "receiver balance should be credited exactly once")
}

func TestConcurrentOverdraftRace(t *testing.T) {
	svc, _, pool := setupPostgres(t)
	defer pool.Close()
	ctx := context.Background()

	acc, err := svc.CreateAccount(ctx, "OverdraftUser", "INR")
	require.NoError(t, err)

	// Account starts with 1,000
	_, err = svc.Deposit(ctx, "dep-overdraft-"+acc.ID.String(), nil, acc.ID, 1_000)
	require.NoError(t, err)

	// Launch 50 goroutines each attempting to withdraw 100 concurrently
	var (
		wg              sync.WaitGroup
		successCount    int64
		failedCount     int64
		unexpectedCount int64
	)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := fmt.Sprintf("wd-overdraft-%s-%d", acc.ID.String(), idx)
			_, err := svc.Withdraw(ctx, key, nil, acc.ID, 100)
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else if err == domain.ErrInsufficientFunds {
				atomic.AddInt64(&failedCount, 1)
			} else {
				atomic.AddInt64(&unexpectedCount, 1)
			}
		}(i)
	}
	wg.Wait()

	require.Equal(t, int64(0), unexpectedCount, "no unexpected errors allowed")
	require.Equal(t, int64(10), successCount, "exactly 10 withdrawals should succeed")
	require.Equal(t, int64(40), failedCount, "exactly 40 withdrawals should fail with ErrInsufficientFunds")

	bal, err := svc.Balance(ctx, acc.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), bal, "final balance must be exactly 0")

	sum, err := svc.SumEntries(ctx, acc.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), sum, "sum of entries must match cached balance")
}
