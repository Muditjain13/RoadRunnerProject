// Package service is the booking saga orchestrator. The saga is:
//
//	pending -> held -> paid -> confirmed
//	                      \-> refunding -> cancelled   (hard case / voluntary cancel)
//	          \-> failed                                (hold or payment failed early)
//
// Every step past the initial hold (Charge, supplier Confirm, inventory
// ConfirmHold, Refund, ReleaseHold, supplier Cancel) is idempotent on a
// key derived from the booking ID - proven in inventory, payment, and
// suppliersim's own tests. That is what makes this saga resumable: the
// reconciliation sweeper does not need special "what happened while I was
// down" recovery logic, it just re-runs advance() on a stale booking, and
// every external call advance() might repeat is safe to repeat.
//
// The one step that is NOT idempotent-by-key is the very first CreateHold
// call. That is fine by design, not an oversight: if the process dies
// between inventory creating the hold and this service recording its ID,
// the orphaned hold self-heals via inventory's existing TTL sweeper
// (docs/adr/0001-hold-expiry-sweeper.md) - within the hold's TTL, the seat
// is released with no compensation needed on booking's side. See
// docs/adr/0002-resumable-saga.md.
package service

import (
	"context"
	"time"

	"github.com/Muditjain13/transport-app/pkg/money"
)

type Mode string

const (
	ModeBus    Mode = "bus"
	ModeTrain  Mode = "train"
	ModeFlight Mode = "flight"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusHeld      Status = "held"
	StatusPaid      Status = "paid"
	StatusConfirmed Status = "confirmed"
	StatusRefunding Status = "refunding"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Booking struct {
	ID              string
	PNR             string // ours, issued only once Confirmed
	SupplierPNR     string // the external supplier's confirmation code
	UserID          string
	TripID          string
	Mode            Mode
	SeatNos         []string
	AmountPaise     money.Paise
	HoldID          string // an inventory hold - bus/train only
	ExternalHoldRef string // a pre-payment supplier-side hold - flight only, never both this and HoldID
	PaymentID       string
	Status          Status
	FailureReason   string
}

// TrainBookingOutcome is CreateTrainBooking's result: exactly one of
// Booking or WaitlistEntryID is set. A waitlisted request creates no
// booking row at all - "my waitlist" is inventory's own view (see
// ListWaitlistByUser), not booking's.
type TrainBookingOutcome struct {
	Booking         *Booking
	WaitlistEntryID int64
}

// TxRepository is everything a single saga step needs from one
// transaction. Every mutation booking makes to its own state goes through
// Repository.WithinTx; no transaction is ever held open across a gRPC
// call to another service.
type TxRepository interface {
	InsertBooking(ctx context.Context, b Booking) error
	// InsertHeldBooking inserts a booking directly at StatusHeld with
	// HoldID or ExternalHoldRef already set - used only when the hold
	// (inventory or supplier-side) was created before the booking row
	// (train's atomic class-hold, claiming a promoted waitlist entry, or
	// flight's external hold). advance() then resumes from Held exactly
	// as it would for any other booking; doHold is never called for these.
	InsertHeldBooking(ctx context.Context, b Booking) error
	// GetBookingForUpdate locks the booking row - used at the start of
	// every transition to (a) read the latest state and (b) serialize
	// against a concurrent advance() call on the same booking (from the
	// synchronous path racing the reconciliation sweeper).
	GetBookingForUpdate(ctx context.Context, id string) (Booking, error)

	UpdateBookingHeld(ctx context.Context, id, holdID string) error
	UpdateBookingPaid(ctx context.Context, id, paymentID string) error
	UpdateBookingSupplierPNR(ctx context.Context, id, supplierPNR string) error
	UpdateBookingConfirmed(ctx context.Context, id, pnr string) error
	UpdateBookingRefunding(ctx context.Context, id, reason string) error
	UpdateBookingFailed(ctx context.Context, id, reason string) error
	UpdateBookingCancelled(ctx context.Context, id string) error

	// InsertOutboxEvent writes the event in the same transaction as the
	// status update above it - the transactional outbox, never a
	// separate Kafka publish call (CLAUDE.md: "no dual writes").
	InsertOutboxEvent(ctx context.Context, bookingID, eventType string, payload []byte) error
}

type OutboxEvent struct {
	ID        int64
	BookingID string
	EventType string
	Payload   []byte
}

type Repository interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, tx TxRepository) error) error
	GetBooking(ctx context.Context, id string) (Booking, error)
	// GetBookingByHoldID backs CreateBookingFromWaitlist's idempotency
	// check. ok is false if no booking has been created from this hold
	// yet.
	GetBookingByHoldID(ctx context.Context, holdID string) (booking Booking, ok bool, err error)
	// ListStaleBookings returns booking IDs in one of statuses whose
	// updated_at is older than updatedBefore - the reconciliation
	// sweeper's candidate list.
	ListStaleBookings(ctx context.Context, statuses []Status, updatedBefore time.Time, limit int) ([]string, error)
	ListUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkOutboxEventPublished(ctx context.Context, id int64) error
}

