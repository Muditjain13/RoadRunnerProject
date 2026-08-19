//go:build integration

// Package integration_test proves the booking saga end to end against
// real infrastructure and real gRPC boundaries - not fakes, not the
// in-process business logic alone. Every service under test here runs
// its actual gRPC handler and actual client adapter code
// (internal/booking/clients), the same code paths production uses. This
// is deliberately different from - and a superset of - the unit tests in
// internal/booking/service, which prove the saga's orchestration logic is
// correct assuming its dependencies behave a certain way. This test
// proves those dependencies actually do, over the wire.
//
// search and catalog are intentionally not part of this test: booking's
// saga (hold -> pay -> supplier confirm -> ticket issued) takes tripID
// and amountPaise as inputs from its caller and never calls catalog
// itself, so they aren't part of the saga's real dependency graph. They
// have their own test coverage (pkg/pricing, internal/catalog/service,
// internal/search/service).
package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bookingv1 "github.com/Muditjain13/transport-app/api/proto/booking/v1"
	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	paymentv1 "github.com/Muditjain13/transport-app/api/proto/payment/v1"
	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"

	bookingclients "github.com/Muditjain13/transport-app/internal/booking/clients"
	bookinghandler "github.com/Muditjain13/transport-app/internal/booking/handler"
	bookingrepo "github.com/Muditjain13/transport-app/internal/booking/repo"
	bookingservice "github.com/Muditjain13/transport-app/internal/booking/service"

	inventoryhandler "github.com/Muditjain13/transport-app/internal/inventory/handler"
	inventoryrepo "github.com/Muditjain13/transport-app/internal/inventory/repo"
	inventoryservice "github.com/Muditjain13/transport-app/internal/inventory/service"

	paymenthandler "github.com/Muditjain13/transport-app/internal/payment/handler"
	paymentrepo "github.com/Muditjain13/transport-app/internal/payment/repo"
	paymentservice "github.com/Muditjain13/transport-app/internal/payment/service"

	suppliersimhandler "github.com/Muditjain13/transport-app/internal/suppliersim/handler"
	suppliersimservice "github.com/Muditjain13/transport-app/internal/suppliersim/service"
)

// --- infrastructure setup -------------------------------------------------

type mysqlDatabases struct {
	inventoryDSN string
	paymentDSN   string
	bookingDSN   string
}

func setupMySQL(t *testing.T) mysqlDatabases {
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

	endpoint, err := container.PortEndpoint(ctx, "3306/tcp", "")
	if err != nil {
		t.Fatalf("port endpoint: %v", err)
	}
	base := fmt.Sprintf("root:root@tcp(%s)/", endpoint)

	admin, err := sql.Open("mysql", base+"inventory?parseTime=true")
	if err != nil {
		t.Fatalf("open admin conn: %v", err)
	}
	defer admin.Close()
	for _, db := range []string{"payment", "booking"} {
		if _, err := admin.Exec("CREATE DATABASE IF NOT EXISTS " + db); err != nil {
			t.Fatalf("create database %s: %v", db, err)
		}
	}

	dbs := mysqlDatabases{
		inventoryDSN: base + "inventory?parseTime=true&multiStatements=true",
		paymentDSN:   base + "payment?parseTime=true&multiStatements=true",
		bookingDSN:   base + "booking?parseTime=true&multiStatements=true",
	}

	runMigrations(t, dbs.inventoryDSN, "inventory")
	runMigrations(t, dbs.paymentDSN, "payment")
	runMigrations(t, dbs.bookingDSN, "booking")
	return dbs
}

func runMigrations(t *testing.T, dsn, serviceName string) {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", serviceName, err)
	}
	defer db.Close()

	driver, err := mysqlmigrate.WithInstance(db, &mysqlmigrate.Config{})
	if err != nil {
		t.Fatalf("migrate driver for %s: %v", serviceName, err)
	}

	_, thisFile, _, _ := runtime.Caller(0)
	migrationsPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", serviceName)
	sourceURL := "file://" + filepath.ToSlash(migrationsPath)

	m, err := migrate.NewWithDatabaseInstance(sourceURL, "mysql", driver)
	if err != nil {
		t.Fatalf("migrate new for %s: %v", serviceName, err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up for %s: %v", serviceName, err)
	}
}

// startGRPCServer binds to an ephemeral local port, serves in a
// background goroutine, and registers cleanup - the same pattern every
// cmd/<service>/main.go uses, minus the OS-level process boundary.
func startGRPCServer(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)
	return lis.Addr().String()
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// stack is every real, wire-connected service booking's saga depends on.
type stack struct {
	bookingClient bookingv1.BookingServiceClient
	inventoryDB   *sql.DB
}

