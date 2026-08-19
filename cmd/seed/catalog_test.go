package main

import (
	"testing"
	"time"

	"github.com/Muditjain13/transport-app/internal/catalog/service"
	"github.com/Muditjain13/transport-app/pkg/geo"
)

func TestGenerateBusTrips_RespectsDistanceCap(t *testing.T) {
	routes := allRoutes()
	operators := busOperators()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	trips := generateBusTrips(routes, operators, 3, 2, now)

	routeByID := make(map[string]service.Route, len(routes))
	for _, r := range routes {
		routeByID[r.ID] = r
	}

	for _, trip := range trips {
		d := routeByID[trip.RouteID].DistanceKm
		if d > maxBusDistanceKm {
			t.Errorf("trip %s on route %s has distance %dkm, exceeds bus cap %dkm", trip.ID, trip.RouteID, d, maxBusDistanceKm)
		}
	}
}

func TestGenerateBusTrips_NoTripsToOrFromGAU(t *testing.T) {
	routes := allRoutes()
	operators := busOperators()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	routeByID := make(map[string]service.Route, len(routes))
	for _, r := range routes {
		routeByID[r.ID] = r
	}

	trips := generateBusTrips(routes, operators, 3, 2, now)

	for _, trip := range trips {
		r := routeByID[trip.RouteID]
		if r.OriginCode == geo.GAU || r.DestCode == geo.GAU {
			t.Errorf("expected no bus trips touching GAU (all distances exceed 1500km), got trip %s on route %s", trip.ID, trip.RouteID)
		}
	}
}

func TestGenerateBusTrips_Deterministic(t *testing.T) {
	routes := allRoutes()
	operators := busOperators()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	a := generateBusTrips(routes, operators, 5, 3, now)
	b := generateBusTrips(routes, operators, 5, 3, now)

	if len(a) != len(b) {
		t.Fatalf("non-deterministic trip count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].OperatorID != b[i].OperatorID || a[i].Classes[0].Code != b[i].Classes[0].Code {
			t.Fatalf("non-deterministic trip at index %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestGenerateBusTrips_VolumeMatchesProfile(t *testing.T) {
	routes := allRoutes()
	operators := busOperators()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	daysForward, tripsPerDay := 7, 2
	trips := generateBusTrips(routes, operators, daysForward, tripsPerDay, now)

	busEligibleRoutes := 0
	for _, r := range routes {
		if r.DistanceKm <= maxBusDistanceKm {
			busEligibleRoutes++
		}
	}
	want := busEligibleRoutes * daysForward * tripsPerDay
	if len(trips) != want {
		t.Errorf("generateBusTrips() returned %d trips, want %d (%d eligible routes * %d days * %d/day)",
			len(trips), want, busEligibleRoutes, daysForward, tripsPerDay)
	}
}
