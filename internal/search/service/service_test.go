package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeCache struct {
	mu    sync.Mutex
	store map[string][]Trip
}

func newFakeCache() *fakeCache {
	return &fakeCache{store: make(map[string][]Trip)}
}

func (c *fakeCache) Get(ctx context.Context, key string) ([]Trip, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	trips, ok := c.store[key]
	return trips, ok, nil
}

func (c *fakeCache) Set(ctx context.Context, key string, trips []Trip, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[key] = trips
	return nil
}

type fakeProvider struct {
	mode  Mode
	delay time.Duration
	err   error
	trips []Trip
	calls int32
}

func (p *fakeProvider) Mode() Mode { return p.mode }

func (p *fakeProvider) Search(ctx context.Context, params SearchParams) ([]Trip, error) {
	p.calls++
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	return p.trips, nil
}

func params() SearchParams {
	return SearchParams{OriginCode: "DEL", DestCode: "BOM", Date: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
}

func TestSearch_MergesAllProviders(t *testing.T) {
	dep1 := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	dep2 := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)
	bus := &fakeProvider{mode: "bus", trips: []Trip{{ID: "bus-1", DepartureAt: dep1}}}
	train := &fakeProvider{mode: "train", trips: []Trip{{ID: "train-1", DepartureAt: dep2}}}

	svc := NewService([]Provider{bus, train}, newFakeCache())
	result, err := svc.Search(context.Background(), params())
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Trips) != 2 {
		t.Fatalf("got %d trips, want 2", len(result.Trips))
	}
	// Merged results are sorted by departure time.
	if result.Trips[0].ID != "train-1" || result.Trips[1].ID != "bus-1" {
		t.Errorf("trips not sorted by departure: %+v", result.Trips)
	}
	if len(result.DegradedProviders) != 0 {
		t.Errorf("DegradedProviders = %v, want none", result.DegradedProviders)
	}
}

// TestSearch_ModeFilterSkipsOtherProviders proves Modes filters which
// providers actually run, not just which results survive after the fact -
// the unrequested provider must never be called at all.
func TestSearch_ModeFilterSkipsOtherProviders(t *testing.T) {
	bus := &fakeProvider{mode: "bus", trips: []Trip{{ID: "bus-1"}}}
	train := &fakeProvider{mode: "train", trips: []Trip{{ID: "train-1"}}}

	svc := NewService([]Provider{bus, train}, newFakeCache())
	p := params()
	p.Modes = []Mode{"train"}
	result, err := svc.Search(context.Background(), p)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Trips) != 1 || result.Trips[0].ID != "train-1" {
		t.Fatalf("trips = %+v, want only train-1", result.Trips)
	}
	if bus.calls != 0 {
		t.Errorf("bus provider called %d times, want 0 (not requested)", bus.calls)
	}
	if train.calls != 1 {
		t.Errorf("train provider called %d times, want 1", train.calls)
	}
}

func TestSearch_ModeFilterDoesNotShareCacheWithUnfiltered(t *testing.T) {
	bus := &fakeProvider{mode: "bus", trips: []Trip{{ID: "bus-1"}}}
	train := &fakeProvider{mode: "train", trips: []Trip{{ID: "train-1"}}}
	svc := NewService([]Provider{bus, train}, newFakeCache())

	pFiltered := params()
	pFiltered.Modes = []Mode{"train"}
	if _, err := svc.Search(context.Background(), pFiltered); err != nil {
		t.Fatal(err)
	}

	result, err := svc.Search(context.Background(), params()) // unfiltered
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trips) != 2 {
		t.Fatalf("unfiltered search after a filtered one got %d trips, want 2 (must not have served the train-only cache entry)", len(result.Trips))
	}
}

