package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

type Service struct {
	repo Repository
	now  func() time.Time // overridable for tests
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, now: time.Now}
}

// CreateHold locks and holds every seat in seatNos for a trip, atomically:
// either all seats end up held under one new hold, or none do. Seats
// already held or booked by anyone (including the same user) cause the
// whole request to fail with ErrConflict - no partial holds.
func (s *Service) CreateHold(ctx context.Context, tripID, userID string, seatNos []string, ttl time.Duration) (Hold, error) {
	if tripID == "" || userID == "" {
		return Hold{}, fmt.Errorf("create hold: tripID and userID are required")
	}
	seatNos, err := normalizeSeatNos(seatNos)
	if err != nil {
		return Hold{}, fmt.Errorf("create hold: %w", err)
	}
	if ttl <= 0 {
		return Hold{}, fmt.Errorf("create hold: ttl must be positive")
	}

	hold := Hold{
		ID:        uuid.NewString(),
		TripID:    tripID,
		UserID:    userID,
		Status:    HoldActive,
		ExpiresAt: s.now().UTC().Add(ttl),
		SeatNos:   seatNos,
	}

	err = s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		seats, err := tx.LockSeats(ctx, tripID, seatNos)
		if err != nil {
			return fmt.Errorf("lock seats: %w", err)
		}
		if len(seats) != len(seatNos) {
			return fmt.Errorf("create hold: %w: one or more seats do not exist on trip %s", apperrors.ErrNotFound, tripID)
		}
		for _, seat := range seats {
			if seat.Status != SeatAvailable {
				return fmt.Errorf("create hold: %w: seat %s is %s", apperrors.ErrConflict, seat.SeatNo, seat.Status)
			}
		}

		if err := tx.CreateHold(ctx, hold); err != nil {
			return fmt.Errorf("insert hold: %w", err)
		}
		for _, seatNo := range seatNos {
			if err := tx.InsertHoldSeat(ctx, hold.ID, tripID, seatNo); err != nil {
				return fmt.Errorf("insert hold seat %s: %w", seatNo, err)
			}
		}
		if err := tx.MarkSeatsHeld(ctx, tripID, seatNos, hold.ID); err != nil {
			return fmt.Errorf("mark seats held: %w", err)
		}
		return nil
	})
	if err != nil {
		return Hold{}, err
	}
	return hold, nil
}

// ReleaseHold releases an active hold's seats back to available. Releasing
// a hold that is already released or expired is a no-op success
// (idempotent, since compensation paths and the sweeper may both try to
// release the same hold). Releasing a confirmed hold is a conflict: those
// seats are booked, and releasing them would double-book the next holder.
func (s *Service) ReleaseHold(ctx context.Context, holdID string) error {
	var tripID string
	var freedClasses []string
	err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		hold, err := tx.GetHoldForUpdate(ctx, holdID)
		if err != nil {
			return fmt.Errorf("release hold: %w", err)
		}
		switch hold.Status {
		case HoldReleased, HoldExpired:
			return nil
		case HoldConfirmed:
			return fmt.Errorf("release hold %s: %w: hold is confirmed", holdID, apperrors.ErrConflict)
		case HoldActive:
			// fall through
		default:
			return fmt.Errorf("release hold %s: unknown status %q", holdID, hold.Status)
		}

		seats, err := tx.GetHoldSeats(ctx, holdID)
		if err != nil {
			return fmt.Errorf("get hold seats: %w", err)
		}
		seatNos := make([]string, len(seats))
		for i, seat := range seats {
			seatNos[i] = seat.SeatNo
		}

		if _, err := tx.MarkSeatsAvailableFromHold(ctx, hold.TripID, seatNos, holdID); err != nil {
			return fmt.Errorf("mark seats available: %w", err)
		}
		if err := tx.UpdateHoldStatus(ctx, holdID, HoldReleased); err != nil {
			return fmt.Errorf("update hold status: %w", err)
		}
		tripID = hold.TripID
		freedClasses, err = s.emitSeatsFreed(ctx, tx, hold.TripID, seatNos)
		return err
	})
	if err != nil {
		return err
	}
	s.tryPromoteAfterFree(ctx, tripID, freedClasses)
	return nil
}

