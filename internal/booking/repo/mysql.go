// Package repo is booking's MySQL implementation of service.Repository.
package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Muditjain13/transport-app/internal/booking/repo/sqlcgen"
	"github.com/Muditjain13/transport-app/internal/booking/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type MySQLRepo struct {
	db *sql.DB
}

func NewMySQLRepo(db *sql.DB) *MySQLRepo {
	return &MySQLRepo{db: db}
}

func (r *MySQLRepo) WithinTx(ctx context.Context, fn func(ctx context.Context, tx service.TxRepository) error) error {
	sqlTx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	if err := fn(ctx, &mysqlTx{q: sqlcgen.New(sqlTx)}); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (r *MySQLRepo) GetBooking(ctx context.Context, id string) (service.Booking, error) {
	row, err := sqlcgen.New(r.db).GetBooking(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Booking{}, fmt.Errorf("get booking %s: %w", id, apperrors.ErrNotFound)
		}
		return service.Booking{}, fmt.Errorf("get booking: %w", err)
	}
	return bookingFromRow(getBookingRow(row))
}

func (r *MySQLRepo) ListStaleBookings(ctx context.Context, statuses []service.Status, updatedBefore time.Time, limit int) ([]string, error) {
	sqlcStatuses := make([]sqlcgen.BookingsStatus, len(statuses))
	for i, st := range statuses {
		sqlcStatuses[i] = sqlcgen.BookingsStatus(st)
	}
	ids, err := sqlcgen.New(r.db).ListStaleBookings(ctx, sqlcgen.ListStaleBookingsParams{
		Statuses:  sqlcStatuses,
		UpdatedAt: updatedBefore,
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list stale bookings: %w", err)
	}
	return ids, nil
}

func (r *MySQLRepo) ListUnpublishedOutboxEvents(ctx context.Context, limit int) ([]service.OutboxEvent, error) {
	rows, err := sqlcgen.New(r.db).ListUnpublishedOutboxEvents(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list unpublished outbox events: %w", err)
	}
	out := make([]service.OutboxEvent, len(rows))
	for i, row := range rows {
		out[i] = service.OutboxEvent{ID: row.ID, BookingID: row.BookingID, EventType: row.EventType, Payload: row.Payload}
	}
	return out, nil
}

func (r *MySQLRepo) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	if err := sqlcgen.New(r.db).MarkOutboxEventPublished(ctx, id); err != nil {
		return fmt.Errorf("mark outbox event %d published: %w", id, err)
	}
	return nil
}

func (r *MySQLRepo) GetBookingByHoldID(ctx context.Context, holdID string) (service.Booking, bool, error) {
	row, err := sqlcgen.New(r.db).GetBookingByHoldID(ctx, nullString(holdID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Booking{}, false, nil
		}
		return service.Booking{}, false, fmt.Errorf("get booking by hold id: %w", err)
	}
	b, err := bookingFromRow(getBookingRow(row))
	if err != nil {
		return service.Booking{}, false, err
	}
	return b, true, nil
}

// mysqlTx implements service.TxRepository against one open transaction.
type mysqlTx struct {
	q *sqlcgen.Queries
}

func (t *mysqlTx) InsertBooking(ctx context.Context, b service.Booking) error {
	seatNos, err := json.Marshal(b.SeatNos)
	if err != nil {
		return fmt.Errorf("marshal seat_nos: %w", err)
	}
	err = t.q.InsertBooking(ctx, sqlcgen.InsertBookingParams{
		ID:          b.ID,
		UserID:      b.UserID,
		TripID:      b.TripID,
		Mode:        string(b.Mode),
		SeatNos:     seatNos,
		AmountPaise: int64(b.AmountPaise),
	})
	if err != nil {
		return fmt.Errorf("insert booking: %w", err)
	}
	return nil
}

func (t *mysqlTx) InsertHeldBooking(ctx context.Context, b service.Booking) error {
	seatNos, err := json.Marshal(b.SeatNos)
	if err != nil {
		return fmt.Errorf("marshal seat_nos: %w", err)
	}
	err = t.q.InsertHeldBooking(ctx, sqlcgen.InsertHeldBookingParams{
		ID:              b.ID,
		UserID:          b.UserID,
		TripID:          b.TripID,
		Mode:            string(b.Mode),
		SeatNos:         seatNos,
		AmountPaise:     int64(b.AmountPaise),
		HoldID:          nullString(b.HoldID),
		ExternalHoldRef: nullString(b.ExternalHoldRef),
	})
	if err != nil {
		return fmt.Errorf("insert held booking: %w", err)
	}
	return nil
}

