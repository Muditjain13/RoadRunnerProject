package service

import (
	"context"
	"log/slog"
	"time"
)

// RunSweeper polls for expired holds and releases them until ctx is
// canceled. CLAUDE.md section 4: chosen over Kafka delayed messages for
// operational simplicity - see docs/adr for the tradeoff.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration, batchSize int, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := s.SweepExpiredHolds(ctx, batchSize)
			if err != nil {
				logger.Error("sweep expired holds failed", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("released expired holds", "count", n)
			}
		}
	}
}
