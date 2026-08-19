// Package circuitbreaker is a minimal three-state (closed/open/half-open)
// circuit breaker, per CLAUDE.md section 7: "Circuit breaker on every
// outbound supplier call." Shared because it isn't specific to any one
// service - search wraps its provider calls with it now, and Phase 4's
// flight adapter will need the same protection against a slow/unreliable
// external GDS.
package circuitbreaker

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrOpen = errors.New("circuit breaker: open")

type state int

const (
	closed state = iota
	open
	halfOpen
)

// Breaker trips to open after threshold consecutive failures, rejecting
// calls without attempting them until resetTimeout has passed. It then
// allows exactly one probe call (half-open): success closes the breaker
// and resets the failure count, failure re-opens it.
type Breaker struct {
	mu           sync.Mutex
	state        state
	failures     int
	threshold    int
	resetTimeout time.Duration
	openedAt     time.Time
	now          func() time.Time
}

func New(threshold int, resetTimeout time.Duration) *Breaker {
	return &Breaker{threshold: threshold, resetTimeout: resetTimeout, now: time.Now}
}

// Do runs fn if the breaker allows it, and records the outcome. Returns
// ErrOpen without calling fn if the breaker is open and hasn't reached
// resetTimeout yet.
func (b *Breaker) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if !b.allow() {
		return ErrOpen
	}
	err := fn(ctx)
	if err != nil {
		b.recordFailure()
		return err
	}
	b.recordSuccess()
	return nil
}

func (b *Breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case closed:
		return true
	case open:
		if b.now().Sub(b.openedAt) < b.resetTimeout {
			return false
		}
		b.state = halfOpen
		return true
	case halfOpen:
		// Only one probe at a time; concurrent callers while half-open
		// are rejected rather than piling more load on a recovering
		// dependency.
		return false
	default:
		return false
	}
}

func (b *Breaker) recordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state = closed
	b.failures = 0
}

func (b *Breaker) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == halfOpen {
		b.state = open
		b.openedAt = b.now()
		return
	}

	b.failures++
	if b.failures >= b.threshold {
		b.state = open
		b.openedAt = b.now()
	}
}
