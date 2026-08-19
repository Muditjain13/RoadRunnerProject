// Package repo is inventory's MySQL implementation of service.Repository.
// All locking semantics live in service; this package only translates
// between domain types and sqlc-generated queries/rows.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Muditjain13/transport-app/internal/inventory/repo/sqlcgen"
	"github.com/Muditjain13/transport-app/internal/inventory/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

type MySQLRepo struct {
	db *sql.DB
}

func NewMySQLRepo(db *sql.DB) *MySQLRepo {
	return &MySQLRepo{db: db}
}

// WithinTx runs fn inside a single MySQL transaction. Every seat/hold
// mutation in the service layer goes through this - it is the only place
// a transaction begins, commits, or rolls back.
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

func (r *MySQLRepo) ListExpiredActiveHolds(ctx context.Context, before time.Time, limit int) ([]service.HoldRef, error) {
	rows, err := sqlcgen.New(r.db).ListExpiredActiveHolds(ctx, sqlcgen.ListExpiredActiveHoldsParams{
		ExpiresAt: before,
		Limit:     int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list expired active holds: %w", err)
	}
	out := make([]service.HoldRef, len(rows))
	for i, row := range rows {
		out[i] = service.HoldRef{ID: row.ID, TripID: row.TripID}
	}
	return out, nil
}

func (r *MySQLRepo) ListAvailableSeats(ctx context.Context, tripID, classCode string) ([]string, error) {
	seatNos, err := sqlcgen.New(r.db).ListAvailableSeats(ctx, sqlcgen.ListAvailableSeatsParams{
		TripID:    tripID,
		ClassCode: classCode,
	})
	if err != nil {
		return nil, fmt.Errorf("list available seats: %w", err)
	}
	return seatNos, nil
}

func (r *MySQLRepo) GetWaitlistEntry(ctx context.Context, id int64) (service.WaitlistEntry, error) {
	row, err := sqlcgen.New(r.db).GetWaitlistEntry(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.WaitlistEntry{}, fmt.Errorf("get waitlist entry %d: %w", id, apperrors.ErrNotFound)
		}
		return service.WaitlistEntry{}, fmt.Errorf("get waitlist entry: %w", err)
	}
	return waitlistEntryFromRow(row.ID, row.TripID, row.ClassCode, row.UserID, row.SeatCount, row.AmountPaise, row.Status, row.PromotedHoldID), nil
}

func (r *MySQLRepo) ListWaitlistEntriesByUser(ctx context.Context, userID string) ([]service.WaitlistEntry, error) {
	rows, err := sqlcgen.New(r.db).ListWaitlistEntriesByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list waitlist entries by user: %w", err)
	}
	out := make([]service.WaitlistEntry, len(rows))
	for i, row := range rows {
		out[i] = waitlistEntryFromRow(row.ID, row.TripID, row.ClassCode, row.UserID, row.SeatCount, row.AmountPaise, row.Status, row.PromotedHoldID)
	}
	return out, nil
}

func (r *MySQLRepo) GetHoldSeatNos(ctx context.Context, holdID string) ([]string, error) {
	rows, err := sqlcgen.New(r.db).GetHoldSeats(ctx, holdID)
	if err != nil {
		return nil, fmt.Errorf("get hold seat nos: %w", err)
	}
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.SeatNo
	}
	return out, nil
}

func (r *MySQLRepo) ListUnpublishedOutboxEvents(ctx context.Context, limit int) ([]service.OutboxEvent, error) {
	rows, err := sqlcgen.New(r.db).ListUnpublishedOutboxEvents(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list unpublished outbox events: %w", err)
	}
	out := make([]service.OutboxEvent, len(rows))
	for i, row := range rows {
		out[i] = service.OutboxEvent{ID: row.ID, TripID: row.TripID, EventType: row.EventType, Payload: row.Payload}
	}
	return out, nil
}

func (r *MySQLRepo) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	if err := sqlcgen.New(r.db).MarkOutboxEventPublished(ctx, id); err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	return nil
}