// InventoryClient is booking's view of the inventory service.
type InventoryClient interface {
	CreateHold(ctx context.Context, tripID, userID string, seatNos []string, ttl time.Duration) (holdID string, err error)
	ReleaseHold(ctx context.Context, holdID string) error
	ConfirmHold(ctx context.Context, holdID, bookingID string) error
	CancelBooking(ctx context.Context, tripID, bookingID string, seatNos []string) error

	// CreateHoldByClass atomically either holds seatCount seats of one
	// class on tripID (inventory picks which ones) or, if the class is
	// exhausted, queues a waitlist entry - never a partial hold, never an
	// error for exhaustion. A holdID of "" means waitlisted; waitlistEntryID
	// is then set instead. Used only by CreateTrainBooking.
	CreateHoldByClass(ctx context.Context, tripID, classCode, userID string, seatCount int, amountPaise money.Paise, ttl time.Duration) (holdID string, seatNos []string, waitlistEntryID int64, err error)

	// ClaimPromotedWaitlist returns a promoted waitlist entry's hold and
	// seats. Used only by CreateBookingFromWaitlist.
	ClaimPromotedWaitlist(ctx context.Context, waitlistEntryID int64, userID string) (holdID string, seatNos []string, err error)
}

// PaymentClient is booking's view of the payment service.
type PaymentClient interface {
	// Charge returns the payment ID and whether the gateway approved it.
	// A decline is a normal return (approved=false, err=nil), not an
	// error - only infra failures (network, etc.) are errors.
	Charge(ctx context.Context, idempotencyKey, bookingID, userID string, amountPaise money.Paise) (paymentID string, approved bool, err error)
	Refund(ctx context.Context, idempotencyKey, paymentID string, amountPaise money.Paise) error
}

// SupplierAdapter is the mode-specific external-supplier confirmation
// step. CLAUDE.md section 2: "One booking engine, three adapters" - the
// saga in service.go never branches on Mode; it looks up the adapter for
// booking.Mode and calls this interface. A bus adapter, train adapter, and
// flight adapter can all differ completely in what they wrap, as long as
// they satisfy this shape.
type SupplierAdapter interface {
	// Confirm returns apperrors.ErrUnavailable (wrapped) on a supplier
	// decline - the hard case this saga exists to handle correctly. Any
	// other error is treated as an infra failure: the step is left
	// unresolved for a later retry instead of triggering compensation.
	Confirm(ctx context.Context, bookingID, tripID string, seatNos []string) (supplierPNR string, err error)
	Cancel(ctx context.Context, supplierPNR string) error
}

// FlightHolder is a SupplierAdapter that also owns pre-payment
// reservation (CLAUDE.md: "Provider owns it... No local lock possible.
// External hold"). Bus and train's adapters don't implement this - their
// seats live in local inventory, held before the saga even starts;
// flight's does, discovered via a type assertion on s.adapters[ModeFlight]
// in CreateFlightBooking rather than widening SupplierAdapter itself,
// which would force bus/train to grow a meaningless no-op Hold method.
type FlightHolder interface {
	Hold(ctx context.Context, bookingID, tripID string, seatCount int) (externalHoldRef string, err error)
}

// HoldReleaser releases a FlightHolder's pre-payment hold if the saga
// never reaches Confirm (e.g. payment declined, or voluntary
// cancellation before ticketing). Optional for the same reason
// FlightHolder is: only an adapter that owns an external hold has
// anything here to release.
type HoldReleaser interface {
	ReleaseHold(ctx context.Context, externalHoldRef string) error
}
