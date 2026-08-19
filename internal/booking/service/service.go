package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

const holdTTL = 8 * time.Minute

type Service struct {
	repo      Repository
	inventory InventoryClient
	payment   PaymentClient
	adapters  map[Mode]SupplierAdapter
	logger    *slog.Logger
}

func NewService(repo Repository, inventory InventoryClient, payment PaymentClient, adapters map[Mode]SupplierAdapter, logger *slog.Logger) *Service {
	return &Service{repo: repo, inventory: inventory, payment: payment, adapters: adapters, logger: logger}
}

// CreateBooking starts a new booking and drives it as far forward as the
// synchronous request path can take it. If a step fails for an infra
// reason (not a definitive decline), the booking is left in its current
// non-terminal status for the reconciliation sweeper to resume later -
// CreateBooking returns that error to the caller, but the booking is not
// lost or abandoned.
func (s *Service) CreateBooking(ctx context.Context, userID, tripID string, seatNos []string, mode Mode, amountPaise money.Paise) (Booking, error) {
	if userID == "" || tripID == "" {
		return Booking{}, fmt.Errorf("create booking: userID and tripID are required")
	}
	if len(seatNos) == 0 {
		return Booking{}, fmt.Errorf("create booking: at least one seat is required")
	}
	if amountPaise <= 0 {
		return Booking{}, fmt.Errorf("create booking: amountPaise must be positive")
	}
	if _, ok := s.adapters[mode]; !ok {
		return Booking{}, fmt.Errorf("create booking: no supplier adapter registered for mode %q", mode)
	}

	booking := Booking{
		ID:          uuid.NewString(),
		UserID:      userID,
		TripID:      tripID,
		Mode:        mode,
		SeatNos:     seatNos,
		AmountPaise: amountPaise,
		Status:      StatusPending,
	}
	if err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		return tx.InsertBooking(ctx, booking)
	}); err != nil {
		return Booking{}, fmt.Errorf("create booking: %w", err)
	}

	return s.advance(ctx, booking)
}

// CreateTrainBooking is train mode's saga entry point. It differs from
// CreateBooking only in how the initial hold is acquired: instead of the
// caller naming explicit seatNos, inventory atomically either picks
// seatCount seats of classCode itself or queues a waitlist entry if the
// class is exhausted (never a partial hold, never an error for
// exhaustion - see inventory's CreateHoldByClass). A real hold flows
// straight into the exact same advance() machinery CreateBooking uses;
// advance(), doHold, doCharge, doConfirm and doCompensate are untouched by
// train mode entirely. A waitlisted outcome creates no booking row.
func (s *Service) CreateTrainBooking(ctx context.Context, userID, tripID, classCode string, seatCount int, amountPaise money.Paise) (TrainBookingOutcome, error) {
	if userID == "" || tripID == "" || classCode == "" {
		return TrainBookingOutcome{}, fmt.Errorf("create train booking: userID, tripID and classCode are required")
	}
	if seatCount <= 0 {
		return TrainBookingOutcome{}, fmt.Errorf("create train booking: seatCount must be positive")
	}
	if amountPaise <= 0 {
		return TrainBookingOutcome{}, fmt.Errorf("create train booking: amountPaise must be positive")
	}
	if _, ok := s.adapters[ModeTrain]; !ok {
		return TrainBookingOutcome{}, fmt.Errorf("create train booking: no supplier adapter registered for train")
	}

	holdID, seatNos, waitlistEntryID, err := s.inventory.CreateHoldByClass(ctx, tripID, classCode, userID, seatCount, amountPaise, holdTTL)
	if err != nil {
		return TrainBookingOutcome{}, fmt.Errorf("create train booking: %w", err)
	}
	if holdID == "" {
		return TrainBookingOutcome{WaitlistEntryID: waitlistEntryID}, nil
	}

	booking, err := s.insertHeldBookingAndAdvance(ctx, uuid.NewString(), userID, tripID, ModeTrain, seatNos, amountPaise, holdID, "")
	if err != nil {
		return TrainBookingOutcome{}, err
	}
	return TrainBookingOutcome{Booking: &booking}, nil
}

