package repo

// BulkCreateSeats is hand-written, not sqlc-generated: sqlc can't produce a
// variable-arity multi-row INSERT, and CLAUDE.md section 9 requires
// batched multi-row inserts (batch size 1000), not one INSERT per seat.
// It takes a raw *sql.DB rather than a MySQLRepo receiver so cmd/seed can
// call it without depending on the sqlc-generated query set that backs
// the transactional locking path.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Muditjain13/transport-app/internal/inventory/service"
)

const seedBatchSize = 1000

// BulkCreateSeats idempotently inserts seats in batches of seedBatchSize,
// each batch in its own transaction. Re-running with the same seats is a
// no-op (CLAUDE.md: "make seed is idempotent").
func BulkCreateSeats(ctx context.Context, db *sql.DB, seats []service.Seat) error {
	for start := 0; start < len(seats); start += seedBatchSize {
		end := min(start+seedBatchSize, len(seats))
		if err := insertSeatBatch(ctx, db, seats[start:end]); err != nil {
			return fmt.Errorf("bulk create seats [%d:%d]: %w", start, end, err)
		}
	}
	return nil
}

func insertSeatBatch(ctx context.Context, db *sql.DB, batch []service.Seat) error {
	if len(batch) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	var sb strings.Builder
	sb.WriteString("INSERT INTO seats (trip_id, seat_no, class_code, status) VALUES ")
	args := make([]any, 0, len(batch)*4)
	for i, s := range batch {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?, ?, 'available')")
		args = append(args, s.TripID, s.SeatNo, s.ClassCode)
	}
	sb.WriteString(" ON DUPLICATE KEY UPDATE class_code = VALUES(class_code)")

	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("insert batch: %w", err)
	}
	return tx.Commit()
}