// TestSearch_OneProviderFailingDoesNotAffectOthers is the core proof of
// CLAUDE.md's graceful-degradation claim: a provider timing out must not
// cancel or block the others, and must not fail the whole request.
func TestSearch_OneProviderFailingDoesNotAffectOthers(t *testing.T) {
	bus := &fakeProvider{mode: "bus", trips: []Trip{{ID: "bus-1"}}}
	slowFlight := &fakeProvider{mode: "flight", delay: 500 * time.Millisecond}

	svc := NewService([]Provider{bus, slowFlight}, newFakeCache())
	svc.providerTimeout = 50 * time.Millisecond

	start := time.Now()
	result, err := svc.Search(context.Background(), params())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Search() error = %v, want nil (partial results are not an error)", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("Search() took %v, want ~providerTimeout (bus must not wait for slow flight)", elapsed)
	}
	if len(result.Trips) != 1 || result.Trips[0].ID != "bus-1" {
		t.Errorf("Trips = %+v, want just bus-1", result.Trips)
	}
	if len(result.DegradedProviders) != 1 || result.DegradedProviders[0] != "flight" {
		t.Errorf("DegradedProviders = %v, want [flight]", result.DegradedProviders)
	}
}

func TestSearch_AllProvidersFail(t *testing.T) {
	bus := &fakeProvider{mode: "bus", err: errors.New("boom")}
	svc := NewService([]Provider{bus}, newFakeCache())

	result, err := svc.Search(context.Background(), params())
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if len(result.Trips) != 0 {
		t.Errorf("Trips = %v, want empty", result.Trips)
	}
	if len(result.DegradedProviders) != 1 {
		t.Errorf("DegradedProviders = %v, want [bus]", result.DegradedProviders)
	}
}

func TestSearch_EmptyResultIsNotAnError(t *testing.T) {
	bus := &fakeProvider{mode: "bus", trips: nil}
	svc := NewService([]Provider{bus}, newFakeCache())

	result, err := svc.Search(context.Background(), params())
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}
	if len(result.Trips) != 0 || len(result.DegradedProviders) != 0 {
		t.Errorf("result = %+v, want empty trips and no degraded providers (genuinely no service)", result)
	}
}

func TestSearch_CacheHit(t *testing.T) {
	bus := &fakeProvider{mode: "bus", trips: []Trip{{ID: "bus-1"}}}
	svc := NewService([]Provider{bus}, newFakeCache())

	if _, err := svc.Search(context.Background(), params()); err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	if _, err := svc.Search(context.Background(), params()); err != nil {
		t.Fatalf("second Search() error = %v", err)
	}
	if bus.calls != 1 {
		t.Errorf("provider called %d times, want 1 (second search should hit cache)", bus.calls)
	}
}

func TestSearch_DegradedResultsAreNotCached(t *testing.T) {
	bus := &fakeProvider{mode: "bus", err: errors.New("boom")}
	svc := NewService([]Provider{bus}, newFakeCache())

	if _, err := svc.Search(context.Background(), params()); err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	if _, err := svc.Search(context.Background(), params()); err != nil {
		t.Fatalf("second Search() error = %v", err)
	}
	if bus.calls != 2 {
		t.Errorf("provider called %d times, want 2 (a degraded result must not be cached)", bus.calls)
	}
}

func TestSearch_ValidatesParams(t *testing.T) {
	svc := NewService(nil, newFakeCache())
	if _, err := svc.Search(context.Background(), SearchParams{}); err == nil {
		t.Error("Search() with empty params: error = nil, want error")
	}
}

// TestSearch_CircuitBreakerOpensAfterRepeatedFailures proves the circuit
// breaker actually engages: once a provider has failed enough times, it
// should stop being called at all (fail fast) rather than retried on
// every search.
func TestSearch_CircuitBreakerOpensAfterRepeatedFailures(t *testing.T) {
	bus := &fakeProvider{mode: "bus", err: errors.New("boom")}
	svc := NewService([]Provider{bus}, newFakeCache())

	// Default breaker threshold is 5; trigger enough failures to open it.
	for i := 0; i < 5; i++ {
		if _, err := svc.Search(context.Background(), SearchParams{OriginCode: "DEL", DestCode: "BOM", Date: time.Date(2026, 1, i+1, 0, 0, 0, 0, time.UTC)}); err != nil {
			t.Fatalf("Search() error = %v", err)
		}
	}
	callsBeforeOpen := bus.calls

	// One more, different cache key so it's not a cache hit - the breaker
	// should now reject without calling the provider.
	if _, err := svc.Search(context.Background(), SearchParams{OriginCode: "DEL", DestCode: "BOM", Date: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if bus.calls != callsBeforeOpen {
		t.Errorf("provider called after breaker should be open: calls went from %d to %d", callsBeforeOpen, bus.calls)
	}
}
