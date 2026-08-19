package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Muditjain13/transport-app/pkg/circuitbreaker"
)

const (
	defaultProviderTimeout = 3 * time.Second
	defaultCacheTTL        = 60 * time.Second
)

type Service struct {
	providers       []Provider
	breakers        map[Mode]*circuitbreaker.Breaker
	cache           Cache
	providerTimeout time.Duration
	cacheTTL        time.Duration
}

func NewService(providers []Provider, cache Cache) *Service {
	breakers := make(map[Mode]*circuitbreaker.Breaker, len(providers))
	for _, p := range providers {
		// Threshold and reset window are conservative defaults for an
		// in-process dependency (catalog); a genuinely flaky external
		// GDS (Phase 4's flight provider) may want tighter values.
		breakers[p.Mode()] = circuitbreaker.New(5, 30*time.Second)
	}
	return &Service{
		providers:       providers,
		breakers:        breakers,
		cache:           cache,
		providerTimeout: defaultProviderTimeout,
		cacheTTL:        defaultCacheTTL,
	}
}

type providerOutcome struct {
	mode  Mode
	trips []Trip
	err   error
}

// Search fans out to every registered provider concurrently. It uses
// errgroup.WithContext per CLAUDE.md section 7 for goroutine lifecycle
// management and propagating the caller's own cancellation, but each
// provider's outcome is captured into its own result slot rather than
// returned to the errgroup - a provider failing or timing out must never
// cancel the others via errgroup's default "first error cancels the
// group" behavior, or "partial results on timeout" would be impossible.
func (s *Service) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	if params.OriginCode == "" || params.DestCode == "" {
		return SearchResult{}, fmt.Errorf("search: origin and dest are required")
	}

	key := cacheKey(params)
	if cached, ok, err := s.cache.Get(ctx, key); err == nil && ok {
		return SearchResult{Trips: cached}, nil
	}

	providers := s.providers
	if len(params.Modes) > 0 {
		want := make(map[Mode]bool, len(params.Modes))
		for _, m := range params.Modes {
			want[m] = true
		}
		providers = nil
		for _, p := range s.providers {
			if want[p.Mode()] {
				providers = append(providers, p)
			}
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	outcomes := make([]providerOutcome, len(providers))
	for i, p := range providers {
		i, p := i, p
		g.Go(func() error {
			outcomes[i] = s.callProvider(gctx, p, params)
			return nil // never propagate to errgroup - see doc comment above
		})
	}
	_ = g.Wait() // always nil: no goroutine above returns a non-nil error

	var trips []Trip
	var degraded []Mode
	for _, o := range outcomes {
		if o.err != nil {
			degraded = append(degraded, o.mode)
			continue
		}
		trips = append(trips, o.trips...)
	}
	sort.Slice(trips, func(i, j int) bool { return trips[i].DepartureAt.Before(trips[j].DepartureAt) })

	if len(degraded) == 0 {
		// Only cache complete results - a degraded result must not
		// paper over a provider outage for other callers during the TTL.
		_ = s.cache.Set(ctx, key, trips, s.cacheTTL)
	}
	return SearchResult{Trips: trips, DegradedProviders: degraded}, nil
}

func (s *Service) callProvider(ctx context.Context, p Provider, params SearchParams) providerOutcome {
	provCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()

	var trips []Trip
	err := s.breakers[p.Mode()].Do(provCtx, func(ctx context.Context) error {
		var err error
		trips, err = p.Search(ctx, params)
		return err
	})
	return providerOutcome{mode: p.Mode(), trips: trips, err: err}
}

func cacheKey(p SearchParams) string {
	modes := make([]string, len(p.Modes))
	for i, m := range p.Modes {
		modes[i] = string(m)
	}
	sort.Strings(modes)
	return strings.Join([]string{"search", p.OriginCode, p.DestCode, p.Date.Format("2006-01-02"), strings.Join(modes, ",")}, ":")
}