func waitlistEntryFromRow(id int64, tripID, classCode, userID string, seatCount int32, amountPaise int64, status sqlcgen.WaitlistEntriesStatus, promotedHoldID sql.NullString) service.WaitlistEntry {
	return service.WaitlistEntry{
		ID:             id,
		TripID:         tripID,
		ClassCode:      classCode,
		UserID:         userID,
		SeatCount:      int(seatCount),
		AmountPaise:    amountPaise,
		Status:         service.WaitlistStatus(status),
		PromotedHoldID: promotedHoldID.String,
	}
}

// mysqlTx implements service.TxRepository against one open transaction.
type mysqlTx struct {
	q *sqlcgen.Queries
}

func (t *mysqlTx) LockSeats(ctx context.Context, tripID string, seatNos []string) ([]service.Seat, error) {
	rows, err := t.q.LockSeats(ctx, sqlcgen.LockSeatsParams{TripID: tripID, SeatNos: seatNos})
	if err != nil {
		return nil, fmt.Errorf("lock seats: %w", err)
	}
	out := make([]service.Seat, len(rows))
	for i, row := range rows {
		out[i] = service.Seat{
			TripID:    row.TripID,
			SeatNo:    row.SeatNo,
			ClassCode: row.ClassCode,
			Status:    service.SeatStatus(row.Status),
			HoldID:    row.HoldID.String,
			BookingID: row.BookingID.String,
		}
	}
	return out, nil
}

func (t *mysqlTx) MarkSeatsHeld(ctx context.Context, tripID string, seatNos []string, holdID string) error {
	err := t.q.MarkSeatsHeld(ctx, sqlcgen.MarkSeatsHeldParams{
		HoldID:  nullString(holdID),
		TripID:  tripID,
		SeatNos: seatNos,
	})
	if err != nil {
		return fmt.Errorf("mark seats held: %w", err)
	}
	return nil
}

func (t *mysqlTx) MarkSeatsAvailableFromHold(ctx context.Context, tripID string, seatNos []string, holdID string) (int64, error) {
	n, err := t.q.MarkSeatsAvailableFromHold(ctx, sqlcgen.MarkSeatsAvailableFromHoldParams{
		TripID:  tripID,
		SeatNos: seatNos,
		HoldID:  nullString(holdID),
	})
	if err != nil {
		return 0, fmt.Errorf("mark seats available from hold: %w", err)
	}
	return n, nil
}

func (t *mysqlTx) MarkSeatsBooked(ctx context.Context, tripID string, seatNos []string, holdID, bookingID string) (int64, error) {
	n, err := t.q.MarkSeatsBooked(ctx, sqlcgen.MarkSeatsBookedParams{
		BookingID: nullString(bookingID),
		TripID:    tripID,
		SeatNos:   seatNos,
		HoldID:    nullString(holdID),
	})
	if err != nil {
		return 0, fmt.Errorf("mark seats booked: %w", err)
	}
	return n, nil
}

func (t *mysqlTx) MarkSeatsAvailableFromBooking(ctx context.Context, tripID string, seatNos []string, bookingID string) (int64, error) {
	n, err := t.q.MarkSeatsAvailableFromBooking(ctx, sqlcgen.MarkSeatsAvailableFromBookingParams{
		TripID:    tripID,
		SeatNos:   seatNos,
		BookingID: nullString(bookingID),
	})
	if err != nil {
		return 0, fmt.Errorf("mark seats available from booking: %w", err)
	}
	return n, nil
}

func (t *mysqlTx) CreateHold(ctx context.Context, hold service.Hold) error {
	err := t.q.CreateHold(ctx, sqlcgen.CreateHoldParams{
		ID:        hold.ID,
		TripID:    hold.TripID,
		UserID:    hold.UserID,
		ExpiresAt: hold.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("create hold: %w", err)
	}
	return nil
}

func (t *mysqlTx) InsertHoldSeat(ctx context.Context, holdID, tripID, seatNo string) error {
	err := t.q.InsertHoldSeat(ctx, sqlcgen.InsertHoldSeatParams{HoldID: holdID, TripID: tripID, SeatNo: seatNo})
	if err != nil {
		return fmt.Errorf("insert hold seat: %w", err)
	}
	return nil
}

func (t *mysqlTx) GetHoldForUpdate(ctx context.Context, holdID string) (service.Hold, error) {
	row, err := t.q.GetHoldForUpdate(ctx, holdID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Hold{}, fmt.Errorf("get hold %s: %w", holdID, apperrors.ErrNotFound)
		}
		return service.Hold{}, fmt.Errorf("get hold for update: %w", err)
	}
	return service.Hold{
		ID:        row.ID,
		TripID:    row.TripID,
		UserID:    row.UserID,
		Status:    service.HoldStatus(row.Status),
		ExpiresAt: row.ExpiresAt,
	}, nil
}