// CreateFlightBooking is flight mode's saga entry point. Flight owns no
// local inventory (CLAUDE.md: "Provider owns it... No local lock
// possible. External hold") - the pre-payment reservation happens
// entirely with the supplier via the flight adapter's Hold method
// (FlightHolder), never via InventoryClient. A successful external hold
// flows into the exact same advance() machinery every other booking
// uses from the Held step forward - doCharge, doConfirm, and
// doCompensate need only the small hold-type gates already in place to
// accommodate it (see releaseHold); advance() itself is untouched.
//
// Unlike bus/train, the caller never names seats - flight has none under
// our control, only a passenger count. SeatNos is filled with synthetic
// per-passenger placeholders purely so the column stays NOT NULL and
// consistent in shape with every other mode's booking row.
func (s *Service) CreateFlightBooking(ctx context.Context, userID, tripID string, seatCount int, amountPaise money.Paise) (Booking, error) {
	if userID == "" || tripID == "" {
		return Booking{}, fmt.Errorf("create flight booking: userID and tripID are required")
	}
	if seatCount <= 0 {
		return Booking{}, fmt.Errorf("create flight booking: seatCount must be positive")
	}
	if amountPaise <= 0 {
		return Booking{}, fmt.Errorf("create flight booking: amountPaise must be positive")
	}
	holder, ok := s.adapters[ModeFlight].(FlightHolder)
	if !ok {
		return Booking{}, fmt.Errorf("create flight booking: no flight-capable supplier adapter registered")
	}

	bookingID := uuid.NewString()
	holdRef, err := holder.Hold(ctx, bookingID, tripID, seatCount)
	if err != nil {
		return Booking{}, fmt.Errorf("create flight booking: %w", err)
	}

	return s.insertHeldBookingAndAdvance(ctx, bookingID, userID, tripID, ModeFlight, syntheticPassengerSeats(seatCount), amountPaise, "", holdRef)
}

func syntheticPassengerSeats(count int) []string {
	seats := make([]string, count)
	for i := range seats {
		seats[i] = fmt.Sprintf("PAX-%d", i+1)
	}
	return seats
}

// CreateBookingFromWaitlist claims a promoted waitlist entry's hold and
// builds a booking directly at the Held step, same as CreateTrainBooking's
// happy path - advance() takes it from there. Idempotent by hold_id: if a
// booking already exists for this hold (an earlier call already claimed
// and inserted it), that booking is re-advanced instead of a duplicate
// being created, so a retried "pay now" click can never double-book.
func (s *Service) CreateBookingFromWaitlist(ctx context.Context, waitlistEntryID int64, userID, tripID string, amountPaise money.Paise) (Booking, error) {
	if userID == "" || tripID == "" {
		return Booking{}, fmt.Errorf("create booking from waitlist: userID and tripID are required")
	}

	holdID, seatNos, err := s.inventory.ClaimPromotedWaitlist(ctx, waitlistEntryID, userID)
	if err != nil {
		return Booking{}, fmt.Errorf("create booking from waitlist: %w", err)
	}

	existing, ok, err := s.repo.GetBookingByHoldID(ctx, holdID)
	if err != nil {
		return Booking{}, fmt.Errorf("create booking from waitlist: %w", err)
	}
	if ok {
		return s.advance(ctx, existing)
	}

	return s.insertHeldBookingAndAdvance(ctx, uuid.NewString(), userID, tripID, ModeTrain, seatNos, amountPaise, holdID, "")
}

