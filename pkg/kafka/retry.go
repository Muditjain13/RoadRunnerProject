package kafka

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
)

// Default backoff for consumer fetch loops: start at 500ms, double on every
// consecutive failure, never wait longer than 30s.
const (
	DefaultBackoffInitial = 500 * time.Millisecond
	DefaultBackoffMax     = 30 * time.Second
)

// Backoff is the retry schedule for a consumer's fetch loop. The zero value
// uses the defaults. A Backoff holds the current delay, so use one per loop
// and do not share it between goroutines.
type Backoff struct {
	Initial time.Duration
	Max     time.Duration

	current time.Duration
	// sleep is replaced in tests so they do not wait in real time.
	sleep func(ctx context.Context, d time.Duration) error
}

func (b *Backoff) next() time.Duration {
	initial, max := b.Initial, b.Max
	if initial <= 0 {
		initial = DefaultBackoffInitial
	}
	if max <= 0 {
		max = DefaultBackoffMax
	}
	if b.current == 0 {
		b.current = initial
	} else {
		b.current *= 2
	}
	if b.current > max {
		b.current = max
	}
	return b.current
}

// Reset starts the schedule over; call it after a successful fetch.
func (b *Backoff) Reset() { b.current = 0 }

func (b *Backoff) wait(ctx context.Context, d time.Duration) error {
	if b.sleep != nil {
		return b.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FetchWithRetry calls fetch until it returns a message, sleeping with
// exponential backoff between failures so a broker that is down at startup,
// or drops later, does not kill the consumer. It returns early, without
// retrying, when ctx is done (so shutdown still works, including during a
// backoff sleep) or when fetch reports io.EOF, which is how a closed
// kafka.Reader says it is finished.
func FetchWithRetry(
	ctx context.Context,
	fetch func(context.Context) (kafka.Message, error),
	b *Backoff,
	logger *slog.Logger,
) (kafka.Message, error) {
	for {
		msg, err := fetch(ctx)
		if err == nil {
			b.Reset()
			return msg, nil
		}
		if ctx.Err() != nil {
			return kafka.Message{}, ctx.Err()
		}
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
			return kafka.Message{}, err
		}
		delay := b.next()
		if logger != nil {
			logger.Warn("kafka fetch failed, retrying", "error", err, "retry_in", delay.String())
		}
		if werr := b.wait(ctx, delay); werr != nil {
			return kafka.Message{}, werr
		}
	}
}
