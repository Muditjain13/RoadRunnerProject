package main

import (
	"testing"
	"time"

	"github.com/Muditjain13/transport-app/internal/catalog/service"
	"github.com/Muditjain13/transport-app/pkg/geo"
)

func TestGenerateTrainTrips_NoTripsToOrFromSXR(t *testing.T) {
	routes := allRoutes()
	operators := trainOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	trips := generateTrainTrips(routes, operators, 3, 2, now)

	routeByID := make(map[string]service.Route, len(routes))
	for _, r := range routes {
		routeByID[r.ID] = r
	}

	for _, trip := range trips {
		r := routeByID[trip.RouteID]
		if r.OriginCode == geo.SXR || r.DestCode == geo.SXR {
			t.Errorf("expected no train trips touching SXR, got trip %s on route %s", trip.ID, trip.RouteID)
		}
	}
}

func TestGenerateTrainTrips_OffersAllThreeClasses(t *testing.T) {
	routes := allRoutes()
	operators := trainOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	trips := generateTrainTrips(routes, operators, 1, 1, now)
	if len(trips) == 0 {
		t.Fatal("generateTrainTrips() returned no trips")
	}
	for _, trip := range trips {
		if len(trip.Classes) != 3 {
			t.Fatalf("trip %s has %d classes, want 3 (sl, 3a, 2a)", trip.ID, len(trip.Classes))
		}
		seen := make(map[string]bool)
		for _, c := range trip.Classes {
			seen[c.Code] = true
			if c.Capacity < bertsPerCoach {
				t.Errorf("trip %s class %s capacity = %d, want at least one coach (%d)", trip.ID, c.Code, c.Capacity, bertsPerCoach)
			}
		}
		for _, code := range trainClassCodes {
			if !seen[code] {
				t.Errorf("trip %s missing class %s", trip.ID, code)
			}
		}
	}
}

func TestGenerateTrainTrips_Deterministic(t *testing.T) {
	routes := allRoutes()
	operators := trainOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	a := generateTrainTrips(routes, operators, 5, 3, now)
	b := generateTrainTrips(routes, operators, 5, 3, now)

	if len(a) != len(b) {
		t.Fatalf("non-deterministic trip count: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].OperatorID != b[i].OperatorID {
			t.Fatalf("non-deterministic trip at index %d: %+v vs %+v", i, a[i], b[i])
		}
		for j := range a[i].Classes {
			if a[i].Classes[j].Capacity != b[i].Classes[j].Capacity || a[i].Classes[j].FarePaise != b[i].Classes[j].FarePaise {
				t.Fatalf("non-deterministic class at trip %d class %d", i, j)
			}
		}
	}
}

func TestGenerateTrainTrips_FareOrderingVsBus(t *testing.T) {
	// Sanity check on the CLAUDE.md section 9 / pkg/pricing invariant:
	// the cheapest class of every mode carries class multiplier 1.0, so
	// cheapest-train (sl) must exceed cheapest-bus (seater) fare for the
	// same route, jitter notwithstanding. Each bus trip only offers one
	// randomly chosen class, so this only checks routes/trips where that
	// class happened to be "seater".
	routes := allRoutes()
	busOps := busOperators()
	trainOps := trainOperatorsList()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	busTrips := generateBusTrips(routes, busOps, 1, 1, now)
	trainTrips := generateTrainTrips(routes, trainOps, 1, 1, now)

	seaterFareByRoute := make(map[string]int64)
	for _, trip := range busTrips {
		if trip.Classes[0].Code == "seater" {
			seaterFareByRoute[trip.RouteID] = int64(trip.Classes[0].FarePaise)
		}
	}
	checked := 0
	for _, trip := range trainTrips {
		busFare, ok := seaterFareByRoute[trip.RouteID]
		if !ok {
			continue // no bus trip on this route got the seater class in this sample
		}
		var slFare int64
		for _, c := range trip.Classes {
			if c.Code == "sl" {
				slFare = int64(c.FarePaise)
			}
		}
		checked++
		if slFare <= busFare {
			t.Errorf("route %s: train SL fare %d should exceed bus seater fare %d", trip.RouteID, slFare, busFare)
		}
	}
	if checked == 0 {
		t.Skip("no route had a sampled bus seater trip to compare against")
	}
}
