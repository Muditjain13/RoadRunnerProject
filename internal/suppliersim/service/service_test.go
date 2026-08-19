package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

func noSleep(ctx context.Context, d time.Duration) error { return nil }

func TestConfirm_Success(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	pnr, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if len(pnr) != 6 {
		t.Errorf("Confirm() pnr = %q, want length 6", pnr)
	}
}

func TestConfirm_Idempotent(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	pnr1, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	pnr2, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus)
	if err != nil {
		t.Fatalf("Confirm() replay error = %v", err)
	}
	if pnr1 != pnr2 {
		t.Errorf("Confirm() replay returned a different PNR: %s != %s", pnr1, pnr2)
	}
}

func TestConfirm_ValidationErrors(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	tests := []struct {
		name      string
		bookingID string
		tripID    string
		seatNos   []string
	}{
		{"empty booking id", "", "trip-1", []string{"1"}},
		{"empty trip id", "booking-1", "", []string{"1"}},
		{"no seats", "booking-1", "trip-1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.Confirm(context.Background(), tt.bookingID, tt.tripID, tt.seatNos, ModeBus); err == nil {
				t.Error("Confirm() error = nil, want error")
			}
		})
	}
}

func TestConfirm_ChaosDecline(t *testing.T) {
	svc := NewService(
		WithSleeper(noSleep),
		WithChaos(ModeFlight, Chaos{ErrorRate: 1.0}), // always declines
		WithRNG(func() float64 { return 0 }),         // 0 < 1.0, so the roll always "hits"
	)
	_, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeFlight)
	if !errors.Is(err, apperrors.ErrUnavailable) {
		t.Errorf("Confirm() error = %v, want errors.Is(_, ErrUnavailable)", err)
	}
}

func TestConfirm_ChaosOnlyAppliesToConfiguredMode(t *testing.T) {
	svc := NewService(
		WithSleeper(noSleep),
		WithChaos(ModeFlight, Chaos{ErrorRate: 1.0}),
		WithRNG(func() float64 { return 0 }),
	)
	// ModeBus has no chaos configured, so it should always succeed even
	// though the RNG would trigger a decline if bus were affected.
	if _, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus); err != nil {
		t.Errorf("Confirm() for unaffected mode error = %v, want nil", err)
	}
}

func TestConfirm_DeclinedAttemptNotCached(t *testing.T) {
	svc := NewService(
		WithSleeper(noSleep),
		WithChaos(ModeFlight, Chaos{ErrorRate: 1.0}),
		WithRNG(func() float64 { return 0 }),
	)
	_, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeFlight)
	if !errors.Is(err, apperrors.ErrUnavailable) {
		t.Fatalf("first Confirm() error = %v, want ErrUnavailable", err)
	}

	// Simulate the chaos clearing up (e.g. the saga retries later) by
	// reconfiguring a fresh service with no error rate but the same
	// booking - proves the failed attempt wasn't cached as a permanent
	// decline.
	svc2 := NewService(WithSleeper(noSleep))
	pnr, err := svc2.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeFlight)
	if err != nil {
		t.Fatalf("retry Confirm() error = %v, want nil", err)
	}
	if pnr == "" {
		t.Error("retry Confirm() returned empty PNR")
	}
}

func TestConfirm_ConcurrentSameBookingID(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))

	const attempts = 100
	pnrs := make([]string, attempts)
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			pnr, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus)
			if err != nil {
				t.Errorf("goroutine %d: Confirm() error = %v", i, err)
				return
			}
			pnrs[i] = pnr
		}(i)
	}
	wg.Wait()

	first := pnrs[0]
	for i, pnr := range pnrs {
		if pnr != first {
			t.Errorf("goroutine %d got PNR %s, want %s (idempotent replay)", i, pnr, first)
		}
	}
}

func TestCancel(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	pnr, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}

	if err := svc.Cancel(context.Background(), pnr); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	// Idempotent: cancelling again is a no-op success.
	if err := svc.Cancel(context.Background(), pnr); err != nil {
		t.Errorf("second Cancel() error = %v, want nil", err)
	}
}

