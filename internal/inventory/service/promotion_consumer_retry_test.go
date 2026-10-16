package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// brokerSource fails the first `failures` fetches like an unreachable broker,
// then hands out messages, then blocks until ctx is done.
type brokerSource struct {
	mu        sync.Mutex
	failures  int
	calls     int
	messages  []kafkago.Message
	committed int
}

func (b *brokerSource) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	b.mu.Lock()
	b.calls++
	if b.calls <= b.failures {
		b.mu.Unlock()
		return kafkago.Message{}, errors.New("failed to dial: connection refused")
	}
	if len(b.messages) > 0 {
		m := b.messages[0]
		b.messages = b.messages[1:]
		b.mu.Unlock()
		return m, nil
	}
	b.mu.Unlock()
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (b *brokerSource) CommitMessages(ctx context.Context, msgs ...kafkago.Message) error {
	b.mu.Lock()
	b.committed += len(msgs)
	b.mu.Unlock()
	return nil
}

func (b *brokerSource) stats() (calls, committed int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, b.committed
}

func eventually(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// TestPromotionConsumer_SurvivesBrokerOutage is the regression test for the
// consumer returning on its first fetch error and staying dead until restart.
// The message is a poison payload, which is committed without touching the
// repository, so only the fetch retry is under test.
func TestPromotionConsumer_SurvivesBrokerOutage(t *testing.T) {
	old := fetchBackoff
	fetchBackoff.Initial, fetchBackoff.Max = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { fetchBackoff = old })

	src := &brokerSource{failures: 3, messages: []kafkago.Message{{Value: []byte("not json")}}}
	svc := NewService(newFakeRepo(nil))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunPromotionConsumer(ctx, src, 1, logger) }()

	eventually(t, 2*time.Second, func() bool { _, c := src.stats(); return c == 1 })
	if calls, _ := src.stats(); calls < 4 {
		t.Errorf("fetch calls = %d, want at least 4 (3 failures then a success)", calls)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunPromotionConsumer() error = %v, want nil on clean shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not stop after cancel")
	}
}

func TestPromotionConsumer_StopsOnCancelWhileBrokerDown(t *testing.T) {
	old := fetchBackoff
	fetchBackoff.Initial, fetchBackoff.Max = time.Minute, time.Minute
	t.Cleanup(func() { fetchBackoff = old })

	src := &brokerSource{failures: 1 << 30}
	svc := NewService(newFakeRepo(nil))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.RunPromotionConsumer(ctx, src, 1, logger) }()
	eventually(t, time.Second, func() bool { c, _ := src.stats(); return c >= 1 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("error = %v, want nil on clean shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not stop while sleeping in backoff")
	}
}
