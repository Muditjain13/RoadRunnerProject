package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("notification")

const dedupTTL = 10 * time.Minute

// MessageSource is the subset of *kafka.Reader this service depends on -
// small enough to fake in tests without a real Kafka broker. Fetch/Commit
// (not ReadMessage's auto-commit) so a message is only committed after
// Notify actually succeeds.
type MessageSource interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

type Service struct {
	notifier Notifier
	dedup    *dedupCache
	metrics  *Metrics
	logger   *slog.Logger
}

func NewService(notifier Notifier, metrics *Metrics, logger *slog.Logger) *Service {
	return &Service{notifier: notifier, dedup: newDedupCache(dedupTTL), metrics: metrics, logger: logger}
}

// Run drives a bounded pool of `workers` goroutines consuming from
// source until ctx is canceled. CLAUDE.md section 7: "Bounded worker
// pools for consumers. Never spawn an unbounded goroutine per message" -
// the single fetch loop below is the only thing that reads from Kafka; it
// hands messages to a fixed pool via a channel, so throughput is capped by
// worker count, not by how fast messages arrive.
func (s *Service) Run(ctx context.Context, source MessageSource, workers int) error {
	jobs := make(chan kafkago.Message, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range jobs {
				s.processMessage(ctx, source, msg)
			}
		}()
	}
	defer func() {
		close(jobs)
		wg.Wait()
	}()

	for {
		msg, err := source.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		select {
		case jobs <- msg:
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *Service) processMessage(ctx context.Context, source MessageSource, msg kafkago.Message) {
	// No gRPC/HTTP framework to hook a tracing interceptor into here, so
	// the span is created manually - the equivalent of what
	// otelgrpc/otelhttp do automatically for the request/response services.
	ctx, span := tracer.Start(ctx, "notification.process_message")
	defer span.End()

	var event BookingEvent
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		// A poison message can never be processed successfully - commit
		// past it rather than blocking the partition on it forever.
		s.logger.Error("invalid booking event payload, skipping", "error", err)
		span.SetStatus(codes.Error, "invalid payload")
		s.metrics.messagesTotal.WithLabelValues(outcomePoison).Inc()
		s.commit(ctx, source, msg)
		return
	}
	span.SetAttributes(attribute.String("booking_id", event.BookingID), attribute.String("status", event.Status))

	dedupKey := event.BookingID + "|" + event.Status
	if s.dedup.checkAndMark(dedupKey) {
		s.metrics.messagesTotal.WithLabelValues(outcomeDuplicate).Inc()
		s.commit(ctx, source, msg)
		return
	}

	if err := s.notifier.Notify(ctx, event); err != nil {
		s.logger.Error("notify failed, leaving uncommitted for redelivery", "booking_id", event.BookingID, "status", event.Status, "error", err)
		span.SetStatus(codes.Error, err.Error())
		s.metrics.messagesTotal.WithLabelValues(outcomeNotifyFailed).Inc()
		return
	}
	s.metrics.messagesTotal.WithLabelValues(outcomeNotified).Inc()
	s.commit(ctx, source, msg)
}

func (s *Service) commit(ctx context.Context, source MessageSource, msg kafkago.Message) {
	if err := source.CommitMessages(ctx, msg); err != nil {
		s.logger.Error("commit failed", "error", err)
	}
}
