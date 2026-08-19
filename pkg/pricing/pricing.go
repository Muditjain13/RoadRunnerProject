// Package pricing implements the deterministic fare formula from
// CLAUDE.md section 9: base = distance_km * 120 paise, times a mode
// multiplier, times a class multiplier, plus deterministic jitter.
package pricing

import (
	"fmt"
	"hash/fnv"
	"time"

	"github.com/Muditjain13/transport-app/pkg/money"
)

type Mode string

const (
	ModeBus    Mode = "bus"
	ModeTrain  Mode = "train"
	ModeFlight Mode = "flight"
)

var modeMultiplier = map[Mode]float64{
	ModeBus:    1.0,
	ModeTrain:  1.6,
	ModeFlight: 3.2,
}

// classMultiplier is keyed by (mode, class code). The class codes here are
// the canonical ones used across seed data and catalog documents.
var classMultiplier = map[Mode]map[string]float64{
	ModeBus: {
		"seater":     1.0,
		"sleeper":    1.35,
		"ac_sleeper": 1.6,
	},
	ModeTrain: {
		"sl": 1.0,
		"3a": 1.9,
		"2a": 2.7,
	},
	ModeFlight: {
		"economy": 1.0,
		"premium": 1.8,
	},
}

const maxJitter = 0.08

// BaseFarePaise is distance-only, before mode/class multipliers: ₹1.20/km.
func BaseFarePaise(distanceKm int) money.Paise {
	return money.Paise(distanceKm * 120)
}

// Jitter returns a deterministic value in [-maxJitter, maxJitter], seeded
// from (route, mode, class, departure). Same inputs always produce the
// same jitter, which is required for `make seed` to be idempotent and for
// tests to assert exact fares.
func Jitter(routeID string, mode Mode, class string, departure time.Time) float64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "%s|%s|%s|%d", routeID, mode, class, departure.Unix())
	sum := h.Sum64()
	// Map the top 32 bits of the hash to [-maxJitter, maxJitter].
	frac := float64(uint32(sum>>32)) / float64(^uint32(0)) // [0, 1]
	return (frac*2 - 1) * maxJitter
}

// Fare computes the jittered fare for one class on one trip.
//
// The cross-mode ordering invariant (min bus < min train < min flight,
// asserted in the seeder's tests) holds by construction and needs no
// clamping here: the cheapest class of every mode has class multiplier
// 1.0, so the gap between modes' cheapest fares is exactly the mode
// multiplier gap (1.0 -> 1.6 -> 3.2), which is far larger than the +/-8%
// jitter band. Verify the arithmetic before touching these multipliers.
func Fare(distanceKm int, mode Mode, class string, routeID string, departure time.Time) (money.Paise, error) {
	mm, ok := modeMultiplier[mode]
	if !ok {
		return 0, fmt.Errorf("pricing: unknown mode %q", mode)
	}
	cm, ok := classMultiplier[mode][class]
	if !ok {
		return 0, fmt.Errorf("pricing: unknown class %q for mode %q", class, mode)
	}
	base := float64(BaseFarePaise(distanceKm))
	j := Jitter(routeID, mode, class, departure)
	fare := base * mm * cm * (1 + j)
	return money.Paise(fare + 0.5), nil // round half up
}
