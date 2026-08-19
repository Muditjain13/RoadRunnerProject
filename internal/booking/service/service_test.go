package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- fake Repository/TxRepository ---------------------------------------

type fakeRepo struct {
	mu       sync.Mutex
	bookings map[string]Booking
	outbox   []OutboxEvent
	nextID   int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{bookings: make(map[string]Booking)}
}

func (f *fakeRepo) WithinTx(ctx context.Context, fn func(ctx context.Context, tx TxRepository) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(ctx, f)
}

func (f *fakeRepo) GetBooking(ctx context.Context, id string) (Booking, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.bookings[id]
	if !ok {
		return Booking{}, apperrors.ErrNotFound
	}
	return b, nil
}

func (f *fakeRepo) ListStaleBookings(ctx context.Context, statuses []Status, updatedBefore time.Time, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	want := make(map[Status]bool, len(statuses))
	for _, s := range statuses {
		want[s] = true
	}
	var ids []string
	for id, b := range f.bookings {
		if want[b.Status] {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (f *fakeRepo) ListUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]OutboxEvent{}, f.outbox...), nil
}

func (f *fakeRepo) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	return nil
}

func (f *fakeRepo) InsertBooking(ctx context.Context, b Booking) error {
	f.bookings[b.ID] = b
	return nil
}

func (f *fakeRepo) InsertHeldBooking(ctx context.Context, b Booking) error {
	f.bookings[b.ID] = b
	return nil
}

func (f *fakeRepo) GetBookingByHoldID(ctx context.Context, holdID string) (Booking, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.bookings {
		if b.HoldID == holdID {
			return b, true, nil
		}
	}
	return Booking{}, false, nil
}

func (f *fakeRepo) GetBookingForUpdate(ctx context.Context, id string) (Booking, error) {
	b, ok := f.bookings[id]
	if !ok {
		return Booking{}, apperrors.ErrNotFound
	}
	return b, nil
}

func (f *fakeRepo) UpdateBookingHeld(ctx context.Context, id, holdID string) error {
	b := f.bookings[id]
	b.Status, b.HoldID = StatusHeld, holdID
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) UpdateBookingPaid(ctx context.Context, id, paymentID string) error {
	b := f.bookings[id]
	b.Status, b.PaymentID = StatusPaid, paymentID
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) UpdateBookingSupplierPNR(ctx context.Context, id, supplierPNR string) error {
	b := f.bookings[id]
	b.SupplierPNR = supplierPNR
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) UpdateBookingConfirmed(ctx context.Context, id, pnr string) error {
	b := f.bookings[id]
	b.Status, b.PNR = StatusConfirmed, pnr
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) UpdateBookingRefunding(ctx context.Context, id, reason string) error {
	b := f.bookings[id]
	b.Status, b.FailureReason = StatusRefunding, reason
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) UpdateBookingFailed(ctx context.Context, id, reason string) error {
	b := f.bookings[id]
	b.Status, b.FailureReason = StatusFailed, reason
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) UpdateBookingCancelled(ctx context.Context, id string) error {
	b := f.bookings[id]
	b.Status = StatusCancelled
	f.bookings[id] = b
	return nil
}

func (f *fakeRepo) InsertOutboxEvent(ctx context.Context, bookingID, eventType string, payload []byte) error {
	f.nextID++
	f.outbox = append(f.outbox, OutboxEvent{ID: f.nextID, BookingID: bookingID, EventType: eventType, Payload: payload})
	return nil
}

// --- fake clients --------------------------------------------------------

type fakeInventory struct {
	mu sync.Mutex

	createHoldID  string
	createHoldErr error
	createHoldN   int

	releaseHoldErr error
	releaseHoldN   int

	confirmHoldErr error
	confirmHoldN   int

	cancelBookingErr error
	cancelBookingN   int

	createHoldByClassHoldID     string
	createHoldByClassSeatNos    []string
	createHoldByClassWaitlistID int64 // set (with holdID "") to simulate a waitlisted outcome
	createHoldByClassErr        error
	createHoldByClassN          int

	claimHoldID  string
	claimSeatNos []string
	claimErr     error
	claimN       int
}

