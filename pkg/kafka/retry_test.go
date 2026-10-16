package kafka

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

var errBroker = errors.New("dial tcp: connection refused")

// recordingBackoff returns a Backoff that records requested sleeps instead of
// waiting for them.
func recordingBackoff(initial, max time.Duration) (*Backoff, *[]time.Duration) {
	var waits []time.Duration
	b := &Backoff{Initial: initial, Max: max}
	b.sleep = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return ctx.Err()
	}
	return b, &waits
}

func TestFetchWithRetry_RetriesWithExponentialBackoffAndCap(t *testing.T) {
	b, waits := recordingBackoff(time.Second, 5*time.Second)
	calls := 0
	fetch := func(context.Context) (kafka.Message, error) {
		calls++
		if calls <= 5 {
			return kafka.Message{}, errBroker
		}
		return kafka.Message{Value: []byte("ok")}, nil
	}

	msg, err := FetchWithRetry(context.Background(), fetch, b, nil)
	if err != nil {
		t.Fatalf("FetchWithRetry() error = %v, want nil", err)
	}
	if string(msg.Value) != "ok" {
		t.Errorf("message = %q, want ok", msg.Value)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	if len(*waits) != len(want) {
		t.Fatalf("waits = %v, want %v", *waits, want)
	}
	for i := range want {
		if (*waits)[i] != want[i] {
			t.Errorf("wait[%d] = %v, want %v (doubling, capped at 5s)", i, (*waits)[i], want[i])
		}
	}
}

func TestFetchWithRetry_ResetsBackoffAfterSuccess(t *testing.T) {
	b, waits := recordingBackoff(time.Second, time.Minute)
	script := []error{errBroker, errBroker, nil, errBroker, nil}
	i := 0
	fetch := func(context.Context) (kafka.Message, error) {
		err := script[i]
		i++
		return kafka.Message{}, err
	}

	for n := 0; n < 2; n++ {
		if _, err := FetchWithRetry(context.Background(), fetch, b, nil); err != nil {
			t.Fatalf("fetch %d: %v", n, err)
		}
	}
	want := []time.Duration{time.Second, 2 * time.Second, time.Second}
	if len(*waits) != len(want) {
		t.Fatalf("waits = %v, want %v", *waits, want)
	}
	for k := range want {
		if (*waits)[k] != want[k] {
			t.Errorf("wait[%d] = %v, want %v (schedule restarts after a success)", k, (*waits)[k], want[k])
		}
	}
}

func TestFetchWithRetry_ContextCancelDuringBackoffReturnsPromptly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Real timer, one minute long: only cancellation can end the wait.
	b := &Backoff{Initial: time.Minute, Max: time.Minute}
	fetch := func(context.Context) (kafka.Message, error) { return kafka.Message{}, errBroker }

	done := make(chan error, 1)
	go func() {
		_, err := FetchWithRetry(ctx, fetch, b, nil)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchWithRetry did not return after cancel; shutdown would hang in backoff")
	}
}

func TestFetchWithRetry_DoesNotRetryClosedReader(t *testing.T) {
	b, waits := recordingBackoff(time.Second, time.Minute)
	calls := 0
	fetch := func(context.Context) (kafka.Message, error) {
		calls++
		return kafka.Message{}, io.EOF
	}

	_, err := FetchWithRetry(context.Background(), fetch, b, nil)
	if !errors.Is(err, io.EOF) {
		t.Errorf("error = %v, want io.EOF", err)
	}
	if calls != 1 || len(*waits) != 0 {
		t.Errorf("calls = %d, waits = %v, want 1 call and no waits", calls, *waits)
	}
}
