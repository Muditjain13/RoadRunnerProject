package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"
)

// bookingEventTypes is a plausible spread of lifecycle events a booking
// generates over its life - this table isn't tied to the live `bookings`
// table (see seedBookingEvents doc), so exact fidelity to the real saga's
// event names doesn't matter, only that the volume and date spread are
// realistic enough to make monthly partitioning a real performance
// question rather than a toy one.
var bookingEventTypes = []string{
	"booking.created", "booking.held", "booking.paid", "booking.confirmed",
	"booking.cancelled", "booking.failed", "booking.refunded",
	"supplier.confirmed", "supplier.cancelled", "payment.charged", "payment.refunded",
}

const (
	bookingEventsBatchSize  = 1000
	bookingEventsPerBooking = 12
)

type bookingEventRow struct {
	bookingID string
	eventType string
	createdAt time.Time
}

// seedBookingEvents generates a synthetic historical event log spread
// deterministically across [historyStart, historyEnd) - CLAUDE.md section
// 9's ~20,000,000-row booking_events table, which exists purely to give
// docs/perf/'s partitioning benchmark a realistically large, realistically
// dated table. eventsPerBooking is fixed; bookingCount is derived from
// totalRows so callers just pick a row-count target.
func seedBookingEvents(ctx context.Context, logger *slog.Logger, db *sql.DB, totalRows, eventsPerBooking int, historyStart, historyEnd time.Time) error {
	rng := rand.New(rand.NewPCG(seedRNGSeed, seedRNGSeed+2))
	spanSeconds := int64(historyEnd.Sub(historyStart).Seconds())
	bookingCount := totalRows / eventsPerBooking

	batch := make([]bookingEventRow, 0, bookingEventsBatchSize)
	inserted := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := insertBookingEventsBatch(ctx, db, batch); err != nil {
			return err
		}
		inserted += len(batch)
		batch = batch[:0]
		return nil
	}

	for i := 0; i < bookingCount; i++ {
		bookingID := fmt.Sprintf("hist-booking-%08d", i)
		for j := 0; j < eventsPerBooking; j++ {
			offset := rng.Int64N(spanSeconds)
			batch = append(batch, bookingEventRow{
				bookingID: bookingID,
				eventType: bookingEventTypes[rng.IntN(len(bookingEventTypes))],
				createdAt: historyStart.Add(time.Duration(offset) * time.Second),
			})
			if len(batch) >= bookingEventsBatchSize {
				if err := flush(); err != nil {
					return fmt.Errorf("seed booking_events: %w", err)
				}
				if inserted%1_000_000 == 0 {
					logger.Info("seeding booking_events", "inserted", inserted, "target", totalRows)
				}
			}
		}
	}
	if err := flush(); err != nil {
		return fmt.Errorf("seed booking_events: %w", err)
	}
	logger.Info("seeded booking_events", "count", inserted)
	return nil
}

func insertBookingEventsBatch(ctx context.Context, db *sql.DB, batch []bookingEventRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	var sb strings.Builder
	sb.WriteString("INSERT INTO booking_events (booking_id, event_type, created_at) VALUES ")
	args := make([]any, 0, len(batch)*3)
	for i, r := range batch {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?, ?)")
		args = append(args, r.bookingID, r.eventType, r.createdAt)
	}

	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}
	return tx.Commit()
}
