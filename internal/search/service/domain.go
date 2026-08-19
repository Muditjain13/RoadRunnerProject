// Package service is search's fan-out logic: query every registered
// Provider in parallel, merge results, and degrade gracefully (return
// what came back, flag what didn't) instead of failing the whole request
// when one provider is slow or down. This is the concrete proof of
// CLAUDE.md's core engineering claim #4.
//
// Phase 1 registers exactly one provider (bus, via catalog). The fan-out
// logic itself never branches on mode - registering a train or flight
// Provider in a later phase is the entire change needed there.
package service

import (
	"context"
	"time"

	"github.com/Muditjain13/transport-app/pkg/money"
)

type Mode string

const (
	ModeBus    Mode = "bus"
	ModeTrain  Mode = "train"
	ModeFlight Mode = "flight"
)

type TripClass struct {
	Code      string
	FarePaise money.Paise
	Capacity  int
}

type Trip struct {
	ID            string
	RouteID       string
	OperatorID    string
	OperatorName  string
	Mode          Mode
	VehicleNumber string
	DepartureAt   time.Time
	ArrivalAt     time.Time
	Classes       []TripClass
}

type SearchParams struct {
	OriginCode string
	DestCode   string
	Date       time.Time
	// Modes restricts which providers are queried. Empty means "every
	// registered provider" - the default, and the only behavior that
	// existed before this field. A caller asking for one mode should never
	// pay the latency or circuit-breaker load of providers it didn't ask
	// for, so this filters which providers run, not which results get
	// discarded after the fact.
	Modes []Mode
}

// Provider is one upstream trip source. Search's fan-out treats every
// provider identically regardless of what's behind it.
type Provider interface {
	Mode() Mode
	Search(ctx context.Context, params SearchParams) ([]Trip, error)
}

// Cache is the Redis-backed result cache. Only complete (non-degraded)
// results are cached - see Service.Search.
type Cache interface {
	Get(ctx context.Context, key string) ([]Trip, bool, error)
	Set(ctx context.Context, key string, trips []Trip, ttl time.Duration) error
}

// SearchResult carries which providers, if any, failed or timed out, so
// callers (gateway, eventually the end user) can distinguish "no bus
// service on this route" from "the bus provider didn't answer in time."
type SearchResult struct {
	Trips             []Trip
	DegradedProviders []Mode
}
