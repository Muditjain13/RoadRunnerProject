//go:build integration

package repo_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/Muditjain13/transport-app/internal/inventory/repo"
	"github.com/Muditjain13/transport-app/internal/inventory/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

func setupMySQL(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := tcmysql.Run(ctx, "mysql:8.4",
		tcmysql.WithDatabase("inventory"),
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

// runMigrations applies the exact migrations/inventory files production
// uses, via the real golang-migrate library - the integration test proves
// the schema in migrations/, not a hand-copied approximation of it.
func runMigrations(t *testing.T, db *sql.DB) {
	t.Helper()

	driver, err := mysqlmigrate.WithInstance(db, &mysqlmigrate.Config{})
	if err != nil {
		t.Fatalf("migrate driver: %v", err)
	}

	_, thisFile, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations", "inventory")
	sourceURL := "file://" + filepath.ToSlash(migrationsPath)

	m, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

func seedTestSeats(t *testing.T, db *sql.DB, tripID string, seatNos []string) {
	t.Helper()
	seats := make([]service.Seat, 0, len(seatNos))
	for _, sn := range seatNos {
		seats = append(seats, service.Seat{TripID: tripID, SeatNo: sn, ClassCode: "seater"})
	}
	if err := repo.BulkCreateSeats(context.Background(), db, seats); err != nil {
		t.Fatalf("seed seats: %v", err)
	}
}

// TestCreateHold_NoDoubleBooking_RealMySQL is the definitive proof of the
// repo's single most important correctness property (CLAUDE.md section 2):
// 100 goroutines, each its own connection, racing SELECT ... FOR UPDATE for
// the same seat on real MySQL. Exactly one must win. The equivalent test
// in service_test.go proves the orchestration logic is correct assuming
// the repo serializes transactions; this test proves MySQL actually does.
func TestCreateHold_NoDoubleBooking_RealMySQL(t *testing.T) {
	db := setupMySQL(t)
	seedTestSeats(t, db, "trip-1", []string{"1"})

	mysqlRepo := repo.NewMySQLRepo(db)
	svc := service.NewService(mysqlRepo)

	const attempts = 100
	var succeeded, conflicts int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := svc.CreateHold(context.Background(), "trip-1", "user1", []string{"1"}, 8*time.Minute)
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
		t.Errorf("succeeded = %d, want exactly 1 (no double-booking)", succeeded)
	}
	if conflicts != attempts-1 {
		t.Errorf("conflicts = %d, want %d", conflicts, attempts-1)
	}
}

func TestHoldLifecycle_RealMySQL(t *testing.T) {
	db := setupMySQL(t)
	seedTestSeats(t, db, "trip-2", []string{"1", "2"})

	mysqlRepo := repo.NewMySQLRepo(db)
	svc := service.NewService(mysqlRepo)
	ctx := context.Background()

	hold, err := svc.CreateHold(ctx, "trip-2", "user1", []string{"1", "2"}, 8*time.Minute)
	if err != nil {
		t.Fatalf("CreateHold() error = %v", err)
	}

	if err := svc.ConfirmHold(ctx, hold.ID, "booking-1"); err != nil {
		t.Fatalf("ConfirmHold() error = %v", err)
	}

	if _, err := svc.CreateHold(ctx, "trip-2", "user2", []string{"1"}, 8*time.Minute); !errors.Is(err, apperrors.ErrConflict) {
		t.Errorf("CreateHold() on booked seat error = %v, want ErrConflict", err)
	}

	if err := svc.CancelBooking(ctx, "trip-2", "booking-1", []string{"1", "2"}); err != nil {
		t.Fatalf("CancelBooking() error = %v", err)
	}

	if _, err := svc.CreateHold(ctx, "trip-2", "user3", []string{"1", "2"}, 8*time.Minute); err != nil {
		t.Errorf("CreateHold() after cancellation error = %v, want nil", err)
	}
}

// TestSweepExpiredHolds_RealMySQL seeds an already-expired hold directly
// through the repo's transactional interface (CreateHold's own ttl > 0
// validation would reject an already-expired TTL, so the fixture bypasses
// the service and drives TxRepository the same way CreateHold internally
// does) and verifies the sweeper releases it on real MySQL.
func TestSweepExpiredHolds_RealMySQL(t *testing.T) {
	db := setupMySQL(t)
	seedTestSeats(t, db, "trip-3", []string{"1"})

	mysqlRepo := repo.NewMySQLRepo(db)
	ctx := context.Background()

	const expiredHoldID = "expired-hold"
	err := mysqlRepo.WithinTx(ctx, func(ctx context.Context, tx service.TxRepository) error {
		if _, err := tx.LockSeats(ctx, "trip-3", []string{"1"}); err != nil {
			return err
		}
		h := service.Hold{
			ID:        expiredHoldID,
			TripID:    "trip-3",
			UserID:    "user1",
			Status:    service.HoldActive,
			ExpiresAt: time.Now().Add(-time.Minute),
		}
		if err := tx.CreateHold(ctx, h); err != nil {
			return err
		}
		if err := tx.InsertHoldSeat(ctx, h.ID, "trip-3", "1"); err != nil {
			return err
		}
		return tx.MarkSeatsHeld(ctx, "trip-3", []string{"1"}, h.ID)
	})
	if err != nil {
		t.Fatalf("seed expired hold: %v", err)
	}

	svc := service.NewService(mysqlRepo)
	n, err := svc.SweepExpiredHolds(ctx, 10)
	if err != nil {
		t.Fatalf("SweepExpiredHolds() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("SweepExpiredHolds() released %d holds, want 1", n)
	}

	if _, err := svc.CreateHold(ctx, "trip-3", "user2", []string{"1"}, 8*time.Minute); err != nil {
		t.Errorf("CreateHold() after sweep error = %v, want nil", err)
	}
}
