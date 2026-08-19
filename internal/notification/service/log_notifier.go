package service

import (
	"context"
	"log/slog"
)

type LogNotifier struct {
	logger *slog.Logger
}

func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Notify(ctx context.Context, event BookingEvent) error {
	n.logger.Info("notification",
		"booking_id", event.BookingID,
		"user_id", event.UserID,
		"trip_id", event.TripID,
		"status", event.Status,
		"pnr", event.PNR,
		"reason", event.Reason,
	)
	return nil
}
