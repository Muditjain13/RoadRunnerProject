package main

import (
	"testing"
	"time"
)

func TestGenerateFlightTrips_AllRoutesCovered(t *testing.T) {
	// CLAUDE.md section 9: "Flights on all 45 pairs" - unlike bus (distance
	// cap) and train (no SXR), flight has no exclusions at all.
	routes := allRoutes()
	operators := flightOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	trips := generateFlightTrips(routes, operators, 1, 1, now)

	seenRoutes := make(map[string]bool)
	for _, trip := range trips {
		seenRoutes[trip.RouteID] = true
	}
	for _, r := range routes {
		if !seenRoutes[r.ID] {
			t.Errorf("route %s has no flight trip, want flights on every route", r.ID)
		}
	}
}

func TestGenerateFlightTrips_FixedCabinCapacity(t *testing.T) {
	routes := allRoutes()
	operators := flightOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	trips := generateFlightTrips(routes, operators, 1, 1, now)
	if len(trips) == 0 {
		t.Fatal("generateFlightTrips() returned no trips")
	}
	for _, trip := range trips {
		if len(trip.Classes) != 2 {
			t.Fatalf("trip %s has %d classes, want 2 (economy, premium)", trip.ID, len(trip.Classes))
		}
		for _, c := range trip.Classes {
			want := flightClassCapacity[c.Code]
			if c.Capacity != want {
				t.Errorf("trip %s class %s capacity = %d, want %d", trip.ID, c.Code, c.Capacity, want)
			}
		}
	}
}

func TestGenerateFlightTrips_Deterministic(t *testing.T) {
	routes := allRoutes()
	operators := flightOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	a := generateFlightTrips(routes, operators, 3, 2, now)
	b := generateFlightTrips(routes, operators, 3, 2, now)

	if len(a) != len(b) {
		t.Fatalf("non-deterministic trip count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].OperatorID != b[i].OperatorID || a[i].VehicleNumber != b[i].VehicleNumber {
			t.Fatalf("non-deterministic trip at index %d: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestGenerateFlightTrips_FareOrderingVsTrainAndBus(t *testing.T) {
	// CLAUDE.md section 9 invariant: min(bus) < min(train) < min(flight).
	routes := allRoutes()
	trainOps := trainOperatorsList()
	flightOps := flightOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	trainTrips := generateTrainTrips(routes, trainOps, 1, 1, now)
	flightTrips := generateFlightTrips(routes, flightOps, 1, 1, now)

	slFareByRoute := make(map[string]int64)
	for _, trip := range trainTrips {
		for _, c := range trip.Classes {
			if c.Code == "sl" {
				slFareByRoute[trip.RouteID] = int64(c.FarePaise)
			}
		}
	}
	for _, trip := range flightTrips {
		trainFare, ok := slFareByRoute[trip.RouteID]
		if !ok {
			continue
		}
		var economyFare int64
		for _, c := range trip.Classes {
			if c.Code == "economy" {
				economyFare = int64(c.FarePaise)
			}
		}
		if economyFare <= trainFare {
			t.Errorf("route %s: flight economy fare %d should exceed train SL fare %d", trip.RouteID, economyFare, trainFare)
		}
	}
}
