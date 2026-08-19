// Package service is the in-memory fake for the external systems booking
// confirms with after payment succeeds. It stands in for IRCTC (train),
// an Amadeus-style GDS (flight), and a bus operator's GDS (bus) behind
// one implementation, with per-mode injectable error rate and latency so
// the saga's degraded-provider compensation path (CLAUDE.md section 4:
// "payment succeeds but supplier confirm fails") can be exercised against
// something that actually behaves unreliably, not a mock that always
// succeeds.
package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

type Mode string

const (
	ModeBus    Mode = "bus"
	ModeTrain  Mode = "train"
	ModeFlight Mode = "flight"
)

// Chaos is the injectable unreliability for one mode: ErrorRate is the
// probability (0.0-1.0) that Confirm declines, Latency is added before
// Confirm responds either way.
type Chaos struct {
	ErrorRate float64
	Latency   time.Duration
}

type pnrRecord struct {
	bookingID string
	cancelled bool
}

type holdRecord struct {
	bookingID string
	released  bool
}

type Service struct {
	mu        sync.Mutex
	confirmed map[string]string // bookingID -> supplier PNR, successes only
	pnrs      map[string]*pnrRecord
	held      map[string]string // bookingID -> hold ref, successes only
	holds     map[string]*holdRecord

	chaos map[Mode]Chaos
	rng   func() float64                                   // returns [0,1); overridable for tests
	sleep func(ctx context.Context, d time.Duration) error // overridable for tests
}

type Option func(*Service)

// WithChaos sets the error rate and latency for one mode. Modes not
// configured default to zero error rate and zero added latency.
func WithChaos(mode Mode, chaos Chaos) Option {
	return func(s *Service) { s.chaos[mode] = chaos }
}

func WithRNG(rng func() float64) Option {
	return func(s *Service) { s.rng = rng }
}

func WithSleeper(sleep func(ctx context.Context, d time.Duration) error) Option {
	return func(s *Service) { s.sleep = sleep }
}

