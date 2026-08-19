// Package service implements inventory's core correctness property: no
// double-booking. Every seat state transition (available -> held ->
// booked, or back) happens inside one DB transaction that locks the exact
// seat rows involved with SELECT ... FOR UPDATE before checking their
// status. Two concurrent requests for the same seat serialize on that
// lock; the loser sees status != available inside its own transaction and
// fails cleanly instead of racing.
package service

import (
	"context"
	"time"
)

type SeatStatus string

const (
	SeatAvailable SeatStatus = "available"
	SeatHeld      SeatStatus = "held"
	SeatBooked    SeatStatus = "booked"
)

type HoldStatus string

const (
	HoldActive    HoldStatus = "active"
	HoldExpired   HoldStatus = "expired"
	HoldReleased  HoldStatus = "released"
	HoldConfirmed HoldStatus = "confirmed"
)

type WaitlistStatus string

const (
	WaitlistPending   WaitlistStatus = "pending"
	WaitlistPromoted  WaitlistStatus = "promoted"
	WaitlistCancelled WaitlistStatus = "cancelled"
)

type Seat struct {
	TripID    string
	SeatNo    string
	ClassCode string
	Status    SeatStatus
	HoldID    string // empty if not held
	BookingID string // empty if not booked
}

type Hold struct {
	ID        string
	TripID    string
	UserID    string
	Status    HoldStatus
	ExpiresAt time.Time
	SeatNos   []string
}

// SeatRef identifies one seat without its full state - used for hold
// membership and the sweeper's candidate list.
type SeatRef struct {
	TripID string
	SeatNo string
}

// HoldRef is the sweeper's candidate list shape: just enough to attempt a
// release, not the full hold.
type HoldRef struct {
	ID     string
	TripID string
}

// WaitlistEntry is one FIFO queue position for a (trip, class) whose quota
// was exhausted at request time. PromotedHoldID is set once the promotion
// consumer finds enough freed seats to satisfy it.
type WaitlistEntry struct {
	ID             int64
	TripID         string
	ClassCode      string
	UserID         string
	SeatCount      int
	AmountPaise    int64
	Status         WaitlistStatus
	PromotedHoldID string
}

// HoldOutcome is CreateHoldByClass's result: exactly one of Hold or
// Waitlist is set. Waitlist is not an error - it's the normal outcome when
// a train class's quota is exhausted.
type HoldOutcome struct {
	Hold     *Hold
	Waitlist *WaitlistEntry
}

// OutboxEvent is one row of the transactional outbox - see migrations
// 000002_waitlist for why trip_id (not booking_id, inventory has no
// booking concept) is the event's routing key.
type OutboxEvent struct {
	ID        int64
	TripID    string
	EventType string
	Payload   []byte
}

// TxRepository is everything the service needs from a single database
// transaction. Every Service method that mutates seat or hold state runs
// entirely inside one call to Repository.WithinTx.
type TxRepository interface {
	// LockSeats takes SELECT ... FOR UPDATE on the given seats, ordered by
	// seat_no. Returns fewer rows than requested if some seat_no doesn't
	// exist for the trip.
	LockSeats(ctx context.Context, tripID string, seatNos []string) ([]Seat, error)
	MarkSeatsHeld(ctx context.Context, tripID string, seatNos []string, holdID string) error
	// MarkSeatsAvailableFromHold and the other MarkSeats* methods return the
	// number of rows actually updated, guarded by hold_id/booking_id so a
	// stale caller can never undo a state transition that already moved on.
	MarkSeatsAvailableFromHold(ctx context.Context, tripID string, seatNos []string, holdID string) (int64, error)
	MarkSeatsBooked(ctx context.Context, tripID string, seatNos []string, holdID, bookingID string) (int64, error)
	MarkSeatsAvailableFromBooking(ctx context.Context, tripID string, seatNos []string, bookingID string) (int64, error)

	CreateHold(ctx context.Context, hold Hold) error
	InsertHoldSeat(ctx context.Context, holdID, tripID, seatNo string) error
	// GetHoldForUpdate locks the hold row so a concurrent sweep and an
	// explicit release/confirm of the same hold serialize.
	GetHoldForUpdate(ctx context.Context, holdID string) (Hold, error)
	GetHoldSeats(ctx context.Context, holdID string) ([]SeatRef, error)
	UpdateHoldStatus(ctx context.Context, holdID string, status HoldStatus) error

	// LockAvailableSeatsByClass locks (SELECT ... FOR UPDATE) up to count
	// available seats of one class, ordered by seat_no. Returns fewer than
	// count if fewer are available - CreateHoldByClass and
	// PromoteWaitlistHead both treat that as "not enough, do nothing"
	// rather than a partial hold.
	LockAvailableSeatsByClass(ctx context.Context, tripID, classCode string, count int) ([]string, error)
	// GetSeatClassCodes returns the distinct class codes among the given
	// seats - used after freeing seats to know which waitlist queue(s) to
	// notify via the outbox.
	GetSeatClassCodes(ctx context.Context, tripID string, seatNos []string) ([]string, error)
	InsertWaitlistEntry(ctx context.Context, entry WaitlistEntry) (int64, error)
	// GetWaitlistHeadForUpdate locks the oldest pending entry for
	// (tripID, classCode). ok is false if there is none.
	GetWaitlistHeadForUpdate(ctx context.Context, tripID, classCode string) (entry WaitlistEntry, ok bool, err error)
	UpdateWaitlistPromoted(ctx context.Context, id int64, holdID string) error
	// InsertOutboxEvent writes the event in the same transaction as the
	// seat-freeing update above it - the transactional outbox, never a
	// separate Kafka publish call.
	InsertOutboxEvent(ctx context.Context, tripID, eventType string, payload []byte) error
}

// Repository is what the service depends on outside of a transaction, plus
// the entry point into one.
type Repository interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, tx TxRepository) error) error
	// ListExpiredActiveHolds is read-only and deliberately outside a
	// transaction - it just produces candidates for the sweeper. Each
	// candidate is then released through its own WithinTx call, which
	// re-checks the hold's real status under lock.
	ListExpiredActiveHolds(ctx context.Context, before time.Time, limit int) ([]HoldRef, error)
	// ListAvailableSeats is a read-only lookup, not a reservation - the
	// result can go stale the instant it's read. Callers must still go
	// through CreateHold, which is what actually enforces availability.
	ListAvailableSeats(ctx context.Context, tripID, classCode string) ([]string, error)

	// GetWaitlistEntry and GetHoldSeatNos are plain reads used by
	// ClaimPromotedWaitlist - claiming doesn't mutate the entry, so it
	// needs no transaction; idempotency is the caller's (booking's)
	// responsibility, keyed on hold_id.
	GetWaitlistEntry(ctx context.Context, id int64) (WaitlistEntry, error)
	ListWaitlistEntriesByUser(ctx context.Context, userID string) ([]WaitlistEntry, error)
	GetHoldSeatNos(ctx context.Context, holdID string) ([]string, error)

	// ListUnpublishedOutboxEvents/MarkOutboxEventPublished back the outbox
	// relay - see internal/booking/service/outbox.go for the identical
	// pattern.
	ListUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkOutboxEventPublished(ctx context.Context, id int64) error
}