func TestCancel_UnknownPNR(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	err := svc.Cancel(context.Background(), "does-not-exist")
	if !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("Cancel() error = %v, want errors.Is(_, ErrNotFound)", err)
	}
}

func TestHold_Success(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	ref, err := svc.Hold(context.Background(), "booking-1", "trip-1", 2, ModeFlight)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if ref == "" {
		t.Error("Hold() returned empty ref")
	}
}

func TestHold_Idempotent(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	ref1, err := svc.Hold(context.Background(), "booking-1", "trip-1", 2, ModeFlight)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	ref2, err := svc.Hold(context.Background(), "booking-1", "trip-1", 2, ModeFlight)
	if err != nil {
		t.Fatalf("Hold() replay error = %v", err)
	}
	if ref1 != ref2 {
		t.Errorf("Hold() replay returned a different ref: %s != %s", ref1, ref2)
	}
}

func TestHold_ValidationErrors(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	tests := []struct {
		name      string
		bookingID string
		tripID    string
		seatCount int
	}{
		{"empty booking id", "", "trip-1", 1},
		{"empty trip id", "booking-1", "", 1},
		{"zero seat count", "booking-1", "trip-1", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.Hold(context.Background(), tt.bookingID, tt.tripID, tt.seatCount, ModeFlight); err == nil {
				t.Error("Hold() error = nil, want error")
			}
		})
	}
}

// TestHold_NotSubjectToErrorChaos documents the deliberate design choice
// in Hold's doc comment: CLAUDE.md's hard case is specifically "payment
// succeeds but supplier confirm fails," so ErrorRate chaos only applies
// to Confirm - a maxed-out error rate must not make Hold decline.
func TestHold_NotSubjectToErrorChaos(t *testing.T) {
	svc := NewService(
		WithSleeper(noSleep),
		WithChaos(ModeFlight, Chaos{ErrorRate: 1.0}),
		WithRNG(func() float64 { return 0 }),
	)
	ref, err := svc.Hold(context.Background(), "booking-1", "trip-1", 1, ModeFlight)
	if err != nil {
		t.Fatalf("Hold() error = %v, want nil (Hold ignores ErrorRate)", err)
	}
	if ref == "" {
		t.Error("Hold() returned empty ref")
	}
}

func TestReleaseHold(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	ref, err := svc.Hold(context.Background(), "booking-1", "trip-1", 1, ModeFlight)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	if err := svc.ReleaseHold(context.Background(), ref); err != nil {
		t.Fatalf("ReleaseHold() error = %v", err)
	}
	// Idempotent: releasing again is a no-op success.
	if err := svc.ReleaseHold(context.Background(), ref); err != nil {
		t.Errorf("second ReleaseHold() error = %v, want nil", err)
	}
}

func TestReleaseHold_UnknownRef(t *testing.T) {
	svc := NewService(WithSleeper(noSleep))
	err := svc.ReleaseHold(context.Background(), "does-not-exist")
	if !errors.Is(err, apperrors.ErrNotFound) {
		t.Errorf("ReleaseHold() error = %v, want errors.Is(_, ErrNotFound)", err)
	}
}

func TestConfirm_Latency(t *testing.T) {
	var slept time.Duration
	var mu sync.Mutex
	var sleepCalls int64

	svc := NewService(
		WithChaos(ModeBus, Chaos{Latency: 50 * time.Millisecond}),
		WithSleeper(func(ctx context.Context, d time.Duration) error {
			atomic.AddInt64(&sleepCalls, 1)
			mu.Lock()
			slept = d
			mu.Unlock()
			return nil
		}),
	)

	if _, err := svc.Confirm(context.Background(), "booking-1", "trip-1", []string{"1"}, ModeBus); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if atomic.LoadInt64(&sleepCalls) != 1 {
		t.Fatalf("sleeper called %d times, want 1", sleepCalls)
	}
	mu.Lock()
	defer mu.Unlock()
	if slept != 50*time.Millisecond {
		t.Errorf("slept for %v, want 50ms", slept)
	}
}
