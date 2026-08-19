// Package service is notification's Kafka consumer: fan out on booking
// lifecycle events. CLAUDE.md section 2: "Idempotency everywhere. Every
// Kafka consumer... At-least-once delivery is assumed."
//
// notification has no store of its own (CLAUDE.md section 3), so its
// idempotency is necessarily weaker than inventory's or payment's:
// duplicate delivery within a process's lifetime is deduped via an
// in-memory cache keyed by (booking ID, status) - each booking moves
// through a given status exactly once, so that pair is a stable natural
// key. A process restart loses that cache. This is an accepted tradeoff,
// not an oversight: the cost of a duplicate notification (one extra
// email/SMS) is low enough that a persisted dedup log isn't worth the
// store this service deliberately doesn't have. Contrast with payment's
// idempotency_key, where a duplicate means double-charging a customer.
package service

import "context"

// BookingEvent mirrors the payload internal/booking/service publishes to
// the outbox (its own private type - see CLAUDE.md section 6 on
// cross-service internal/ imports being forbidden, hence the duplicate
// shape here rather than a shared import).
type BookingEvent struct {
	BookingID string `json:"booking_id"`
	PNR       string `json:"pnr,omitempty"`
	UserID    string `json:"user_id"`
	TripID    string `json:"trip_id"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
}

// Notifier sends the actual notification. The only implementation in
// this repo logs it - there is no real email/SMS/push provider in this
// architecture, and none is needed to demonstrate the consumer pattern.
type Notifier interface {
	Notify(ctx context.Context, event BookingEvent) error
}
