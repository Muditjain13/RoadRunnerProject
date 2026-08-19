package circuitbreaker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreaker_ClosedAllowsCalls(t *testing.T) {
	b := New(3, time.Minute)
	called := false
	err := b.Do(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	if !called {
		t.Error("fn was not called while closed")
	}
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	b := New(3, time.Minute)
	wantErr := errors.New("boom")

	for i := 0; i < 3; i++ {
		err := b.Do(context.Background(), func(ctx context.Context) error { return wantErr })
		if !errors.Is(err, wantErr) {
			t.Fatalf("attempt %d: error = %v, want %v", i, err, wantErr)
		}
	}

	// Fourth call: breaker should now be open and reject without calling fn.
	called := false
	err := b.Do(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Errorf("error = %v, want ErrOpen", err)
	}
	if called {
		t.Error("fn was called while breaker is open")
	}
}

func TestBreaker_HalfOpenProbe_Success(t *testing.T) {
	now := time.Now()
	b := New(1, 10*time.Second)
	b.now = func() time.Time { return now }

	if err := b.Do(context.Background(), func(ctx context.Context) error { return errors.New("boom") }); err == nil {
		t.Fatal("expected first call to fail")
	}
	// Still within resetTimeout: rejected.
	if err := b.Do(context.Background(), func(ctx context.Context) error { return nil }); !errors.Is(err, ErrOpen) {
		t.Fatalf("error = %v, want ErrOpen (still within reset timeout)", err)
	}

	// Advance past resetTimeout: the probe call should be allowed and,
	// on success, close the breaker.
	now = now.Add(11 * time.Second)
	probed := false
	err := b.Do(context.Background(), func(ctx context.Context) error {
		probed = true
		return nil
	})
	if err != nil {
		t.Fatalf("probe call error = %v", err)
	}
	if !probed {
		t.Fatal("probe call was not attempted after reset timeout")
	}

	// Breaker should be closed again now.
	called := false
	if err := b.Do(context.Background(), func(ctx context.Context) error { called = true; return nil }); err != nil {
		t.Fatalf("post-recovery call error = %v", err)
	}
	if !called {
		t.Error("breaker did not close after successful probe")
	}
}

func TestBreaker_HalfOpenProbe_FailureReopens(t *testing.T) {
	now := time.Now()
	b := New(1, 10*time.Second)
	b.now = func() time.Time { return now }

	_ = b.Do(context.Background(), func(ctx context.Context) error { return errors.New("boom") })
	now = now.Add(11 * time.Second)

	// Probe fails - breaker should re-open.
	if err := b.Do(context.Background(), func(ctx context.Context) error { return errors.New("still broken") }); err == nil {
		t.Fatal("expected probe failure")
	}

	// Immediately after, should be open again (rejecting without a
	// full new resetTimeout having elapsed).
	called := false
	err := b.Do(context.Background(), func(ctx context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrOpen) {
		t.Errorf("error = %v, want ErrOpen (re-opened after failed probe)", err)
	}
	if called {
		t.Error("fn was called while breaker should be open")
	}
}

func TestBreaker_SuccessResetsFailureCount(t *testing.T) {
	b := New(3, time.Minute)

	// Two failures, then a success - failure count should reset, so it
	// takes a fresh 3 failures to open, not just one more.
	_ = b.Do(context.Background(), func(ctx context.Context) error { return errors.New("x") })
	_ = b.Do(context.Background(), func(ctx context.Context) error { return errors.New("x") })
	_ = b.Do(context.Background(), func(ctx context.Context) error { return nil })

	err := b.Do(context.Background(), func(ctx context.Context) error { return errors.New("x") })
	if errors.Is(err, ErrOpen) {
		t.Error("breaker opened too early - success should have reset the failure count")
	}
}
