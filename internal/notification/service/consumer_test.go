package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	kafkago "github.com/segmentio/kafka-go"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeSource struct {
	messages []kafkago.Message
	idx      int

	mu        sync.Mutex
	committed []kafkago.Message
}

func (f *fakeSource) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	if f.idx < len(f.messages) {
		msg := f.messages[f.idx]
		f.idx++
		return msg, nil
	}
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (f *fakeSource) CommitMessages(ctx context.Context, msgs ...kafkago.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.committed = append(f.committed, msgs...)
	return nil
}

func (f *fakeSource) committedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.committed)
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []BookingEvent
	err   error
}

func (n *fakeNotifier) Notify(ctx context.Context, event BookingEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, event)
	return n.err
}

func (n *fakeNotifier) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.calls)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func runAndStop(t *testing.T, svc *Service, src MessageSource, workers int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, src, workers) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func TestConsumer_ProcessesAndCommits(t *testing.T) {
	src := &fakeSource{messages: []kafkago.Message{{Value: []byte(`{"booking_id":"b1","status":"confirmed"}`)}}}
	notifier := &fakeNotifier{}
	svc := NewService(notifier, NewMetrics(prometheus.NewRegistry()), testLogger())
	runAndStop(t, svc, src, 2)

	waitFor(t, time.Second, func() bool { return notifier.callCount() == 1 })
	waitFor(t, time.Second, func() bool { return src.committedCount() == 1 })
}

// TestConsumer_DedupSkipsNotifyButStillCommits uses a single worker so
// message processing is strictly sequential, making the dedup outcome
// deterministic rather than a race between two concurrent workers.
func TestConsumer_DedupSkipsNotifyButStillCommits(t *testing.T) {
	payload := []byte(`{"booking_id":"b1","status":"confirmed"}`)
	src := &fakeSource{messages: []kafkago.Message{{Value: payload}, {Value: payload}}}
	notifier := &fakeNotifier{}
	svc := NewService(notifier, NewMetrics(prometheus.NewRegistry()), testLogger())
	runAndStop(t, svc, src, 1)

	waitFor(t, time.Second, func() bool { return src.committedCount() == 2 })
	if notifier.callCount() != 1 {
		t.Errorf("Notify called %d times, want 1 (second message is a duplicate)", notifier.callCount())
	}
}

func TestConsumer_NotifyFailureLeavesUncommitted(t *testing.T) {
	src := &fakeSource{messages: []kafkago.Message{{Value: []byte(`{"booking_id":"b1","status":"confirmed"}`)}}}
	notifier := &fakeNotifier{err: errors.New("smtp down")}
	svc := NewService(notifier, NewMetrics(prometheus.NewRegistry()), testLogger())
	runAndStop(t, svc, src, 1)

	waitFor(t, time.Second, func() bool { return notifier.callCount() == 1 })
	// Waiting for an absence: give a bounded window for a wrongful commit
	// to show up, then assert it didn't.
	time.Sleep(50 * time.Millisecond)
	if src.committedCount() != 0 {
		t.Errorf("committed %d messages, want 0 (Notify failed, must not commit)", src.committedCount())
	}
}

func TestConsumer_PoisonMessageCommittedWithoutNotify(t *testing.T) {
	src := &fakeSource{messages: []kafkago.Message{{Value: []byte(`not json`)}}}
	notifier := &fakeNotifier{}
	svc := NewService(notifier, NewMetrics(prometheus.NewRegistry()), testLogger())
	runAndStop(t, svc, src, 1)

	waitFor(t, time.Second, func() bool { return src.committedCount() == 1 })
	if notifier.callCount() != 0 {
		t.Errorf("Notify called %d times, want 0 (poison message must not be notified)", notifier.callCount())
	}
}

func TestConsumer_StopsOnContextCancel(t *testing.T) {
	src := &fakeSource{}
	notifier := &fakeNotifier{}
	svc := NewService(notifier, NewMetrics(prometheus.NewRegistry()), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, src, 2) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v, want nil on clean shutdown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

// flakySource fails the first `failures` fetches like an unreachable broker,
// then behaves like fakeSource.
type flakySource struct {
	fakeSource
	failures int
	mu2      sync.Mutex
	calls    int
}

func (f *flakySource) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	f.mu2.Lock()
	f.calls++
	failing := f.calls <= f.failures
	f.mu2.Unlock()
	if failing {
		return kafkago.Message{}, errors.New("dial tcp: connection refused")
	}
	return f.fakeSource.FetchMessage(ctx)
}

func (f *flakySource) fetchCalls() int {
	f.mu2.Lock()
	defer f.mu2.Unlock()
	return f.calls
}

func shrinkBackoff(t *testing.T) {
	t.Helper()
	old := fetchBackoff
	fetchBackoff.Initial, fetchBackoff.Max = time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() { fetchBackoff = old })
}

// TestConsumer_SurvivesBrokerOutageAtStartup is the regression test for the
// consumer exiting on its first fetch error: it must keep retrying and then
// process messages once the broker is reachable.
func TestConsumer_SurvivesBrokerOutageAtStartup(t *testing.T) {
	shrinkBackoff(t)
	src := &flakySource{
		fakeSource: fakeSource{messages: []kafkago.Message{{Value: []byte(`{"booking_id":"b1","status":"confirmed"}`)}}},
		failures:   3,
	}
	notifier := &fakeNotifier{}
	svc := NewService(notifier, NewMetrics(prometheus.NewRegistry()), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, src, 1) }()

	waitFor(t, 2*time.Second, func() bool { return notifier.callCount() == 1 })
	waitFor(t, 2*time.Second, func() bool { return src.committedCount() == 1 })
	if src.fetchCalls() < 4 {
		t.Errorf("fetch calls = %d, want at least 4 (3 failures then a success)", src.fetchCalls())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v, want nil on clean shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}

func TestConsumer_StopsOnCancelWhileBrokerDown(t *testing.T) {
	old := fetchBackoff
	fetchBackoff.Initial, fetchBackoff.Max = time.Minute, time.Minute
	t.Cleanup(func() { fetchBackoff = old })

	src := &flakySource{failures: 1 << 30}
	svc := NewService(&fakeNotifier{}, NewMetrics(prometheus.NewRegistry()), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- svc.Run(ctx, src, 1) }()
	waitFor(t, time.Second, func() bool { return src.fetchCalls() >= 1 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run() error = %v, want nil on clean shutdown", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop while sleeping in backoff")
	}
}