// insertHeldBookingAndAdvance is the shared tail of every entry point
// above that starts from an already-acquired hold: a booking row is
// inserted directly at StatusHeld with holdID or externalHoldRef already
// set (InsertHeldBooking, not InsertBooking - exactly one of the two is
// non-empty), then handed to the same advance() every booking uses to
// reach a terminal status.
func (s *Service) insertHeldBookingAndAdvance(ctx context.Context, id, userID, tripID string, mode Mode, seatNos []string, amountPaise money.Paise, holdID, externalHoldRef string) (Booking, error) {
	booking := Booking{
		ID:              id,
		UserID:          userID,
		TripID:          tripID,
		Mode:            mode,
		SeatNos:         seatNos,
		AmountPaise:     amountPaise,
		HoldID:          holdID,
		ExternalHoldRef: externalHoldRef,
		Status:          StatusHeld,
	}
	if err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		return tx.InsertHeldBooking(ctx, booking)
	}); err != nil {
		return Booking{}, fmt.Errorf("insert held booking: %w", err)
	}
	return s.advance(ctx, booking)
}

// CancelBooking cancels a confirmed booking: refunds the payment, releases
// the booked seats, and cancels the supplier PNR. Idempotent - cancelling
// an already-cancelled booking is a no-op success.
func (s *Service) CancelBooking(ctx context.Context, bookingID string) (Booking, error) {
	booking, err := s.repo.GetBooking(ctx, bookingID)
	if err != nil {
		return Booking{}, fmt.Errorf("cancel booking: %w", err)
	}
	switch booking.Status {
	case StatusCancelled:
		return booking, nil
	case StatusConfirmed:
		// fall through to compensation below
	default:
		return booking, fmt.Errorf("cancel booking %s: %w: only a confirmed booking can be cancelled (status is %s)", bookingID, apperrors.ErrConflict, booking.Status)
	}

	booking, err = s.transition(ctx, bookingID, StatusConfirmed, func(tx TxRepository, current Booking) (Booking, error) {
		if err := tx.UpdateBookingRefunding(ctx, bookingID, "user requested cancellation"); err != nil {
			return Booking{}, err
		}
		current.Status = StatusRefunding
		current.FailureReason = "user requested cancellation"
		return current, nil
	})
	if err != nil {
		return booking, fmt.Errorf("cancel booking: %w", err)
	}

	return s.advance(ctx, booking)
}

func (s *Service) GetBooking(ctx context.Context, bookingID string) (Booking, error) {
	return s.repo.GetBooking(ctx, bookingID)
}

// advance drives booking forward from its current status toward a
// terminal one (confirmed, failed, or cancelled), re-running whichever
// steps haven't completed yet. Called both by the synchronous request
// path and by the reconciliation sweeper - identical logic either way,
// which is what makes a stuck booking resumable instead of needing
// separate crash-recovery code.
func (s *Service) advance(ctx context.Context, booking Booking) (Booking, error) {
	switch booking.Status {
	case StatusPending:
		var err error
		booking, err = s.doHold(ctx, booking)
		if err != nil || booking.Status != StatusHeld {
			return booking, err
		}
		fallthrough
	case StatusHeld:
		var err error
		booking, err = s.doCharge(ctx, booking)
		if err != nil || booking.Status != StatusPaid {
			return booking, err
		}
		fallthrough
	case StatusPaid:
		var err error
		booking, err = s.doConfirm(ctx, booking)
		if err != nil {
			return booking, err
		}
		if booking.Status == StatusConfirmed {
			return booking, nil
		}
		fallthrough
	case StatusRefunding:
		return s.doCompensate(ctx, booking)
	case StatusConfirmed, StatusCancelled, StatusFailed:
		return booking, nil
	default:
		return booking, fmt.Errorf("advance booking %s: unknown status %q", booking.ID, booking.Status)
	}
}