func (f *fakeInventory) CreateHold(ctx context.Context, tripID, userID string, seatNos []string, ttl time.Duration) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createHoldN++
	if f.createHoldErr != nil {
		return "", f.createHoldErr
	}
	if f.createHoldID == "" {
		return "hold-1", nil
	}
	return f.createHoldID, nil
}

func (f *fakeInventory) ReleaseHold(ctx context.Context, holdID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseHoldN++
	return f.releaseHoldErr
}

func (f *fakeInventory) ConfirmHold(ctx context.Context, holdID, bookingID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmHoldN++
	return f.confirmHoldErr
}

func (f *fakeInventory) CancelBooking(ctx context.Context, tripID, bookingID string, seatNos []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelBookingN++
	return f.cancelBookingErr
}

func (f *fakeInventory) CreateHoldByClass(ctx context.Context, tripID, classCode, userID string, seatCount int, amountPaise money.Paise, ttl time.Duration) (string, []string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createHoldByClassN++
	if f.createHoldByClassErr != nil {
		return "", nil, 0, f.createHoldByClassErr
	}
	if f.createHoldByClassWaitlistID != 0 {
		return "", nil, f.createHoldByClassWaitlistID, nil
	}
	holdID := f.createHoldByClassHoldID
	if holdID == "" {
		holdID = "hold-1"
	}
	seatNos := f.createHoldByClassSeatNos
	if seatNos == nil {
		seatNos = []string{"1", "2"}
	}
	return holdID, seatNos, 0, nil
}

func (f *fakeInventory) ClaimPromotedWaitlist(ctx context.Context, waitlistEntryID int64, userID string) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimN++
	if f.claimErr != nil {
		return "", nil, f.claimErr
	}
	holdID := f.claimHoldID
	if holdID == "" {
		holdID = "hold-promoted-1"
	}
	seatNos := f.claimSeatNos
	if seatNos == nil {
		seatNos = []string{"1", "2"}
	}
	return holdID, seatNos, nil
}

type fakePayment struct {
	mu sync.Mutex

	paymentID string
	approved  bool
	chargeErr error
	chargeN   int

	refundErr error
	refundN   int
}

func (f *fakePayment) Charge(ctx context.Context, idempotencyKey, bookingID, userID string, amountPaise money.Paise) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chargeN++
	if f.chargeErr != nil {
		return "", false, f.chargeErr
	}
	id := f.paymentID
	if id == "" {
		id = "payment-1"
	}
	return id, f.approved, nil
}

func (f *fakePayment) Refund(ctx context.Context, idempotencyKey, paymentID string, amountPaise money.Paise) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refundN++
	return f.refundErr
}

type fakeSupplierAdapter struct {
	mu sync.Mutex

	pnr        string
	confirmErr error
	confirmN   int

	cancelErr error
	cancelN   int
}

func (f *fakeSupplierAdapter) Confirm(ctx context.Context, bookingID, tripID string, seatNos []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmN++
	if f.confirmErr != nil {
		return "", f.confirmErr
	}
	pnr := f.pnr
	if pnr == "" {
		pnr = "SUPPNR"
	}
	return pnr, nil
}

func (f *fakeSupplierAdapter) Cancel(ctx context.Context, supplierPNR string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelN++
	return f.cancelErr
}

// --- test harness ---------------------------------------------------------

type harness struct {
	repo           *fakeRepo
	inventory      *fakeInventory
	payment        *fakePayment
	supplier       *fakeSupplierAdapter
	trainSupplier  *fakeSupplierAdapter
	flightSupplier *fakeFlightSupplierAdapter
	svc            *Service
}

func newHarness() *harness {
	h := &harness{
		repo:           newFakeRepo(),
		inventory:      &fakeInventory{},
		payment:        &fakePayment{approved: true},
		supplier:       &fakeSupplierAdapter{},
		trainSupplier:  &fakeSupplierAdapter{},
		flightSupplier: &fakeFlightSupplierAdapter{},
	}
	h.svc = NewService(h.repo, h.inventory, h.payment, map[Mode]SupplierAdapter{
		ModeBus:    h.supplier,
		ModeTrain:  h.trainSupplier,
		ModeFlight: h.flightSupplier,
	}, testLogger())
	return h
}