// ConfirmHold permanently books an active hold's seats under bookingID.
// Called by the booking saga after payment and supplier confirmation both
// succeed. Confirming an already-confirmed hold is idempotent; confirming
// anything else (released, expired) is a conflict.
func (s *Service) ConfirmHold(ctx context.Context, holdID, bookingID string) error {
	if bookingID == "" {
		return fmt.Errorf("confirm hold: bookingID is required")
	}
	return s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		hold, err := tx.GetHoldForUpdate(ctx, holdID)
		if err != nil {
			return fmt.Errorf("confirm hold: %w", err)
		}
		switch hold.Status {
		case HoldConfirmed:
			return nil
		case HoldActive:
			// fall through
		default:
			return fmt.Errorf("confirm hold %s: %w: hold is %s", holdID, apperrors.ErrConflict, hold.Status)
		}

		seats, err := tx.GetHoldSeats(ctx, holdID)
		if err != nil {
			return fmt.Errorf("get hold seats: %w", err)
		}
		seatNos := make([]string, len(seats))
		for i, seat := range seats {
			seatNos[i] = seat.SeatNo
		}

		n, err := tx.MarkSeatsBooked(ctx, hold.TripID, seatNos, holdID, bookingID)
		if err != nil {
			return fmt.Errorf("mark seats booked: %w", err)
		}
		if n != int64(len(seatNos)) {
			return fmt.Errorf("confirm hold %s: expected to book %d seats, booked %d", holdID, len(seatNos), n)
		}
		if err := tx.UpdateHoldStatus(ctx, holdID, HoldConfirmed); err != nil {
			return fmt.Errorf("update hold status: %w", err)
		}
		return nil
	})
}

// CancelBooking releases previously booked seats back to available, e.g.
// on a booking cancellation. Guarded by bookingID so it can't release
// seats belonging to a different booking.
func (s *Service) CancelBooking(ctx context.Context, tripID, bookingID string, seatNos []string) error {
	seatNos, err := normalizeSeatNos(seatNos)
	if err != nil {
		return fmt.Errorf("cancel booking: %w", err)
	}
	var freedClasses []string
	err = s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		n, err := tx.MarkSeatsAvailableFromBooking(ctx, tripID, seatNos, bookingID)
		if err != nil {
			return fmt.Errorf("mark seats available: %w", err)
		}
		if n != int64(len(seatNos)) {
			return fmt.Errorf("cancel booking %s: %w: expected to release %d seats, released %d", bookingID, apperrors.ErrConflict, len(seatNos), n)
		}
		freedClasses, err = s.emitSeatsFreed(ctx, tx, tripID, seatNos)
		return err
	})
	if err != nil {
		return err
	}
	s.tryPromoteAfterFree(ctx, tripID, freedClasses)
	return nil
}

// emitSeatsFreed writes one outbox event per distinct class among seatNos,
// in the same transaction as the seat-freeing update that triggered it,
// and returns those class codes so the caller can also attempt a
// synchronous promotion after commit (see tryPromoteAfterFree). Harmless
// for classes with no pending waitlist entries (every bus class, and any
// train class not currently exhausted) - nothing downstream finds
// anything to do.
func (s *Service) emitSeatsFreed(ctx context.Context, tx TxRepository, tripID string, seatNos []string) ([]string, error) {
	classCodes, err := tx.GetSeatClassCodes(ctx, tripID, seatNos)
	if err != nil {
		return nil, fmt.Errorf("get seat class codes: %w", err)
	}
	for _, classCode := range classCodes {
		payload, err := json.Marshal(seatsFreedPayload{TripID: tripID, ClassCode: classCode})
		if err != nil {
			return nil, fmt.Errorf("marshal seats freed payload: %w", err)
		}
		if err := tx.InsertOutboxEvent(ctx, tripID, eventTypeSeatsFreed, payload); err != nil {
			return nil, fmt.Errorf("insert outbox event: %w", err)
		}
	}
	return classCodes, nil
}

// tryPromoteAfterFree makes a synchronous, best-effort attempt to promote
// the waitlist head for each freed class right after the seat-freeing
// transaction commits - purely a latency optimization so a promotion can
// happen without waiting for the outbox relay's poll interval. The outbox
// event inserted inside that same transaction is the durable source of
// truth: if this process crashes right here, RunOutboxRelay and the
// promotion consumer will still promote the same entry later, exactly
// once, thanks to GetWaitlistHeadForUpdate's row lock.
func (s *Service) tryPromoteAfterFree(ctx context.Context, tripID string, classCodes []string) {
	for _, classCode := range classCodes {
		if _, err := s.PromoteWaitlistHead(ctx, tripID, classCode); err != nil {
			slog.Default().Error("best-effort waitlist promotion failed", "trip_id", tripID, "class_code", classCode, "error", err)
		}
	}
}

const eventTypeSeatsFreed = "seats_freed"

type seatsFreedPayload struct {
	TripID    string `json:"trip_id"`
	ClassCode string `json:"class_code"`
}