// doHold acquires the seat hold. See the package doc for why this step,
// alone among the saga's steps, doesn't need to be idempotent-by-key.
//
// Because CreateHold isn't idempotent-by-key, two concurrent doHold calls
// for the same still-Pending booking (synchronous path racing the
// sweeper) can diverge at the inventory level: one wins the seat lock and
// succeeds, the other gets ErrConflict from inventory even though, from
// booking's perspective, both are "the same" attempt. If that happens,
// the loser's own CreateHold call failed, but the booking itself is
// fine - transition below will find status already moved to Held and
// return that instead of applying the loser's failure, and the check
// after it turns that into a non-error so advance() continues normally.
func (s *Service) doHold(ctx context.Context, booking Booking) (Booking, error) {
	holdID, err := s.inventory.CreateHold(ctx, booking.TripID, booking.UserID, booking.SeatNos, holdTTL)
	if err != nil {
		reason := err.Error()
		updated, txErr := s.transition(ctx, booking.ID, StatusPending, func(tx TxRepository, current Booking) (Booking, error) {
			if e := tx.UpdateBookingFailed(ctx, booking.ID, reason); e != nil {
				return Booking{}, e
			}
			current.Status = StatusFailed
			current.FailureReason = reason
			if e := s.emitEvent(ctx, tx, booking.ID, "booking.failed", eventPayload(current, reason)); e != nil {
				return Booking{}, e
			}
			return current, nil
		})
		if txErr != nil {
			return booking, fmt.Errorf("hold seats: %w (also failed to persist failure: %v)", err, txErr)
		}
		if updated.Status != StatusFailed {
			// A concurrent doHold call already moved this booking past
			// Pending (it won the seat lock); our own failure was a
			// no-op. The booking is fine - report success.
			return updated, nil
		}
		return updated, fmt.Errorf("hold seats: %w", err)
	}

	return s.transition(ctx, booking.ID, StatusPending, func(tx TxRepository, current Booking) (Booking, error) {
		if err := tx.UpdateBookingHeld(ctx, booking.ID, holdID); err != nil {
			return Booking{}, err
		}
		current.HoldID = holdID
		current.Status = StatusHeld
		return current, nil
	})
}

// doCharge charges the booking. A gateway decline releases the hold
// immediately and fails the booking - there is no payment to refund. An
// infra error leaves the booking Held for a later retry.
func (s *Service) doCharge(ctx context.Context, booking Booking) (Booking, error) {
	paymentID, approved, err := s.payment.Charge(ctx, booking.ID, booking.ID, booking.UserID, booking.AmountPaise)
	if err != nil {
		return booking, fmt.Errorf("charge: %w", err)
	}
	if !approved {
		reason := "payment declined"
		if relErr := s.releaseHold(ctx, booking); relErr != nil {
			s.logger.Error("release hold after declined payment failed", "booking_id", booking.ID, "hold_id", booking.HoldID, "external_hold_ref", booking.ExternalHoldRef, "error", relErr)
		}
		updated, txErr := s.transition(ctx, booking.ID, StatusHeld, func(tx TxRepository, current Booking) (Booking, error) {
			if e := tx.UpdateBookingFailed(ctx, booking.ID, reason); e != nil {
				return Booking{}, e
			}
			current.Status = StatusFailed
			current.FailureReason = reason
			if e := s.emitEvent(ctx, tx, booking.ID, "booking.failed", eventPayload(current, reason)); e != nil {
				return Booking{}, e
			}
			return current, nil
		})
		if txErr != nil {
			return booking, fmt.Errorf("payment declined (also failed to persist failure: %w)", txErr)
		}
		return updated, fmt.Errorf("payment declined")
	}

	return s.transition(ctx, booking.ID, StatusHeld, func(tx TxRepository, current Booking) (Booking, error) {
		if err := tx.UpdateBookingPaid(ctx, booking.ID, paymentID); err != nil {
			return Booking{}, err
		}
		current.PaymentID = paymentID
		current.Status = StatusPaid
		return current, nil
	})
}