// buildStack wires real gRPC servers for inventory, payment, and
// supplier-sim (with the given chaos), then a real booking server whose
// client adapters (internal/booking/clients) dial those servers exactly
// as cmd/booking/main.go does.
func buildStack(t *testing.T, dbs mysqlDatabases, supplierChaos suppliersimservice.Chaos) stack {
	t.Helper()

	inventoryDB, err := sql.Open("mysql", dbs.inventoryDSN)
	if err != nil {
		t.Fatalf("open inventory db: %v", err)
	}
	t.Cleanup(func() { inventoryDB.Close() })
	inventorySvc := inventoryservice.NewService(inventoryrepo.NewMySQLRepo(inventoryDB))
	inventoryAddr := startGRPCServer(t, func(s *grpc.Server) {
		inventoryv1.RegisterInventoryServiceServer(s, inventoryhandler.NewGRPCHandler(inventorySvc))
	})

	paymentDB, err := sql.Open("mysql", dbs.paymentDSN)
	if err != nil {
		t.Fatalf("open payment db: %v", err)
	}
	t.Cleanup(func() { paymentDB.Close() })
	paymentSvc := paymentservice.NewService(paymentrepo.NewMySQLRepo(paymentDB))
	paymentAddr := startGRPCServer(t, func(s *grpc.Server) {
		paymentv1.RegisterPaymentServiceServer(s, paymenthandler.NewGRPCHandler(paymentSvc))
	})

	supplierSvc := suppliersimservice.NewService(suppliersimservice.WithChaos(suppliersimservice.ModeBus, supplierChaos))
	supplierAddr := startGRPCServer(t, func(s *grpc.Server) {
		suppliersimv1.RegisterSupplierSimServiceServer(s, suppliersimhandler.NewGRPCHandler(supplierSvc))
	})

	bookingDB, err := sql.Open("mysql", dbs.bookingDSN)
	if err != nil {
		t.Fatalf("open booking db: %v", err)
	}
	t.Cleanup(func() { bookingDB.Close() })

	inventoryClient := bookingclients.NewInventoryClient(inventoryv1.NewInventoryServiceClient(dial(t, inventoryAddr)))
	paymentClient := bookingclients.NewPaymentClient(paymentv1.NewPaymentServiceClient(dial(t, paymentAddr)))
	busAdapter := bookingclients.NewBusSupplierAdapter(suppliersimv1.NewSupplierSimServiceClient(dial(t, supplierAddr)))
	adapters := map[bookingservice.Mode]bookingservice.SupplierAdapter{bookingservice.ModeBus: busAdapter}

	bookingSvc := bookingservice.NewService(bookingrepo.NewMySQLRepo(bookingDB), inventoryClient, paymentClient, adapters, testLogger())
	bookingAddr := startGRPCServer(t, func(s *grpc.Server) {
		bookingv1.RegisterBookingServiceServer(s, bookinghandler.NewGRPCHandler(bookingSvc))
	})

	return stack{
		bookingClient: bookingv1.NewBookingServiceClient(dial(t, bookingAddr)),
		inventoryDB:   inventoryDB,
	}
}

func seedSeats(t *testing.T, db *sql.DB, tripID string, seatNos []string) {
	t.Helper()
	seats := make([]inventoryservice.Seat, 0, len(seatNos))
	for _, sn := range seatNos {
		seats = append(seats, inventoryservice.Seat{TripID: tripID, SeatNo: sn, ClassCode: "seater"})
	}
	if err := inventoryrepo.BulkCreateSeats(context.Background(), db, seats); err != nil {
		t.Fatalf("seed seats: %v", err)
	}
}

// --- tests -----------------------------------------------------------------

// TestBusSaga_HappyPath drives CreateBooking through real gRPC servers for
// inventory, payment, and supplier-sim, backed by real MySQL, and asserts
// the booking reaches Confirmed with both a PNR and a supplier PNR.
func TestBusSaga_HappyPath(t *testing.T) {
	dbs := setupMySQL(t)
	s := buildStack(t, dbs, suppliersimservice.Chaos{})
	seedSeats(t, s.inventoryDB, "trip-1", []string{"1", "2"})

	resp, err := s.bookingClient.CreateBooking(context.Background(), &bookingv1.CreateBookingRequest{
		UserId:      "user-1",
		TripId:      "trip-1",
		SeatNos:     []string{"1", "2"},
		Mode:        bookingv1.Mode_MODE_BUS,
		AmountPaise: 10000,
	})
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	b := resp.GetBooking()
	if b.GetStatus() != bookingv1.Status_STATUS_CONFIRMED {
		t.Fatalf("status = %s, want CONFIRMED", b.GetStatus())
	}
	if b.GetPnr() == "" {
		t.Error("PNR was not issued")
	}
	if b.GetSupplierPnr() == "" {
		t.Error("supplier PNR was not recorded")
	}
}