func (t *mysqlTx) GetBookingForUpdate(ctx context.Context, id string) (service.Booking, error) {
	row, err := t.q.GetBookingForUpdate(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Booking{}, fmt.Errorf("get booking %s for update: %w", id, apperrors.ErrNotFound)
		}
		return service.Booking{}, fmt.Errorf("get booking for update: %w", err)
	}
	return bookingFromRow(getBookingRow(row))
}

func (t *mysqlTx) UpdateBookingHeld(ctx context.Context, id, holdID string) error {
	err := t.q.UpdateBookingHeld(ctx, sqlcgen.UpdateBookingHeldParams{HoldID: nullString(holdID), ID: id})
	if err != nil {
		return fmt.Errorf("update booking held: %w", err)
	}
	return nil
}

func (t *mysqlTx) UpdateBookingPaid(ctx context.Context, id, paymentID string) error {
	err := t.q.UpdateBookingPaid(ctx, sqlcgen.UpdateBookingPaidParams{PaymentID: nullString(paymentID), ID: id})
	if err != nil {
		return fmt.Errorf("update booking paid: %w", err)
	}
	return nil
}

func (t *mysqlTx) UpdateBookingSupplierPNR(ctx context.Context, id, supplierPNR string) error {
	err := t.q.UpdateBookingSupplierPNR(ctx, sqlcgen.UpdateBookingSupplierPNRParams{SupplierPnr: nullString(supplierPNR), ID: id})
	if err != nil {
		return fmt.Errorf("update booking supplier pnr: %w", err)
	}
	return nil
}

func (t *mysqlTx) UpdateBookingConfirmed(ctx context.Context, id, pnr string) error {
	err := t.q.UpdateBookingConfirmed(ctx, sqlcgen.UpdateBookingConfirmedParams{Pnr: nullString(pnr), ID: id})
	if err != nil {
		return fmt.Errorf("update booking confirmed: %w", err)
	}
	return nil
}

func (t *mysqlTx) UpdateBookingRefunding(ctx context.Context, id, reason string) error {
	err := t.q.UpdateBookingRefunding(ctx, sqlcgen.UpdateBookingRefundingParams{FailureReason: nullString(reason), ID: id})
	if err != nil {
		return fmt.Errorf("update booking refunding: %w", err)
	}
	return nil
}

func (t *mysqlTx) UpdateBookingFailed(ctx context.Context, id, reason string) error {
	err := t.q.UpdateBookingFailed(ctx, sqlcgen.UpdateBookingFailedParams{FailureReason: nullString(reason), ID: id})
	if err != nil {
		return fmt.Errorf("update booking failed: %w", err)
	}
	return nil
}

func (t *mysqlTx) UpdateBookingCancelled(ctx context.Context, id string) error {
	if err := t.q.UpdateBookingCancelled(ctx, id); err != nil {
		return fmt.Errorf("update booking cancelled: %w", err)
	}
	return nil
}

func (t *mysqlTx) InsertOutboxEvent(ctx context.Context, bookingID, eventType string, payload []byte) error {
	err := t.q.InsertOutboxEvent(ctx, sqlcgen.InsertOutboxEventParams{
		BookingID: bookingID,
		EventType: eventType,
		Payload:   payload,
	})
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// getBookingRow is the shape shared by GetBooking and GetBookingForUpdate
// (sqlc generates two structurally identical but distinctly-named row
// types for them; this local type lets bookingFromRow handle both).
type getBookingRow struct {
	ID              string
	Pnr             sql.NullString
	SupplierPnr     sql.NullString
	UserID          string
	TripID          string
	Mode            string
	SeatNos         json.RawMessage
	AmountPaise     int64
	HoldID          sql.NullString
	ExternalHoldRef sql.NullString
	PaymentID       sql.NullString
	Status          sqlcgen.BookingsStatus
	FailureReason   sql.NullString
}

func bookingFromRow(row getBookingRow) (service.Booking, error) {
	var seatNos []string
	if err := json.Unmarshal(row.SeatNos, &seatNos); err != nil {
		return service.Booking{}, fmt.Errorf("unmarshal seat_nos: %w", err)
	}
	return service.Booking{
		ID:              row.ID,
		PNR:             row.Pnr.String,
		SupplierPNR:     row.SupplierPnr.String,
		UserID:          row.UserID,
		TripID:          row.TripID,
		Mode:            service.Mode(row.Mode),
		SeatNos:         seatNos,
		AmountPaise:     money.Paise(row.AmountPaise),
		HoldID:          row.HoldID.String,
		ExternalHoldRef: row.ExternalHoldRef.String,
		PaymentID:       row.PaymentID.String,
		Status:          service.Status(row.Status),
		FailureReason:   row.FailureReason.String,
	}, nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
