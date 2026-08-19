package service

import (
	"context"
	"log/slog"
	"time"
)

// nonTerminalStatuses are exactly the statuses advance() knows how to
// resume from. Confirmed, Failed, and Cancelled are terminal and never
// swept.
var nonTerminalStatuses = []Status{StatusPending, StatusHeld, StatusPaid, StatusRefunding}

// RunReconciliationSweeper periodically resumes bookings that have sat in
// a non-terminal status past staleness - crashed mid-saga, a slow
// retry, or an infra error that left a step unresolved. It calls the
// exact same advance() the synchronous request path uses, which is safe
// because every step advance() might repeat (Charge, supplier Confirm,
// ConfirmHold, Refund, ReleaseHold, supplier Cancel) is idempotent - see
// the package doc and docs/adr/0002-resumable-saga.md.
func (s *Service) RunReconciliationSweeper(ctx context.Context, interval, staleness time.Duration, batchSize int, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.sweepStaleBookings(ctx, staleness, batchSize, logger)
			if err != nil {
				logger.Error("sweep stale bookings failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("resumed stale bookings", "count", n)
			}
		}
	}
}

func (s *Service) sweepStaleBookings(ctx context.Context, staleness time.Duration, batchSize int, logger *slog.Logger) (int, error) {
	ids, err := s.repo.ListStaleBookings(ctx, nonTerminalStatuses, time.Now().Add(-staleness), batchSize)
	if err != nil {
		return 0, err
	}

	resumed := 0
	for _, id := range ids {
		booking, err := s.repo.GetBooking(ctx, id)
		if err != nil {
			logger.Error("sweeper: get stale booking failed", "booking_id", id, "error", err)
			continue
		}
		if _, err := s.advance(ctx, booking); err != nil {
			logger.Warn("sweeper: resume did not complete", "booking_id", id, "status", booking.Status, "error", err)
			continue
		}
		resumed++
	}
	return resumed, nil
}
