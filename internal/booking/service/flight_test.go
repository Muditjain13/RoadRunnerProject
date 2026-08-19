package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

// fakeFlightSupplierAdapter implements SupplierAdapter plus FlightHolder
// and HoldReleaser - the richer shape only flight's real adapter needs
// (see domain.go's doc comments on those two interfaces).
type fakeFlightSupplierAdapter struct {
	mu sync.Mutex

	pnr        string
	confirmErr error
	confirmN   int

	cancelErr error
	cancelN   int

	holdRef string
	holdErr error
	holdN   int

	releaseHoldErr error
	releaseHoldN   int
}

func (f *fakeFlightSupplierAdapter) Confirm(ctx context.Context, bookingID, tripID string, seatNos []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmN++
	if f.confirmErr != nil {
		return "", f.confirmErr
	}
	pnr := f.pnr
	if pnr == "" {
		pnr = "FLTPNR"
	}
	return pnr, nil
}

func (f *fakeFlightSupplierAdapter) Cancel(ctx context.Context, supplierPNR string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelN++
	return f.cancelErr
}

func (f *fakeFlightSupplierAdapter) Hold(ctx context.Context, bookingID, tripID string, seatCount int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.holdN++
	if f.holdErr != nil {
		return "", f.holdErr
	}
	ref := f.holdRef
	if ref == "" {
		ref = "HOLD-REF-1"
	}
	return ref, nil
}

func (f *fakeFlightSupplierAdapter) ReleaseHold(ctx context.Context, externalHoldRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseHoldN++
	return f.releaseHoldErr
}

// TestCreateFlightBooking_HappyPath proves flight's external hold flows
// through the exact same advance() machinery (doCharge, doConfirm) bus
// and train use, all the way to Confirmed, with no local inventory call
// anywhere in the path.
func TestCreateFlightBooking_HappyPath(t *testing.T) {
	h := newHarness()

	booking, err := h.svc.CreateFlightBooking(context.Background(), "user-1", "trip-1", 2, 80000)
	if err != nil {
		t.Fatalf("CreateFlightBooking() error = %v", err)
	}
	if booking.Status != StatusConfirmed {
		t.Errorf("status = %s, want confirmed", booking.Status)
	}
	if booking.Mode != ModeFlight {
		t.Errorf("mode = %s, want flight", booking.Mode)
	}
	if booking.PNR == "" {
		t.Error("PNR was not issued")
	}
	if booking.SupplierPNR == "" {
		t.Error("SupplierPNR was not recorded")
	}
	if len(booking.SeatNos) != 2 {
		t.Errorf("seat nos = %v, want 2 synthetic passenger placeholders", booking.SeatNos)
	}
	if h.flightSupplier.holdN != 1 {
		t.Errorf("Hold called %d times, want 1", h.flightSupplier.holdN)
	}
	if h.flightSupplier.confirmN != 1 {
		t.Errorf("Confirm called %d times, want 1", h.flightSupplier.confirmN)
	}
	if h.inventory.createHoldN != 0 || h.inventory.confirmHoldN != 0 {
		t.Errorf("inventory calls = createHold:%d confirmHold:%d, want 0 (flight has no local inventory)", h.inventory.createHoldN, h.inventory.confirmHoldN)
	}
}

func TestCreateFlightBooking_HoldFails(t *testing.T) {
	h := newHarness()
	h.flightSupplier.holdErr = errors.New("provider unreachable")

	_, err := h.svc.CreateFlightBooking(context.Background(), "user-1", "trip-1", 1, 50000)
	if err == nil {
		t.Fatal("CreateFlightBooking() error = nil, want error")
	}
	if len(h.repo.bookings) != 0 {
		t.Errorf("bookings created = %d, want 0 (nothing to charge or fail - no reservation was ever made)", len(h.repo.bookings))
	}
	if h.payment.chargeN != 0 {
		t.Errorf("Charge called %d times, want 0", h.payment.chargeN)
	}
}

// TestCreateFlightBooking_SupplierDeclinesConfirm_HardCase is CLAUDE.md
// section 4's hard case for flight specifically: the external hold
// succeeded, payment succeeded, but the supplier declines to actually
// ticket it. Compensation must refund, release the EXTERNAL hold (not
// call inventory at all), and there's no supplier PNR to cancel since
// one was never issued.
func TestCreateFlightBooking_SupplierDeclinesConfirm_HardCase(t *testing.T) {
	h := newHarness()
	h.flightSupplier.confirmErr = apperrors.ErrUnavailable

	booking, err := h.svc.CreateFlightBooking(context.Background(), "user-1", "trip-1", 1, 50000)
	if err != nil {
		t.Fatalf("CreateFlightBooking() error = %v, want nil (compensation itself succeeded)", err)
	}
	if booking.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", booking.Status)
	}
	if h.payment.refundN != 1 {
		t.Errorf("Refund called %d times, want 1", h.payment.refundN)
	}
	if h.flightSupplier.releaseHoldN != 1 {
		t.Errorf("adapter ReleaseHold called %d times, want 1", h.flightSupplier.releaseHoldN)
	}
	if h.inventory.releaseHoldN != 0 || h.inventory.cancelBookingN != 0 {
		t.Errorf("inventory release/cancel calls = %d/%d, want 0/0 (flight never touches inventory)", h.inventory.releaseHoldN, h.inventory.cancelBookingN)
	}
	if h.flightSupplier.cancelN != 0 {
		t.Errorf("adapter Cancel called %d times, want 0 (no PNR was ever issued)", h.flightSupplier.cancelN)
	}
}

func TestCreateFlightBooking_PaymentDeclined(t *testing.T) {
	h := newHarness()
	h.payment.approved = false

	booking, err := h.svc.CreateFlightBooking(context.Background(), "user-1", "trip-1", 1, 50000)
	if err == nil {
		t.Fatal("CreateFlightBooking() error = nil, want error")
	}
	if booking.Status != StatusFailed {
		t.Errorf("status = %s, want failed", booking.Status)
	}
	if h.flightSupplier.releaseHoldN != 1 {
		t.Errorf("adapter ReleaseHold called %d times, want 1 (declined payment must release the external hold)", h.flightSupplier.releaseHoldN)
	}
	if h.flightSupplier.confirmN != 0 {
		t.Errorf("Confirm called %d times, want 0 (should never be reached)", h.flightSupplier.confirmN)
	}
}

// TestCreateFlightBooking_CancelConfirmed proves a voluntary cancellation
// of a confirmed flight booking releases via the supplier PNR only - no
// inventory call, since flight was never in inventory in the first place.
func TestCreateFlightBooking_CancelConfirmed(t *testing.T) {
	h := newHarness()
	booking, err := h.svc.CreateFlightBooking(context.Background(), "user-1", "trip-1", 1, 50000)
	if err != nil {
		t.Fatalf("CreateFlightBooking() error = %v", err)
	}

	cancelled, err := h.svc.CancelBooking(context.Background(), booking.ID)
	if err != nil {
		t.Fatalf("CancelBooking() error = %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", cancelled.Status)
	}
	if h.flightSupplier.cancelN != 1 {
		t.Errorf("adapter Cancel called %d times, want 1", h.flightSupplier.cancelN)
	}
	if h.inventory.cancelBookingN != 0 {
		t.Errorf("inventory CancelBooking called %d times, want 0", h.inventory.cancelBookingN)
	}
}
