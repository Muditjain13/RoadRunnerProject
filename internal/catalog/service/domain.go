// Package service holds catalog's business logic: domain types and the
// Repository interface it depends on. The interface is defined here, next
// to its consumer, not in the repo package - repo only provides an
// implementation.
package service

import (
	"context"
	"time"

	"github.com/Muditjain13/transport-app/pkg/money"
)

// Mode is a transport mode. Phase 1 only ever populates "bus", but the
// field exists now so train and flight (Phase 3/4) are additive, not a
// rewrite.
type Mode string

const (
	ModeBus    Mode = "bus"
	ModeTrain  Mode = "train"
	ModeFlight Mode = "flight"
)

// Operator runs trips on one or more routes.
type Operator struct {
	ID   string
	Name string
	Mode Mode
}

// Route is a directed city pair with a fixed road distance.
type Route struct {
	ID         string
	OriginCode string
	DestCode   string
	DistanceKm int
}

// TripClass is one bookable class on a trip (e.g. bus "seater"), with the
// fare for that class and the number of seats catalog says the vehicle has.
// Live seat availability is NOT here - that is inventory's job. Catalog
// only knows the static shape of the vehicle.
type TripClass struct {
	Code      string // e.g. "seater", "sleeper", "ac_sleeper"
	FarePaise money.Paise
	Capacity  int
}

// Trip is one scheduled departure.
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

// SearchParams filters trips for a search request.
type SearchParams struct {
	OriginCode string
	DestCode   string
	Date       time.Time // departure date, caller's local calendar day in UTC
	Mode       Mode
}

// Repository is what the catalog service needs from storage. Defined here
// (the consumer) per CLAUDE.md section 7; repo/ provides the Mongo
// implementation.
type Repository interface {
	FindTrips(ctx context.Context, params SearchParams) ([]Trip, error)
	GetTrip(ctx context.Context, tripID string) (Trip, error)
	GetRoute(ctx context.Context, routeID string) (Route, error)
	GetOperator(ctx context.Context, operatorID string) (Operator, error)
}

// Service implements the catalog use cases on top of a Repository.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SearchTrips returns scheduled trips matching the search params, ordered
// by departure time. Empty results (e.g. no bus service on a long route,
// no rail to SXR) are a valid, non-error response - callers must handle
// zero-length slices as a real outcome, not a failure.
func (s *Service) SearchTrips(ctx context.Context, params SearchParams) ([]Trip, error) {
	return s.repo.FindTrips(ctx, params)
}

func (s *Service) GetTrip(ctx context.Context, tripID string) (Trip, error) {
	return s.repo.GetTrip(ctx, tripID)
}