func (t *mysqlTx) GetHoldSeats(ctx context.Context, holdID string) ([]service.SeatRef, error) {
	rows, err := t.q.GetHoldSeats(ctx, holdID)
	if err != nil {
		return nil, fmt.Errorf("get hold seats: %w", err)
	}
	out := make([]service.SeatRef, len(rows))
	for i, row := range rows {
		out[i] = service.SeatRef{TripID: row.TripID, SeatNo: row.SeatNo}
	}
	return out, nil
}

func (t *mysqlTx) UpdateHoldStatus(ctx context.Context, holdID string, status service.HoldStatus) error {
	err := t.q.UpdateHoldStatus(ctx, sqlcgen.UpdateHoldStatusParams{
		Status: sqlcgen.HoldsStatus(status),
		ID:     holdID,
	})
	if err != nil {
		return fmt.Errorf("update hold status: %w", err)
	}
	return nil
}

func (t *mysqlTx) LockAvailableSeatsByClass(ctx context.Context, tripID, classCode string, count int) ([]string, error) {
	seatNos, err := t.q.LockAvailableSeatsByClass(ctx, sqlcgen.LockAvailableSeatsByClassParams{
		TripID:    tripID,
		ClassCode: classCode,
		Limit:     int32(count),
	})
	if err != nil {
		return nil, fmt.Errorf("lock available seats by class: %w", err)
	}
	return seatNos, nil
}

func (t *mysqlTx) GetSeatClassCodes(ctx context.Context, tripID string, seatNos []string) ([]string, error) {
	classCodes, err := t.q.GetSeatClassCodes(ctx, sqlcgen.GetSeatClassCodesParams{TripID: tripID, SeatNos: seatNos})
	if err != nil {
		return nil, fmt.Errorf("get seat class codes: %w", err)
	}
	return classCodes, nil
}

func (t *mysqlTx) InsertWaitlistEntry(ctx context.Context, entry service.WaitlistEntry) (int64, error) {
	id, err := t.q.InsertWaitlistEntry(ctx, sqlcgen.InsertWaitlistEntryParams{
		TripID:      entry.TripID,
		ClassCode:   entry.ClassCode,
		UserID:      entry.UserID,
		SeatCount:   int32(entry.SeatCount),
		AmountPaise: entry.AmountPaise,
	})
	if err != nil {
		return 0, fmt.Errorf("insert waitlist entry: %w", err)
	}
	return id, nil
}

func (t *mysqlTx) GetWaitlistHeadForUpdate(ctx context.Context, tripID, classCode string) (service.WaitlistEntry, bool, error) {
	row, err := t.q.GetWaitlistHeadForUpdate(ctx, sqlcgen.GetWaitlistHeadForUpdateParams{TripID: tripID, ClassCode: classCode})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.WaitlistEntry{}, false, nil
		}
		return service.WaitlistEntry{}, false, fmt.Errorf("get waitlist head for update: %w", err)
	}
	return waitlistEntryFromRow(row.ID, row.TripID, row.ClassCode, row.UserID, row.SeatCount, row.AmountPaise, row.Status, row.PromotedHoldID), true, nil
}

func (t *mysqlTx) UpdateWaitlistPromoted(ctx context.Context, id int64, holdID string) error {
	err := t.q.UpdateWaitlistPromoted(ctx, sqlcgen.UpdateWaitlistPromotedParams{
		PromotedHoldID: nullString(holdID),
		ID:             id,
	})
	if err != nil {
		return fmt.Errorf("update waitlist promoted: %w", err)
	}
	return nil
}

func (t *mysqlTx) InsertOutboxEvent(ctx context.Context, tripID, eventType string, payload []byte) error {
	err := t.q.InsertOutboxEvent(ctx, sqlcgen.InsertOutboxEventParams{
		TripID:    tripID,
		EventType: eventType,
		Payload:   payload,
	})
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
