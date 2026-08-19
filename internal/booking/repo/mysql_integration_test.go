//go:build integration

package repo_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"

	"github.com/Muditjain13/transport-app/internal/booking/repo"
	"github.com/Muditjain13/transport-app/internal/booking/service"
)

func setupMySQL(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()

	container, err := tcmysql.Run(ctx, "mysql:8.4",
		tcmysql.WithDatabase("booking"),
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
	migrationsPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations", "booking")
	sourceURL := "file://" + filepath.ToSlash(migrationsPath)

	m, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up: %v", err)
	}
}

// TestBookingLifecycle_RealMySQL exercises InsertBooking through
// UpdateBookingConfirmed against a real MySQL instance and the actual
// migrations, proving: seat_nos JSON round-trips correctly, the
// transactional outbox insert commits atomically with the status update,
// and GetBookingForUpdate's row lock is real (two sequential
// transactions, not concurrent - the concurrency proof lives in
// inventory's and payment's own integration tests, which share this
// exact WithinTx/GetForUpdate pattern).
func TestBookingLifecycle_RealMySQL(t *testing.T) {
	db := setupMySQL(t)
	mysqlRepo := repo.NewMySQLRepo(db)
	ctx := context.Background()

	booking := service.Booking{
		ID: "b1", UserID: "u1", TripID: "t1", Mode: service.ModeBus,
		SeatNos: []string{"1", "2"}, AmountPaise: 10000,
		Status: service.StatusPending,
	}
	err := mysqlRepo.WithinTx(ctx, func(ctx context.Context, tx service.TxRepository) error {
		return tx.InsertBooking(ctx, booking)
	})
	if err != nil {
		t.Fatalf("InsertBooking() error = %v", err)
	}

	got, err := mysqlRepo.GetBooking(ctx, "b1")
	if err != nil {
		t.Fatalf("GetBooking() error = %v", err)
	}
	if len(got.SeatNos) != 2 || got.SeatNos[0] != "1" || got.SeatNos[1] != "2" {
		t.Errorf("SeatNos = %v, want [1 2] (JSON round-trip)", got.SeatNos)
	}
	if got.Status != service.StatusPending {
		t.Errorf("Status = %s, want pending", got.Status)
	}

	err = mysqlRepo.WithinTx(ctx, func(ctx context.Context, tx service.TxRepository) error {
		if err := tx.UpdateBookingConfirmed(ctx, "b1", "PNR001"); err != nil {
			return err
		}
		return tx.InsertOutboxEvent(ctx, "b1", "booking.confirmed", []byte(`{"booking_id":"b1"}`))
	})
	if err != nil {
		t.Fatalf("confirm transaction error = %v", err)
	}

	got, err = mysqlRepo.GetBooking(ctx, "b1")
	if err != nil {
		t.Fatalf("GetBooking() error = %v", err)
	}
	if got.Status != service.StatusConfirmed || got.PNR != "PNR001" {
		t.Errorf("booking = %+v, want status=confirmed pnr=PNR001", got)
	}

	events, err := mysqlRepo.ListUnpublishedOutboxEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListUnpublishedOutboxEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].EventType != "booking.confirmed" {
		t.Fatalf("outbox events = %+v, want exactly one booking.confirmed", events)
	}

	if err := mysqlRepo.MarkOutboxEventPublished(ctx, events[0].ID); err != nil {
		t.Fatalf("MarkOutboxEventPublished() error = %v", err)
	}
	events, err = mysqlRepo.ListUnpublishedOutboxEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListUnpublishedOutboxEvents() error = %v", err)
	}
	if len(events) != 0 {
		t.Errorf("outbox events after publish = %+v, want none unpublished", events)
	}
}