func TestCreateBooking_HappyPath(t *testing.T) {
	h := newHarness()

	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1", "2"}, ModeBus, 10000)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	if booking.Status != StatusConfirmed {
		t.Fatalf("status = %s, want confirmed", booking.Status)
	}
	if booking.PNR == "" {
		t.Error("PNR was not issued")
	}
	if booking.SupplierPNR == "" {
		t.Error("SupplierPNR was not recorded")
	}
	if h.inventory.confirmHoldN != 1 {
		t.Errorf("ConfirmHold called %d times, want 1", h.inventory.confirmHoldN)
	}

	events := mustEvents(t, h.repo)
	if len(events) != 1 || events[0].EventType != "booking.confirmed" {
		t.Errorf("outbox events = %+v, want exactly one booking.confirmed", events)
	}
}

func TestCreateBooking_HoldFails(t *testing.T) {
	h := newHarness()
	h.inventory.createHoldErr = errors.New("seat unavailable")

	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err == nil {
		t.Fatal("CreateBooking() error = nil, want error")
	}
	if booking.Status != StatusFailed {
		t.Errorf("status = %s, want failed", booking.Status)
	}
	if h.payment.chargeN != 0 {
		t.Errorf("Charge called %d times, want 0 (should never be reached)", h.payment.chargeN)
	}

	events := mustEvents(t, h.repo)
	if len(events) != 1 || events[0].EventType != "booking.failed" {
		t.Errorf("outbox events = %+v, want exactly one booking.failed", events)
	}
}

func TestCreateBooking_PaymentDeclined(t *testing.T) {
	h := newHarness()
	h.payment.approved = false

	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err == nil {
		t.Fatal("CreateBooking() error = nil, want error")
	}
	if booking.Status != StatusFailed {
		t.Errorf("status = %s, want failed", booking.Status)
	}
	if h.inventory.releaseHoldN != 1 {
		t.Errorf("ReleaseHold called %d times, want 1 (hold must be released on decline)", h.inventory.releaseHoldN)
	}
	if h.payment.refundN != 0 {
		t.Errorf("Refund called %d times, want 0 (nothing was charged)", h.payment.refundN)
	}
	if h.supplier.confirmN != 0 {
		t.Errorf("supplier Confirm called %d times, want 0 (never reached)", h.supplier.confirmN)
	}
}

// TestCreateBooking_SupplierDeclines_HardCase is the case CLAUDE.md
// section 4 calls out explicitly: payment succeeds but supplier confirm
// fails. Money already moved, so the correct outcome is refund + release
// + (nothing to cancel at the supplier, since it never confirmed) +
// Cancelled, not a silently lost booking.
func TestCreateBooking_SupplierDeclines_HardCase(t *testing.T) {
	h := newHarness()
	h.supplier.confirmErr = apperrors.ErrUnavailable

	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v, want nil (compensation itself succeeded)", err)
	}
	if booking.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", booking.Status)
	}
	if h.payment.refundN != 1 {
		t.Errorf("Refund called %d times, want 1", h.payment.refundN)
	}
	if h.inventory.releaseHoldN != 1 {
		t.Errorf("ReleaseHold called %d times, want 1", h.inventory.releaseHoldN)
	}
	if h.inventory.cancelBookingN != 0 {
		t.Errorf("CancelBooking called %d times, want 0 (seats were only held, never booked)", h.inventory.cancelBookingN)
	}
	if h.supplier.cancelN != 0 {
		t.Errorf("supplier Cancel called %d times, want 0 (no PNR was ever issued)", h.supplier.cancelN)
	}

	events := mustEvents(t, h.repo)
	if len(events) != 1 || events[0].EventType != "booking.cancelled" {
		t.Errorf("outbox events = %+v, want exactly one booking.cancelled", events)
	}
}