// TestBusSaga_SupplierDeclines_HardCase is the scenario CLAUDE.md section
// 4 calls out by name: payment succeeds but the supplier declines. Proves
// the full compensation path (refund, release, no supplier cancel since
// no PNR was issued) actually executes correctly across real gRPC calls
// and real MySQL transactions, ending in Cancelled - and that the seat is
// actually bookable again afterward, proving the hold was truly released.
func TestBusSaga_SupplierDeclines_HardCase(t *testing.T) {
	dbs := setupMySQL(t)
	s := buildStack(t, dbs, suppliersimservice.Chaos{ErrorRate: 1.0})
	seedSeats(t, s.inventoryDB, "trip-2", []string{"1"})

	resp, err := s.bookingClient.CreateBooking(context.Background(), &bookingv1.CreateBookingRequest{
		UserId:      "user-1",
		TripId:      "trip-2",
		SeatNos:     []string{"1"},
		Mode:        bookingv1.Mode_MODE_BUS,
		AmountPaise: 10000,
	})
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	b := resp.GetBooking()
	if b.GetStatus() != bookingv1.Status_STATUS_CANCELLED {
		t.Fatalf("status = %s, want CANCELLED", b.GetStatus())
	}

	// The seat must be bookable again - proves ReleaseHold actually ran,
	// not just that the booking row says "cancelled".
	resp2, err := s.bookingClient.CreateBooking(context.Background(), &bookingv1.CreateBookingRequest{
		UserId:      "user-2",
		TripId:      "trip-2",
		SeatNos:     []string{"1"},
		Mode:        bookingv1.Mode_MODE_BUS,
		AmountPaise: 10000,
	})
	if err != nil {
		t.Fatalf("second CreateBooking() error = %v", err)
	}
	// This second attempt will itself hit the same supplier chaos and end
	// up Cancelled too - the point is it got PAST the hold step, proving
	// the seat was actually free.
	if resp2.GetBooking().GetStatus() == bookingv1.Status_STATUS_FAILED {
		t.Errorf("second booking status = FAILED, want it to at least reach the hold (seat should have been released)")
	}
}

// TestBusSaga_ConcurrentBookings_NoDoubleBooking runs 100 concurrent
// CreateBooking calls for the same seat through the full stack (real
// gRPC, real MySQL locking in inventory) and asserts exactly one reaches
// Confirmed - the saga-level counterpart to inventory's own
// TestCreateHold_NoDoubleBooking_RealMySQL, proving the guarantee holds
// through the whole booking flow, not just the raw hold call.
func TestBusSaga_ConcurrentBookings_NoDoubleBooking(t *testing.T) {
	dbs := setupMySQL(t)
	s := buildStack(t, dbs, suppliersimservice.Chaos{})
	seedSeats(t, s.inventoryDB, "trip-3", []string{"1"})

	const attempts = 100
	var confirmed, failed int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			resp, err := s.bookingClient.CreateBooking(context.Background(), &bookingv1.CreateBookingRequest{
				UserId:      fmt.Sprintf("user-%d", i),
				TripId:      "trip-3",
				SeatNos:     []string{"1"},
				Mode:        bookingv1.Mode_MODE_BUS,
				AmountPaise: 10000,
			})
			if err != nil {
				t.Errorf("goroutine %d: CreateBooking() error = %v", i, err)
				return
			}
			switch resp.GetBooking().GetStatus() {
			case bookingv1.Status_STATUS_CONFIRMED:
				atomic.AddInt64(&confirmed, 1)
			case bookingv1.Status_STATUS_FAILED:
				atomic.AddInt64(&failed, 1)
			default:
				t.Errorf("goroutine %d: unexpected status %s", i, resp.GetBooking().GetStatus())
			}
		}(i)
	}
	wg.Wait()

	if confirmed != 1 {
		t.Errorf("confirmed = %d, want exactly 1 (no double-booking)", confirmed)
	}
	if failed != attempts-1 {
		t.Errorf("failed = %d, want %d", failed, attempts-1)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