// CreateHoldByClass atomically either holds seatCount seats of classCode
// on tripID (letting inventory pick which ones) or, if fewer than
// seatCount are available, queues one waitlist entry instead. Both
// branches happen inside the same transaction as the availability check,
// so no other request can interleave between "not enough seats" and
// "queue the entry" - unlike CreateHold, this never returns ErrConflict
// for quota exhaustion; joining the waitlist is a normal outcome.
func (s *Service) CreateHoldByClass(ctx context.Context, tripID, classCode, userID string, seatCount int, amountPaise int64, ttl time.Duration) (HoldOutcome, error) {
	if tripID == "" || classCode == "" || userID == "" {
		return HoldOutcome{}, fmt.Errorf("create hold by class: tripID, classCode and userID are required")
	}
	if seatCount <= 0 {
		return HoldOutcome{}, fmt.Errorf("create hold by class: seatCount must be positive")
	}
	if ttl <= 0 {
		return HoldOutcome{}, fmt.Errorf("create hold by class: ttl must be positive")
	}

	var outcome HoldOutcome
	err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		seatNos, err := tx.LockAvailableSeatsByClass(ctx, tripID, classCode, seatCount)
		if err != nil {
			return fmt.Errorf("lock available seats by class: %w", err)
		}

		if len(seatNos) < seatCount {
			entry := WaitlistEntry{
				TripID:      tripID,
				ClassCode:   classCode,
				UserID:      userID,
				SeatCount:   seatCount,
				AmountPaise: amountPaise,
				Status:      WaitlistPending,
			}
			id, err := tx.InsertWaitlistEntry(ctx, entry)
			if err != nil {
				return fmt.Errorf("insert waitlist entry: %w", err)
			}
			entry.ID = id
			outcome = HoldOutcome{Waitlist: &entry}
			return nil
		}

		hold := Hold{
			ID:        uuid.NewString(),
			TripID:    tripID,
			UserID:    userID,
			Status:    HoldActive,
			ExpiresAt: s.now().UTC().Add(ttl),
			SeatNos:   seatNos,
		}
		if err := tx.CreateHold(ctx, hold); err != nil {
			return fmt.Errorf("insert hold: %w", err)
		}
		for _, seatNo := range seatNos {
			if err := tx.InsertHoldSeat(ctx, hold.ID, tripID, seatNo); err != nil {
				return fmt.Errorf("insert hold seat %s: %w", seatNo, err)
			}
		}
		if err := tx.MarkSeatsHeld(ctx, tripID, seatNos, hold.ID); err != nil {
			return fmt.Errorf("mark seats held: %w", err)
		}
		outcome = HoldOutcome{Hold: &hold}
		return nil
	})
	if err != nil {
		return HoldOutcome{}, err
	}
	return outcome, nil
}

// PromoteWaitlistHead checks whether enough seats are now free in
// (tripID, classCode) to satisfy the oldest pending waitlist entry, and if
// so, atomically creates a real hold for it and marks it promoted. No
// pending entry, or not enough seats yet, is a no-op success - the next
// seats-freed event (or another cancellation) will retry. The waitlist
// head is locked for the whole attempt, so two concurrent promotion
// attempts for the same (trip, class) can never promote the same entry
// twice, and can never race each other onto the same freed seats.
func (s *Service) PromoteWaitlistHead(ctx context.Context, tripID, classCode string) (promoted bool, err error) {
	err = s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		entry, ok, err := tx.GetWaitlistHeadForUpdate(ctx, tripID, classCode)
		if err != nil {
			return fmt.Errorf("get waitlist head: %w", err)
		}
		if !ok {
			return nil
		}

		seatNos, err := tx.LockAvailableSeatsByClass(ctx, tripID, classCode, entry.SeatCount)
		if err != nil {
			return fmt.Errorf("lock available seats by class: %w", err)
		}
		if len(seatNos) < entry.SeatCount {
			return nil
		}

		hold := Hold{
			ID:        uuid.NewString(),
			TripID:    tripID,
			UserID:    entry.UserID,
			Status:    HoldActive,
			ExpiresAt: s.now().UTC().Add(waitlistPromotionHoldTTL),
			SeatNos:   seatNos,
		}
		if err := tx.CreateHold(ctx, hold); err != nil {
			return fmt.Errorf("insert hold: %w", err)
		}
		for _, seatNo := range seatNos {
			if err := tx.InsertHoldSeat(ctx, hold.ID, tripID, seatNo); err != nil {
				return fmt.Errorf("insert hold seat %s: %w", seatNo, err)
			}
		}
		if err := tx.MarkSeatsHeld(ctx, tripID, seatNos, hold.ID); err != nil {
			return fmt.Errorf("mark seats held: %w", err)
		}
		if err := tx.UpdateWaitlistPromoted(ctx, entry.ID, hold.ID); err != nil {
			return fmt.Errorf("update waitlist promoted: %w", err)
		}
		promoted = true
		return nil
	})
	return promoted, err
}

