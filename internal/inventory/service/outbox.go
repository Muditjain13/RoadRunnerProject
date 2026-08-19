package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Publisher is inventory's view of a Kafka producer. pkg/kafka.Producer
// satisfies this by structural typing - identical shape to booking's
// Publisher (internal/booking/service/outbox.go).
type Publisher interface {
	Publish(ctx context.Context, key string, value []byte) error
}

// RunOutboxRelay polls outbox_events for unpublished "seats freed" rows
// and publishes them to Kafka, keyed by trip ID. This is the durable half
// of waitlist promotion: tryPromoteAfterFree already attempts promotion
// synchronously for low latency, but if that attempt's process crashes
// before running, or a promotion opportunity is missed some other way,
// this relay plus the promotion consumer on the other end guarantee the
// waitlist head is still eventually promoted.
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
		if err := publisher.Publish(ctx, e.TripID, e.Payload); err != nil {
			return published, fmt.Errorf("publish event %d: %w", e.ID, err)
		}
		if err := s.repo.MarkOutboxEventPublished(ctx, e.ID); err != nil {
			return published, fmt.Errorf("mark event %d published: %w", e.ID, err)
		}
		published++
	}
	return published, nil
}