// TestCreateBooking_InventoryConfirmFails_HardCase covers the harder
// variant: the supplier DID confirm (a PNR was issued) but the local
// inventory confirm then fails - compensation must also cancel the
// supplier PNR, not just refund and release.
func TestCreateBooking_InventoryConfirmFails_HardCase(t *testing.T) {
	h := newHarness()
	h.inventory.confirmHoldErr = apperrors.ErrConflict

	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v, want nil", err)
	}
	if booking.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", booking.Status)
	}
	if h.payment.refundN != 1 {
		t.Errorf("Refund called %d times, want 1", h.payment.refundN)
	}
	if h.inventory.releaseHoldN != 1 {
		t.Errorf("ReleaseHold called %d times, want 1", h.inventory.releaseHoldN)
	}
	if h.supplier.cancelN != 1 {
		t.Errorf("supplier Cancel called %d times, want 1 (a PNR was issued and must be cancelled)", h.supplier.cancelN)
	}
}

func TestCancelBooking_ConfirmedBooking(t *testing.T) {
	h := newHarness()
	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}

	cancelled, err := h.svc.CancelBooking(context.Background(), booking.ID)
	if err != nil {
		t.Fatalf("CancelBooking() error = %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", cancelled.Status)
	}
	if h.payment.refundN != 1 {
		t.Errorf("Refund called %d times, want 1", h.payment.refundN)
	}
	// The booking was Confirmed, so seats are booked, not held -
	// compensation must use CancelBooking, not ReleaseHold.
	if h.inventory.cancelBookingN != 1 {
		t.Errorf("inventory CancelBooking called %d times, want 1", h.inventory.cancelBookingN)
	}
	if h.inventory.releaseHoldN != 0 {
		t.Errorf("ReleaseHold called %d times, want 0 (wrong call for a confirmed booking)", h.inventory.releaseHoldN)
	}
	if h.supplier.cancelN != 1 {
		t.Errorf("supplier Cancel called %d times, want 1", h.supplier.cancelN)
	}
}

func TestCancelBooking_Idempotent(t *testing.T) {
	h := newHarness()
	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err != nil {
		t.Fatalf("CreateBooking() error = %v", err)
	}
	if _, err := h.svc.CancelBooking(context.Background(), booking.ID); err != nil {
		t.Fatalf("first CancelBooking() error = %v", err)
	}
	if _, err := h.svc.CancelBooking(context.Background(), booking.ID); err != nil {
		t.Fatalf("second CancelBooking() error = %v, want nil (idempotent)", err)
	}
	if h.payment.refundN != 1 {
		t.Errorf("Refund called %d times, want 1 (second cancel must not re-refund)", h.payment.refundN)
	}
}

func TestCancelBooking_NotConfirmed(t *testing.T) {
	h := newHarness()
	h.inventory.createHoldErr = errors.New("boom")
	booking, err := h.svc.CreateBooking(context.Background(), "user-1", "trip-1", []string{"1"}, ModeBus, 10000)
	if err == nil {
		t.Fatal("expected CreateBooking to fail")
	}
	if booking.Status != StatusFailed {
		t.Fatalf("precondition: status = %s, want failed", booking.Status)
	}

	if _, err := h.svc.CancelBooking(context.Background(), booking.ID); !errors.Is(err, apperrors.ErrConflict) {
		t.Errorf("CancelBooking() on a failed booking: error = %v, want errors.Is(_, ErrConflict)", err)
	}
}

// TestAdvance_ResumesFromHeld simulates the reconciliation sweeper
// picking up a booking that crashed after the hold succeeded but before
// payment was attempted.
func TestAdvance_ResumesFromHeld(t *testing.T) {
	h := newHarness()
	booking := Booking{
		ID: "b1", UserID: "u1", TripID: "t1", Mode: ModeBus,
		SeatNos: []string{"1"}, AmountPaise: 10000,
		HoldID: "hold-1", Status: StatusHeld,
	}
	h.repo.bookings[booking.ID] = booking

	resumed, err := h.svc.advance(context.Background(), booking)
	if err != nil {
		t.Fatalf("advance() error = %v", err)
	}
	if resumed.Status != StatusConfirmed {
		t.Fatalf("status = %s, want confirmed", resumed.Status)
	}
	if h.inventory.createHoldN != 0 {
		t.Errorf("CreateHold called %d times, want 0 (must not re-hold)", h.inventory.createHoldN)
	}
}