func NewService(opts ...Option) *Service {
	s := &Service{
		confirmed: make(map[string]string),
		pnrs:      make(map[string]*pnrRecord),
		held:      make(map[string]string),
		holds:     make(map[string]*holdRecord),
		chaos:     make(map[Mode]Chaos),
		rng:       cryptoRandFloat64,
		sleep:     ctxSleep,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Hold simulates reserving seatCount seats with the external supplier
// before payment - only flight calls this (see suppliersim.proto's Hold
// doc). Idempotent on bookingID like Confirm: a prior successful Hold for
// the same bookingID replays its hold ref.
//
// Deliberately NOT subject to ErrorRate chaos, only Latency: CLAUDE.md's
// hard case is specifically "payment succeeds but supplier confirm
// fails" (a seat lock declining is a different, less interesting failure
// mode - nothing has been charged yet, so there's nothing to compensate
// beyond the ordinary "hold failed" path every mode already has). This
// also keeps the single --flight-error-rate flag `make chaos` sets doing
// exactly what its name and pre-existing doc comment always promised:
// governing confirm's decline rate, not a second, distinct failure point.
func (s *Service) Hold(ctx context.Context, bookingID, tripID string, seatCount int, mode Mode) (string, error) {
	if bookingID == "" || tripID == "" {
		return "", fmt.Errorf("hold: bookingID and tripID are required")
	}
	if seatCount <= 0 {
		return "", fmt.Errorf("hold: seatCount must be positive")
	}

	if ref, ok := s.lookupHeld(bookingID); ok {
		return ref, nil
	}

	if latency := s.chaos[mode].Latency; latency > 0 {
		if err := s.sleep(ctx, latency); err != nil {
			return "", fmt.Errorf("hold: %w", err)
		}
	}

	ref := generateHoldRef()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under lock: see Confirm's identical comment.
	if existing, ok := s.held[bookingID]; ok {
		return existing, nil
	}
	s.held[bookingID] = ref
	s.holds[ref] = &holdRecord{bookingID: bookingID}
	return ref, nil
}

func (s *Service) lookupHeld(bookingID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.held[bookingID]
	return ref, ok
}

// ReleaseHold releases a hold that never made it to Confirm. Idempotent:
// releasing an already-released hold is a no-op success. Releasing a
// hold_ref that was never issued is ErrNotFound.
func (s *Service) ReleaseHold(ctx context.Context, holdRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.holds[holdRef]
	if !ok {
		return fmt.Errorf("release hold %s: %w", holdRef, apperrors.ErrNotFound)
	}
	rec.released = true
	return nil
}

// Confirm simulates confirming a booking with the external supplier for
// tripID/seatNos. Idempotent on bookingID: a prior successful Confirm for
// the same bookingID replays its PNR without re-rolling chaos or issuing
// a second PNR. A declined or errored attempt is never cached, so a retry
// gets an independent chaos roll - matching how a real retry against a
// genuinely flaky supplier would behave.
func (s *Service) Confirm(ctx context.Context, bookingID, tripID string, seatNos []string, mode Mode) (string, error) {
	if bookingID == "" || tripID == "" {
		return "", fmt.Errorf("confirm: bookingID and tripID are required")
	}
	if len(seatNos) == 0 {
		return "", fmt.Errorf("confirm: at least one seat is required")
	}

	if pnr, ok := s.lookupConfirmed(bookingID); ok {
		return pnr, nil
	}

	chaos := s.chaos[mode]
	if chaos.Latency > 0 {
		if err := s.sleep(ctx, chaos.Latency); err != nil {
			return "", fmt.Errorf("confirm: %w", err)
		}
	}
	if chaos.ErrorRate > 0 && s.rng() < chaos.ErrorRate {
		return "", fmt.Errorf("confirm: %w: supplier declined the booking", apperrors.ErrUnavailable)
	}

	pnr := generatePNR()

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under lock: a concurrent identical request may have won
	// while this one was "in flight" (sleeping/rolling chaos above).
	if existing, ok := s.confirmed[bookingID]; ok {
		return existing, nil
	}
	s.confirmed[bookingID] = pnr
	s.pnrs[pnr] = &pnrRecord{bookingID: bookingID}
	return pnr, nil
}

func (s *Service) lookupConfirmed(bookingID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pnr, ok := s.confirmed[bookingID]
	return pnr, ok
}

// Cancel releases a previously confirmed PNR. Idempotent: cancelling an
// already-cancelled PNR is a no-op success. Cancelling a PNR that was
// never issued is ErrNotFound.
func (s *Service) Cancel(ctx context.Context, supplierPNR string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.pnrs[supplierPNR]
	if !ok {
		return fmt.Errorf("cancel %s: %w", supplierPNR, apperrors.ErrNotFound)
	}
	rec.cancelled = true
	return nil
}

const pnrAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no ambiguous chars (I/O/0/1)

func generatePNR() string {
	return generateCode(6)
}

// generateHoldRef is deliberately a different shape from a PNR (longer,
// prefixed) so a log line or error message never leaves it ambiguous
// which kind of reference is being talked about.
func generateHoldRef() string {
	return "HOLD-" + generateCode(10)
}

func generateCode(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand.Read failing means the OS RNG is broken - not a
		// condition the caller can meaningfully recover from.
		panic(fmt.Errorf("suppliersim: generate code: %w", err))
	}
	code := make([]byte, n)
	for i, b := range buf {
		code[i] = pnrAlphabet[int(b)%len(pnrAlphabet)]
	}
	return string(code)
}

func cryptoRandFloat64() float64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic(fmt.Errorf("suppliersim: rng: %w", err))
	}
	// Top 53 bits -> [0, 1), matching math/rand's float64 precision.
	const mantissaBits = 53
	u := uint64(0)
	for _, b := range buf {
		u = u<<8 | uint64(b)
	}
	return float64(u>>(64-mantissaBits)) / float64(uint64(1)<<mantissaBits)
}

func ctxSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
