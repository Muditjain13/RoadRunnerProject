package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	kafkago "github.com/segmentio/kafka-go"
)

// PromotionMessageSource is the subset of *kafka.Reader the promotion
// consumer depends on - identical shape to notification's MessageSource
// (internal/notification/service/consumer.go), small enough to fake in
// tests without a real broker.
type PromotionMessageSource interface {
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// RunPromotionConsumer drives a bounded pool of `workers` goroutines
// consuming seats-freed events from source until ctx is canceled.
// CLAUDE.md section 7: "Bounded worker pools for consumers." A poison
// (unparseable) message is committed past rather than retried forever;
// any other failure to promote is logged and left uncommitted for
// redelivery - PromoteWaitlistHead is idempotent (guarded by the
// waitlist head's row lock and its pending-only query), so redelivery
// can never double-promote.
func (s *Service) RunPromotionConsumer(ctx context.Context, source PromotionMessageSource, workers int, logger *slog.Logger) error {
	jobs := make(chan kafkago.Message, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for msg := range jobs {
				s.processPromotionMessage(ctx, source, msg, logger)
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

func (s *Service) processPromotionMessage(ctx context.Context, source PromotionMessageSource, msg kafkago.Message, logger *slog.Logger) {
	var event seatsFreedPayload
	if err := json.Unmarshal(msg.Value, &event); err != nil {
		logger.Error("invalid seats-freed payload, skipping", "error", err)
		s.commitPromotion(ctx, source, msg, logger)
		return
	}

	if _, err := s.PromoteWaitlistHead(ctx, event.TripID, event.ClassCode); err != nil {
		logger.Error("promote waitlist head failed, leaving uncommitted for redelivery",
			"trip_id", event.TripID, "class_code", event.ClassCode, "error", err)
		return
	}
	s.commitPromotion(ctx, source, msg, logger)
}

func (s *Service) commitPromotion(ctx context.Context, source PromotionMessageSource, msg kafkago.Message, logger *slog.Logger) {
	if err := source.CommitMessages(ctx, msg); err != nil {
		logger.Error("commit failed", "error", err)
	}
}