// doConfirm has two idempotent sub-steps: confirm with the external
// supplier, then confirm the hold locally so the seats become
// permanently booked. Either sub-step failing after payment has already
// succeeded transitions to Refunding - the hard case. An infra error in
// either sub-step leaves the booking Paid (with SupplierPNR recorded if
// that sub-step already completed) for a later retry.
func (s *Service) doConfirm(ctx context.Context, booking Booking) (Booking, error) {
	if booking.SupplierPNR == "" {
		adapter := s.adapters[booking.Mode]
		pnr, err := adapter.Confirm(ctx, booking.ID, booking.TripID, booking.SeatNos)
		if err != nil {
			if errors.Is(err, apperrors.ErrUnavailable) {
				return s.beginRefund(ctx, booking, "supplier declined confirmation: "+err.Error())
			}
			return booking, fmt.Errorf("supplier confirm: %w", err)
		}

		updated, err := s.transition(ctx, booking.ID, StatusPaid, func(tx TxRepository, current Booking) (Booking, error) {
			if e := tx.UpdateBookingSupplierPNR(ctx, booking.ID, pnr); e != nil {
				return Booking{}, e
			}
			current.SupplierPNR = pnr
			return current, nil
		})
		if err != nil {
			return booking, fmt.Errorf("persist supplier pnr: %w", err)
		}
		booking = updated
	}

	// Only bus/train have a local inventory hold to confirm - flight's
	// HoldID is always empty (see ExternalHoldRef), and its reservation
	// is already permanent the moment the supplier issues SupplierPNR
	// above, so there is nothing further to confirm locally.
	if booking.HoldID != "" {
		if err := s.inventory.ConfirmHold(ctx, booking.HoldID, booking.ID); err != nil {
			if errors.Is(err, apperrors.ErrConflict) {
				return s.beginRefund(ctx, booking, "confirm hold failed: "+err.Error())
			}
			return booking, fmt.Errorf("confirm hold: %w", err)
		}
	}

	pnr := generatePNR()
	return s.transition(ctx, booking.ID, StatusPaid, func(tx TxRepository, current Booking) (Booking, error) {
		if e := tx.UpdateBookingConfirmed(ctx, booking.ID, pnr); e != nil {
			return Booking{}, e
		}
		current.PNR = pnr
		current.Status = StatusConfirmed
		if e := s.emitEvent(ctx, tx, booking.ID, "booking.confirmed", eventPayload(current, "")); e != nil {
			return Booking{}, e
		}
		return current, nil
	})
}

// releaseHold releases whichever kind of pre-payment hold a booking has:
// an inventory hold (bus/train, via InventoryClient) or an external
// supplier-side hold (flight, via the adapter's optional HoldReleaser -
// see domain.go). A booking with neither set (e.g. one that never got
// past Pending) has nothing to release and this is a no-op.
func (s *Service) releaseHold(ctx context.Context, booking Booking) error {
	switch {
	case booking.HoldID != "":
		return s.inventory.ReleaseHold(ctx, booking.HoldID)
	case booking.ExternalHoldRef != "":
		releaser, ok := s.adapters[booking.Mode].(HoldReleaser)
		if !ok {
			return fmt.Errorf("release hold: supplier adapter for mode %q does not support HoldReleaser", booking.Mode)
		}
		return releaser.ReleaseHold(ctx, booking.ExternalHoldRef)
	default:
		return nil
	}
}

// beginRefund only transitions the booking to Refunding - it does not
// call doCompensate itself. advance()'s StatusPaid case falls through to
// its StatusRefunding case, which is what actually runs doCompensate.
// Compensating here too would run it twice for the synchronous path (once
// from this call, once from advance()'s fallthrough).
func (s *Service) beginRefund(ctx context.Context, booking Booking, reason string) (Booking, error) {
	updated, err := s.transition(ctx, booking.ID, StatusPaid, func(tx TxRepository, current Booking) (Booking, error) {
		if e := tx.UpdateBookingRefunding(ctx, booking.ID, reason); e != nil {
			return Booking{}, e
		}
		current.Status = StatusRefunding
		current.FailureReason = reason
		return current, nil
	})
	if err != nil {
		return booking, fmt.Errorf("begin refund: %w", err)
	}
	return updated, nil
}