// TestAdvance_ResumesFromPaidWithSupplierPNR simulates resuming a booking
// that already has a supplier PNR recorded (the supplier confirm
// sub-step completed, but the process died before the local inventory
// confirm sub-step) - the supplier must not be re-confirmed.
func TestAdvance_ResumesFromPaidWithSupplierPNR(t *testing.T) {
	h := newHarness()
	booking := Booking{
		ID: "b1", UserID: "u1", TripID: "t1", Mode: ModeBus,
		SeatNos: []string{"1"}, AmountPaise: 10000,
		HoldID: "hold-1", PaymentID: "payment-1", SupplierPNR: "EXISTING",
		Status: StatusPaid,
	}
	h.repo.bookings[booking.ID] = booking

	resumed, err := h.svc.advance(context.Background(), booking)
	if err != nil {
		t.Fatalf("advance() error = %v", err)
	}
	if resumed.Status != StatusConfirmed {
		t.Fatalf("status = %s, want confirmed", resumed.Status)
	}
	if resumed.SupplierPNR != "EXISTING" {
		t.Errorf("SupplierPNR = %s, want EXISTING (must not be re-issued)", resumed.SupplierPNR)
	}
	if h.supplier.confirmN != 0 {
		t.Errorf("supplier Confirm called %d times, want 0 (already recorded)", h.supplier.confirmN)
	}
	if h.inventory.confirmHoldN != 1 {
		t.Errorf("ConfirmHold called %d times, want 1", h.inventory.confirmHoldN)
	}
}

// TestAdvance_ResumesFromRefunding simulates a crash mid-compensation:
// the refund and hold release already happened, but the supplier cancel
// didn't. Resuming must not double-refund.
func TestAdvance_ResumesFromRefunding(t *testing.T) {
	h := newHarness()
	booking := Booking{
		ID: "b1", UserID: "u1", TripID: "t1", Mode: ModeBus,
		SeatNos: []string{"1"}, AmountPaise: 10000,
		HoldID: "hold-1", PaymentID: "payment-1", SupplierPNR: "PNR1",
		Status: StatusRefunding, FailureReason: "supplier declined",
	}
	h.repo.bookings[booking.ID] = booking

	resumed, err := h.svc.advance(context.Background(), booking)
	if err != nil {
		t.Fatalf("advance() error = %v", err)
	}
	if resumed.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", resumed.Status)
	}
	if h.payment.refundN != 1 {
		t.Errorf("Refund called %d times, want 1", h.payment.refundN)
	}
	if h.supplier.cancelN != 1 {
		t.Errorf("supplier Cancel called %d times, want 1", h.supplier.cancelN)
	}
}

func TestValidation(t *testing.T) {
	h := newHarness()
	tests := []struct {
		name    string
		userID  string
		tripID  string
		seatNos []string
		amount  money.Paise
	}{
		{"empty user", "", "trip-1", []string{"1"}, 10000},
		{"empty trip", "user-1", "", []string{"1"}, 10000},
		{"no seats", "user-1", "trip-1", nil, 10000},
		{"zero amount", "user-1", "trip-1", []string{"1"}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := h.svc.CreateBooking(context.Background(), tt.userID, tt.tripID, tt.seatNos, ModeBus, tt.amount); err == nil {
				t.Error("CreateBooking() error = nil, want error")
			}
		})
	}
}

func mustEvents(t *testing.T, repo *fakeRepo) []OutboxEvent {
	t.Helper()
	events, err := repo.ListUnpublishedOutboxEvents(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListUnpublishedOutboxEvents() error = %v", err)
	}
	return events
}
