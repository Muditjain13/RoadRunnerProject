package main

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/Muditjain13/transport-app/internal/catalog/service"
	"github.com/Muditjain13/transport-app/pkg/pricing"
)

// Unlike bus (one class per trip) or train (three classes split across a
// variable coach count), a flight's cabin layout is fixed - CLAUDE.md
// section 9: "flight 180 economy plus 12 premium."
var flightClassCapacity = map[string]int{"economy": 180, "premium": 12}

var flightClassCodes = []string{"economy", "premium"}

var flightAirlineNames = []string{
	"IndiGo Air", "Air Bharat", "SkyIndia Express", "Vistara Wings",
	"GoFirst Airways", "SpiceRoute Air", "AirAsia Bharat", "Akasa Skies",
}

// flightCodes are two-letter IATA-style airline codes, one per operator
// above (same order), used to build vehicle numbers like "6B 204".
var flightCodes = []string{"6B", "AB", "SX", "VW", "GF", "SR", "AK", "AS"}

func flightOperatorsList() []service.Operator {
	ops := make([]service.Operator, 0, len(flightAirlineNames))
	for i, name := range flightAirlineNames {
		ops = append(ops, service.Operator{
			ID:   fmt.Sprintf("flight-op-%02d", i+1),
			Name: name,
			Mode: service.ModeFlight,
		})
	}
	return ops
}

// flightJourneyDuration implements CLAUDE.md section 9: distance / 700 km/h + 90 min of ground time.
func flightJourneyDuration(distanceKm int) time.Duration {
	hours := float64(distanceKm) / 700.0
	return time.Duration(hours*float64(time.Hour)) + 90*time.Minute
}

// generateFlightTrips deterministically produces flightsPerDay departures
// per route per day. Unlike bus/train, flight has no distance cap and no
// city exclusions - CLAUDE.md section 9: "Flights on all 45 pairs...
// flight is always available, so search never returns fully empty."
func generateFlightTrips(routes []service.Route, operators []service.Operator, daysForward, flightsPerDay int, now time.Time) []service.Trip {
	// Independent RNG stream so flight generation never perturbs bus's or
	// train's deterministic sequence.
	rng := rand.New(rand.NewPCG(seedRNGSeed, seedRNGSeed+3))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var trips []service.Trip
	for _, route := range routes {
		duration := flightJourneyDuration(route.DistanceKm)

		for day := 0; day < daysForward; day++ {
			dayStart := today.AddDate(0, 0, day)
			for slot := 0; slot < flightsPerDay; slot++ {
				offset := time.Duration(slot) * (24 * time.Hour / time.Duration(flightsPerDay))
				departureAt := dayStart.Add(6 * time.Hour).Add(offset)

				opIdx := rng.IntN(len(operators))
				op := operators[opIdx]

				classes := make([]service.TripClass, 0, len(flightClassCodes))
				for _, code := range flightClassCodes {
					fare, err := pricing.Fare(route.DistanceKm, pricing.ModeFlight, code, route.ID, departureAt)
					if err != nil {
						panic(fmt.Errorf("generateFlightTrips: %w", err))
					}
					classes = append(classes, service.TripClass{
						Code:      code,
						FarePaise: fare,
						Capacity:  flightClassCapacity[code],
					})
				}

				tripID := fmt.Sprintf("flight-%s-%s", route.ID, departureAt.Format("20060102T1504"))
				trips = append(trips, service.Trip{
					ID:            tripID,
					RouteID:       route.ID,
					OperatorID:    op.ID,
					OperatorName:  op.Name,
					Mode:          service.ModeFlight,
					VehicleNumber: fmt.Sprintf("%s %d", flightCodes[opIdx], 100+rng.IntN(900)), // e.g. "6B 204"
					DepartureAt:   departureAt,
					ArrivalAt:     departureAt.Add(duration),
					Classes:       classes,
				})
			}
		}
	}
	return trips
}