// doCompensate refunds the payment, releases the seats (booked or held,
// depending on how far the saga got), and cancels the supplier PNR if one
// was issued. Every sub-call is idempotent, so a partially-completed
// compensation (crash mid-compensate) is safe to re-run from the top.
func (s *Service) doCompensate(ctx context.Context, booking Booking) (Booking, error) {
	if booking.PaymentID != "" {
		if err := s.payment.Refund(ctx, booking.ID+"-refund", booking.PaymentID, booking.AmountPaise); err != nil {
			return booking, fmt.Errorf("compensate: refund: %w", err)
		}
	}

	if booking.PNR != "" {
		// The booking previously reached Confirmed (this is a voluntary
		// cancellation). Bus/train seats are booked in local inventory
		// and must be explicitly released; flight has none - releasing
		// the ticketed reservation is entirely the adapter.Cancel call
		// below, keyed on SupplierPNR.
		if booking.HoldID != "" {
			if err := s.inventory.CancelBooking(ctx, booking.TripID, booking.ID, booking.SeatNos); err != nil {
				return booking, fmt.Errorf("compensate: cancel booked seats: %w", err)
			}
		}
	} else if err := s.releaseHold(ctx, booking); err != nil {
		return booking, fmt.Errorf("compensate: release hold: %w", err)
	}

	if booking.SupplierPNR != "" {
		adapter := s.adapters[booking.Mode]
		if err := adapter.Cancel(ctx, booking.SupplierPNR); err != nil {
			return booking, fmt.Errorf("compensate: cancel supplier pnr: %w", err)
		}
	}

	return s.transition(ctx, booking.ID, StatusRefunding, func(tx TxRepository, current Booking) (Booking, error) {
		if e := tx.UpdateBookingCancelled(ctx, booking.ID); e != nil {
			return Booking{}, e
		}
		current.Status = StatusCancelled
		if e := s.emitEvent(ctx, tx, booking.ID, "booking.cancelled", eventPayload(current, booking.FailureReason)); e != nil {
			return Booking{}, e
		}
		return current, nil
	})
}

// transition locks the booking row; if its status is exactly from, it
// applies mutate (which persists the change and returns the resulting
// Booking) inside the same transaction. If the status has already moved
// past from - a concurrent advance() call (synchronous path vs. the
// sweeper) already applied this step - it returns the CURRENT persisted
// booking unchanged.
//
// Callers must use the returned Booking, not their own pre-call copy: on
// the no-op path, a caller's locally computed result (e.g. a freshly
// generated PNR that lost the race) was never persisted, and returning it
// anyway would hand back state that doesn't match the database.
func (s *Service) transition(ctx context.Context, bookingID string, from Status, mutate func(tx TxRepository, current Booking) (Booking, error)) (Booking, error) {
	var result Booking
	err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		current, err := tx.GetBookingForUpdate(ctx, bookingID)
		if err != nil {
			return err
		}
		if current.Status != from {
			result = current
			return nil
		}
		updated, err := mutate(tx, current)
		if err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *Service) emitEvent(ctx context.Context, tx TxRepository, bookingID, eventType string, payload []byte) error {
	return tx.InsertOutboxEvent(ctx, bookingID, eventType, payload)
}

type bookingEventPayload struct {
	BookingID string `json:"booking_id"`
	PNR       string `json:"pnr,omitempty"`
	UserID    string `json:"user_id"`
	TripID    string `json:"trip_id"`
	Status    Status `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

func eventPayload(b Booking, reason string) []byte {
	p := bookingEventPayload{
		BookingID: b.ID,
		PNR:       b.PNR,
		UserID:    b.UserID,
		TripID:    b.TripID,
		Status:    b.Status,
		Reason:    reason,
	}
	data, err := json.Marshal(p)
	if err != nil {
		// bookingEventPayload has no types that can fail to marshal.
		panic(fmt.Errorf("marshal event payload: %w", err))
	}
	return data
}

const pnrAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func generatePNR() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("booking: generate pnr: %w", err))
	}
	pnr := make([]byte, 6)
	for i, b := range buf {
		pnr[i] = pnrAlphabet[int(b)%len(pnrAlphabet)]
	}
	return string(pnr)
}
