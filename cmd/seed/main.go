// Command seed populates deterministic reference and schedule data.
//
// Currently seeds catalog only (operators, routes, bus trips) - inventory
// (seats), booking, payment, and user seeding land as those services are
// built (see CLAUDE.md section 10 build order) and will be added to run()
// alongside seedCatalog.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	catalogrepo "github.com/Muditjain13/transport-app/internal/catalog/repo"
	catalogservice "github.com/Muditjain13/transport-app/internal/catalog/service"
)

// profile controls seed volume. "small" is what make seed-small uses for
// fast local iteration and integration tests; "full" targets CLAUDE.md
// section 9's ~5,000,000 seats. Train's per-trip seat count (up to 18
// coaches x 72 berths, split across 3 classes) dominates the total, so
// tripsPerDay has to be small even at "full": empirically, small's (7,2)
// - 14 trip-days per route-mode - produces ~900k seats, i.e. ~64k
// seats/trip-day. (90,1) - 90 trip-days - lands at ~5.8M, the closest
// whole-tripsPerDay value to the stated target while keeping the "90 days
// forward" figure from section 9 exact.
type profile struct {
	daysForward int
	tripsPerDay int
}

var profiles = map[string]profile{
	"small": {daysForward: 7, tripsPerDay: 2},
	"full":  {daysForward: 90, tripsPerDay: 1},
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	profileName := flag.String("profile", "small", "seed profile: small or full")
	mongoURI := flag.String("catalog-mongo-uri", envOr("CATALOG_MONGO_URI", "mongodb://localhost:27017"), "catalog MongoDB URI")
	inventoryDSN := flag.String("inventory-mysql-dsn", envOr("INVENTORY_MYSQL_DSN", "root:root@tcp(localhost:3306)/inventory?parseTime=true"), "inventory MySQL DSN")
	bookingDSN := flag.String("booking-mysql-dsn", envOr("BOOKING_MYSQL_DSN", "root:root@tcp(localhost:3306)/booking?parseTime=true"), "booking MySQL DSN")
	bookingEvents := flag.Int("booking-events", 0, "if > 0, seed this many booking_events rows instead of catalog/inventory (a separate, independently-timed step - see docs/perf/)")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall deadline for this run - the multi-million-row full profile needs far longer than the old 5m default")
	flag.Parse()

	if *bookingEvents > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		db, err := sql.Open("mysql", *bookingDSN)
		if err != nil {
			logger.Error("seed failed", "error", err)
			os.Exit(1)
		}
		defer db.Close()
		historyEnd := time.Now().UTC()
		historyStart := historyEnd.AddDate(-2, 0, 0)
		if err := seedBookingEvents(ctx, logger, db, *bookingEvents, bookingEventsPerBooking, historyStart, historyEnd); err != nil {
			logger.Error("seed failed", "error", err)
			os.Exit(1)
		}
		return
	}

	p, ok := profiles[*profileName]
	if !ok {
		logger.Error("unknown profile", "profile", *profileName)
		os.Exit(1)
	}

	if err := run(logger, *mongoURI, *inventoryDSN, p, *timeout); err != nil {
		logger.Error("seed failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, mongoURI, inventoryDSN string, p profile, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		return fmt.Errorf("connect mongo: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Disconnect(shutdownCtx)
	}()
	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("ping mongo: %w", err)
	}

	catalogRepo := catalogrepo.NewMongoRepo(client.Database("catalog"))
	if err := catalogRepo.EnsureIndexes(ctx); err != nil {
		return fmt.Errorf("ensure catalog indexes: %w", err)
	}

	trips, err := seedCatalog(ctx, logger, catalogRepo, p)
	if err != nil {
		return err
	}

	inventoryDB, err := sql.Open("mysql", inventoryDSN)
	if err != nil {
		return fmt.Errorf("open inventory db: %w", err)
	}
	defer inventoryDB.Close()
	if err := inventoryDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping inventory db: %w", err)
	}

	// Flight owns no local inventory (CLAUDE.md: "Provider owns it... No
	// local lock possible") - only bus and train trips get seat rows.
	// Seeding flight seats here would be both architecturally wrong and
	// needlessly bloat the seats table (192 seats x every flight trip).
	localInventoryTrips := trips[:0:0]
	for _, t := range trips {
		if t.Mode != catalogservice.ModeFlight {
			localInventoryTrips = append(localInventoryTrips, t)
		}
	}

	return seedInventorySeats(ctx, logger, inventoryDB, localInventoryTrips)
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
