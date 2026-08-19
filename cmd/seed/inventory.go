package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	catalogservice "github.com/Muditjain13/transport-app/internal/catalog/service"
	inventoryrepo "github.com/Muditjain13/transport-app/internal/inventory/repo"
	inventoryservice "github.com/Muditjain13/transport-app/internal/inventory/service"
)

// seedInventorySeats expands each seeded trip's classes into individual
// seat rows. In production this would happen off a "trip created" event
// from catalog; the seeder does it inline since trips here are seeded,
// not created through a live API.
func seedInventorySeats(ctx context.Context, logger *slog.Logger, db *sql.DB, trips []catalogservice.Trip) error {
	var seats []inventoryservice.Seat
	for _, trip := range trips {
		for _, class := range trip.Classes {
			for i := 1; i <= class.Capacity; i++ {
				seats = append(seats, inventoryservice.Seat{
					TripID:    trip.ID,
					SeatNo:    fmt.Sprintf("%s-%d", class.Code, i),
					ClassCode: class.Code,
					Status:    inventoryservice.SeatAvailable,
				})
			}
		}
	}

	if err := inventoryrepo.BulkCreateSeats(ctx, db, seats); err != nil {
		return fmt.Errorf("seed inventory seats: %w", err)
	}
	logger.Info("seeded seats", "count", len(seats))
	return nil
}
