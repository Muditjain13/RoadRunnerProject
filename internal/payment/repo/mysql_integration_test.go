//go:build integration

package repo_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/Muditjain13/transport-app/internal/payment/repo"
	"github.com/Muditjain13/transport-app/internal/payment/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

func setupMySQL(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := tcmysql.Run(ctx, "mysql:8.4",
		tcmysql.WithDatabase("payment"),
		tcmysql.WithUsername("root"),
		tcmysql.WithPassword("root"),
	)
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate mysql container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping db: %v", err)
	}

	runMigrations(t, db)
	return db
}

func runMigrations(t *testing.T, db *sql.DB) {
	t.Helper()

	driver, err := mysqlmigrate.WithInstance(db, &mysqlmigrate.Config{})
	if err != nil {
		t.Fatalf("migrate driver: %v", err)
	}

	_, thisFile, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations", "payment")
	sourceURL := "file://" + filepath.ToSlash(migrationsPath)

	m, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

// TestCharge_ConcurrentSameIdempotencyKey_RealMySQL proves the unique
// index on idempotency_key actually prevents a double charge under real
// concurrent connections, not just under the fake repo's single mutex.
func TestCharge_ConcurrentSameIdempotencyKey_RealMySQL(t *testing.T) {
	db := setupMySQL(t)
	svc := service.NewService(repo.NewMySQLRepo(db))

	const attempts = 100
	ids := make([]string, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			p, err := svc.Charge(context.Background(), "shared-key", "booking-1", "user-1", 10000)
			if err != nil {
				t.Errorf("goroutine %d: Charge() error = %v", i, err)
				return
			}
			ids[i] = p.ID
		}(i)
	}
	wg.Wait()

	first := ids[0]
	for i, id := range ids {
		if id != first {
			t.Errorf("goroutine %d got payment ID %s, want %s (all callers should see the same charge)", i, id, first)
		}
	}
}

// TestRefund_ConcurrentOverRefund_RealMySQL proves GetPaymentForUpdate's
// row lock actually serializes concurrent refund attempts on real MySQL:
// 100 goroutines each try to refund the full payment amount, exactly one
// may succeed.
func TestRefund_ConcurrentOverRefund_RealMySQL(t *testing.T) {
	db := setupMySQL(t)
	svc := service.NewService(repo.NewMySQLRepo(db))
	ctx := context.Background()

	payment, err := svc.Charge(ctx, "charge-key", "booking-1", "user-1", 10000)
	if err != nil {
		t.Fatalf("Charge() error = %v", err)
	}

	const attempts = 100
	var succeeded, conflicts int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.Refund(ctx, "refund-key-"+strconv.Itoa(i), payment.ID, 10000)
			switch {
			case err == nil:
				atomic.AddInt64(&succeeded, 1)
			case errors.Is(err, apperrors.ErrConflict):
				atomic.AddInt64(&conflicts, 1)
			default:
				t.Errorf("goroutine %d: unexpected error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("succeeded = %d, want exactly 1 (no over-refund)", succeeded)
	}
	if conflicts != attempts-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, attempts-1)
	}
}
