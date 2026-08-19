package pricing

import (
	"testing"
	"time"

	"github.com/Muditjain13/transport-app/pkg/geo"
)

func TestFare_KnownValues(t *testing.T) {
	dep := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		distanceKm int
		mode       Mode
		class      string
	}{
		{"bus seater short route", 280, ModeBus, "seater"},
		{"bus ac_sleeper long route", 1400, ModeBus, "ac_sleeper"},
		{"train 2a", 1400, ModeTrain, "2a"},
		{"flight premium", 1400, ModeFlight, "premium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fare, err := Fare(tt.distanceKm, tt.mode, tt.class, "DEL-BOM", dep)
			if err != nil {
				t.Fatalf("Fare() error = %v", err)
			}

			base := float64(BaseFarePaise(tt.distanceKm))
			mm := modeMultiplier[tt.mode]
			cm := classMultiplier[tt.mode][tt.class]
			j := Jitter("DEL-BOM", tt.mode, tt.class, dep)
			want := base * mm * cm * (1 + j)

			gotf := float64(fare)
			if diff := gotf - want; diff > 1 || diff < -1 {
				t.Errorf("Fare() = %v, want ~%v (diff %v)", fare, want, diff)
			}
			if j < -maxJitter || j > maxJitter {
				t.Errorf("Jitter() = %v, outside [-%v, %v]", j, maxJitter, maxJitter)
			}
		})
	}
}

func TestFare_UnknownModeOrClass(t *testing.T) {
	dep := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)

	if _, err := Fare(100, "rickshaw", "seater", "DEL-BOM", dep); err == nil {
		t.Error("Fare() with unknown mode: want error, got nil")
	}
	if _, err := Fare(100, ModeBus, "first_class", "DEL-BOM", dep); err == nil {
		t.Error("Fare() with unknown class: want error, got nil")
	}
}

func TestFare_Deterministic(t *testing.T) {
	dep := time.Date(2026, 3, 15, 9, 30, 0, 0, time.UTC)
	a, err := Fare(940, ModeTrain, "3a", "AMD-JAI", dep)
	if err != nil {
		t.Fatalf("Fare() error = %v", err)
	}
	b, err := Fare(940, ModeTrain, "3a", "AMD-JAI", dep)
	if err != nil {
		t.Fatalf("Fare() error = %v", err)
	}
	if a != b {
		t.Errorf("Fare() not deterministic: %v != %v for identical inputs", a, b)
	}
}

// TestFareOrderingInvariant is the CLAUDE.md section 9 invariant: for any
// route, the cheapest bus fare undercuts the cheapest train fare, which
// undercuts the cheapest flight fare - and that must hold even after the
// +/-8% deterministic jitter. Checked across every one of the 90 directed
// routes and a spread of departure times, so a future multiplier change
// that breaks it fails here instead of silently producing bad search
// ordering.
func TestFareOrderingInvariant(t *testing.T) {
	departures := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 0, 0, time.UTC),
	}

	cheapestBusClass := "seater"
	cheapestTrainClass := "sl"
	cheapestFlightClass := "economy"

	for _, pair := range geo.AllCityPairs() {
		origin, dest := pair[0], pair[1]
		routeID := origin + "-" + dest
		dist, err := geo.DistanceKm(origin, dest)
		if err != nil {
			t.Fatalf("DistanceKm(%s, %s) error = %v", origin, dest, err)
		}
		if dist == 0 {
			continue
		}

		for _, dep := range departures {
			busFare, err := Fare(dist, ModeBus, cheapestBusClass, routeID, dep)
			if err != nil {
				t.Fatalf("Fare(bus) error = %v", err)
			}
			trainFare, err := Fare(dist, ModeTrain, cheapestTrainClass, routeID, dep)
			if err != nil {
				t.Fatalf("Fare(train) error = %v", err)
			}
			flightFare, err := Fare(dist, ModeFlight, cheapestFlightClass, routeID, dep)
			if err != nil {
				t.Fatalf("Fare(flight) error = %v", err)
			}

			if !(busFare < trainFare && trainFare < flightFare) {
				t.Errorf("%s @ %s: ordering invariant violated: bus=%v train=%v flight=%v",
					routeID, dep, busFare, trainFare, flightFare)
			}
		}
	}
}
