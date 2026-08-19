package service

import (
	"context"
	"errors"
	"testing"
)

// TestCreateTrainBooking_HappyPath proves the exact claim in CLAUDE.md's
// Phase 3 success criterion: a real hold from CreateHoldByClass flows
// through the same advance() machinery (doCharge, doConfirm) that bus
// bookings use, all the way to Confirmed - with no train-specific branch
// anywhere in advance() itself.
func TestCreateTrainBooking_HappyPath(t *testing.T) {
	h := newHarness()
	h.inventory.createHoldByClassSeatNos = []string{"5", "6"}

	outcome, err := h.svc.CreateTrainBooking(context.Background(), "user-1", "trip-1", "sl", 2, 50000)
	if err != nil {
		t.Fatalf("CreateTrainBooking() error = %v", err)
	}
	if outcome.WaitlistEntryID != 0 {
		t.Fatalf("outcome = %+v, want a Booking, not a waitlist entry", outcome)
	}
	if outcome.Booking == nil {
		t.Fatal("outcome.Booking is nil")
	}
	b := *outcome.Booking
	if b.Status != StatusConfirmed {
		t.Errorf("status = %s, want confirmed", b.Status)
	}
	if b.Mode != ModeTrain {
		t.Errorf("mode = %s, want train", b.Mode)
	}
	if b.PNR == "" {
		t.Error("PNR was not issued")
	}
	if len(b.SeatNos) != 2 || b.SeatNos[0] != "5" {
		t.Errorf("seat nos = %v, want [5 6] (inventory-picked, not caller-picked)", b.SeatNos)
	}
	if h.trainSupplier.confirmN != 1 {
		t.Errorf("train supplier Confirm called %d times, want 1", h.trainSupplier.confirmN)
	}
	if h.supplier.confirmN != 0 {
		t.Errorf("bus supplier Confirm called %d times, want 0", h.supplier.confirmN)
	}
}

func TestCreateTrainBooking_WaitlistedCreatesNoBookingRow(t *testing.T) {
	h := newHarness()
	h.inventory.createHoldByClassWaitlistID = 42

	outcome, err := h.svc.CreateTrainBooking(context.Background(), "user-1", "trip-1", "sl", 2, 50000)
	if err != nil {
		t.Fatalf("CreateTrainBooking() error = %v", err)
	}
	if outcome.Booking != nil {
		t.Fatalf("outcome = %+v, want no Booking (waitlisted)", outcome)
	}
	if outcome.WaitlistEntryID != 42 {
		t.Errorf("waitlist entry id = %d, want 42", outcome.WaitlistEntryID)
	}
	if len(h.repo.bookings) != 0 {
		t.Errorf("bookings created = %d, want 0 - a waitlisted request must not create a booking row", len(h.repo.bookings))
	}
	if h.payment.chargeN != 0 {
		t.Errorf("Charge called %d times, want 0", h.payment.chargeN)
	}
}

func TestCreateTrainBooking_HoldByClassErrorPropagates(t *testing.T) {
	h := newHarness()
	h.inventory.createHoldByClassErr = errors.New("inventory unavailable")

	_, err := h.svc.CreateTrainBooking(context.Background(), "user-1", "trip-1", "sl", 2, 50000)
	if err == nil {
		t.Fatal("CreateTrainBooking() error = nil, want error")
	}
	if len(h.repo.bookings) != 0 {
		t.Errorf("bookings created = %d, want 0", len(h.repo.bookings))
	}
}

// TestCreateBookingFromWaitlist_HappyPath proves the claim-and-continue
// path: a promoted waitlist entry's hold becomes a booking at Held,
// advance()s through payment and confirmation exactly like any other
// booking.
func TestCreateBookingFromWaitlist_HappyPath(t *testing.T) {
	h := newHarness()
	h.inventory.claimHoldID = "hold-promo-1"
	h.inventory.claimSeatNos = []string{"9"}

	b, err := h.svc.CreateBookingFromWaitlist(context.Background(), 7, "user-1", "trip-1", 30000)
	if err != nil {
		t.Fatalf("CreateBookingFromWaitlist() error = %v", err)
	}
	if b.Status != StatusConfirmed {
		t.Errorf("status = %s, want confirmed", b.Status)
	}
	if b.HoldID != "hold-promo-1" {
		t.Errorf("hold id = %s, want hold-promo-1", b.HoldID)
	}
	if h.inventory.claimN != 1 {
		t.Errorf("ClaimPromotedWaitlist called %d times, want 1", h.inventory.claimN)
	}
}

// TestCreateBookingFromWaitlist_IdempotentByHoldID proves a retried claim
// (e.g. a double-clicked "pay now") never creates a second booking for the
// same promoted hold - it just re-advances the one that already exists.
func TestCreateBookingFromWaitlist_IdempotentByHoldID(t *testing.T) {
	h := newHarness()
	h.inventory.claimHoldID = "hold-promo-1"

	first, err := h.svc.CreateBookingFromWaitlist(context.Background(), 7, "user-1", "trip-1", 30000)
	if err != nil {
		t.Fatalf("first CreateBookingFromWaitlist() error = %v", err)
	}
	if len(h.repo.bookings) != 1 {
		t.Fatalf("bookings after first call = %d, want 1", len(h.repo.bookings))
	}

	second, err := h.svc.CreateBookingFromWaitlist(context.Background(), 7, "user-1", "trip-1", 30000)
	if err != nil {
		t.Fatalf("second CreateBookingFromWaitlist() error = %v", err)
	}
	if len(h.repo.bookings) != 1 {
		t.Errorf("bookings after second call = %d, want still 1 (idempotent)", len(h.repo.bookings))
	}
	if second.ID != first.ID {
		t.Errorf("second call created a different booking: %s vs %s", second.ID, first.ID)
	}
	if second.Status != StatusConfirmed {
		t.Errorf("second call status = %s, want confirmed", second.Status)
	}
}
