package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Publisher is booking's view of a Kafka producer. pkg/kafka.Producer
// satisfies this by structural typing.
type Publisher interface {
	Publish(ctx context.Context, key string, value []byte) error
}

// RunOutboxRelay polls outbox_events for unpublished rows and publishes
// them to Kafka, keyed by booking ID. This is the other half of the
// transactional outbox (see docs/adr/0002-resumable-saga.md): the DB
// write that recorded the event already committed atomically with the
// booking status change; this loop's only job is to get it onto Kafka.
//
// If the process crashes between a successful Publish and marking the row
// published, the event is republished on the next tick - at-least-once,
// same as every other consumer boundary in this system. Downstream
// consumers (notification) must be idempotent per CLAUDE.md section 2.
func (s *Service) RunOutboxRelay(ctx context.Context, interval time.Duration, batchSize int, publisher Publisher, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.relayOutboxBatch(ctx, batchSize, publisher)
			if err != nil {
				logger.Error("outbox relay failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("relayed outbox events", "count", n)
			}
		}
	}
}

func (s *Service) relayOutboxBatch(ctx context.Context, batchSize int, publisher Publisher) (int, error) {
	events, err := s.repo.ListUnpublishedOutboxEvents(ctx, batchSize)
	if err != nil {
		return 0, fmt.Errorf("list unpublished outbox events: %w", err)
	}

	published := 0
	for _, e := range events {
		// Stop on the first failure rather than skip ahead: preserves
		// rough per-booking ordering and guarantees no event is marked
		// published without actually having been published.
		if err := publisher.Publish(ctx, e.BookingID, e.Payload); err != nil {
			return published, fmt.Errorf("publish event %d: %w", e.ID, err)
		}
		if err := s.repo.MarkOutboxEventPublished(ctx, e.ID); err != nil {
			return published, fmt.Errorf("mark event %d published: %w", e.ID, err)
		}
		published++
	}
	return published, nil
}