// waitlistPromotionHoldTTL is longer than the regular booking-flow holdTTL
// (defined in booking's service package, 8 minutes): a promoted user isn't
// mid-checkout when this fires, they need time to notice and come back to
// pay. Inventory's own sweeper reclaims it exactly like any other expired
// hold if they never do.
const waitlistPromotionHoldTTL = 30 * time.Minute

// ClaimPromotedWaitlist returns the seat numbers of a promoted waitlist
// entry's hold, so a caller (booking) can construct a booking directly at
// the Held step. Returns ErrConflict if the entry isn't promoted yet, or
// doesn't belong to userID. This is a plain read, not a mutation - safe to
// call repeatedly; idempotency for "don't create two bookings from one
// promotion" is booking's responsibility (keyed on hold_id).
func (s *Service) ClaimPromotedWaitlist(ctx context.Context, waitlistEntryID int64, userID string) (holdID string, seatNos []string, err error) {
	entry, err := s.repo.GetWaitlistEntry(ctx, waitlistEntryID)
	if err != nil {
		return "", nil, fmt.Errorf("claim promoted waitlist: %w", err)
	}
	if entry.UserID != userID {
		return "", nil, fmt.Errorf("claim promoted waitlist %d: %w: does not belong to user", waitlistEntryID, apperrors.ErrConflict)
	}
	if entry.Status != WaitlistPromoted || entry.PromotedHoldID == "" {
		return "", nil, fmt.Errorf("claim promoted waitlist %d: %w: not yet promoted (status %s)", waitlistEntryID, apperrors.ErrConflict, entry.Status)
	}
	seatNos, err = s.repo.GetHoldSeatNos(ctx, entry.PromotedHoldID)
	if err != nil {
		return "", nil, fmt.Errorf("claim promoted waitlist: %w", err)
	}
	return entry.PromotedHoldID, seatNos, nil
}

// ListWaitlistByUser is a read-only lookup for a "my waitlist" view.
func (s *Service) ListWaitlistByUser(ctx context.Context, userID string) ([]WaitlistEntry, error) {
	if userID == "" {
		return nil, fmt.Errorf("list waitlist by user: userID is required")
	}
	return s.repo.ListWaitlistEntriesByUser(ctx, userID)
}

// SweepExpiredHolds releases up to batchSize holds that are still active
// past their expiry. A hold that was confirmed in the narrow window
// between being listed and being released is left alone - ReleaseHold's
// own row lock and status check make that race safe, it just surfaces as
// ErrConflict here, which the sweeper treats as "not expired after all",
// not a failure.
// ListAvailableSeats returns seat numbers a caller could try to hold
// right now. It is a snapshot, not a reservation - see the interface doc.
func (s *Service) ListAvailableSeats(ctx context.Context, tripID, classCode string) ([]string, error) {
	if tripID == "" || classCode == "" {
		return nil, fmt.Errorf("list available seats: tripID and classCode are required")
	}
	return s.repo.ListAvailableSeats(ctx, tripID, classCode)
}

func (s *Service) SweepExpiredHolds(ctx context.Context, batchSize int) (int, error) {
	expired, err := s.repo.ListExpiredActiveHolds(ctx, s.now().UTC(), batchSize)
	if err != nil {
		return 0, fmt.Errorf("list expired holds: %w", err)
	}

	released := 0
	for _, h := range expired {
		if err := s.ReleaseHold(ctx, h.ID); err != nil {
			if isConflict(err) {
				continue
			}
			return released, fmt.Errorf("release expired hold %s: %w", h.ID, err)
		}
		released++
	}
	return released, nil
}

func isConflict(err error) bool {
	return errors.Is(err, apperrors.ErrConflict)
}

// normalizeSeatNos validates and sorts seat numbers. Sorting here is
// belt-and-suspenders on top of the DB's own ORDER BY seat_no FOR UPDATE:
// it makes hold_seats insertion order deterministic too.
func normalizeSeatNos(seatNos []string) ([]string, error) {
	if len(seatNos) == 0 {
		return nil, fmt.Errorf("at least one seat is required")
	}
	seen := make(map[string]bool, len(seatNos))
	out := make([]string, len(seatNos))
	copy(out, seatNos)
	sort.Strings(out)
	for _, sn := range out {
		if sn == "" {
			return nil, fmt.Errorf("seat number must not be empty")
		}
		if seen[sn] {
			return nil, fmt.Errorf("duplicate seat number %q", sn)
		}
		seen[sn] = true
	}
	return out, nil
}
