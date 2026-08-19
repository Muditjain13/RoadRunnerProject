package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Muditjain13/transport-app/internal/catalog/repo"
	"github.com/Muditjain13/transport-app/internal/catalog/service"
	"github.com/Muditjain13/transport-app/pkg/geo"
	"github.com/Muditjain13/transport-app/pkg/pricing"
)

// seedCatalog upserts bus operators, all 90 directed routes, and bus trips
// for the given profile. Idempotent: re-running with the same profile
// replaces documents in place rather than duplicating them (see
// repo.bulkUpsert).
func seedCatalog(ctx context.Context, logger *slog.Logger, catalogRepo *repo.MongoRepo, p profile) ([]service.Trip, error) {
	operators := busOperators()
	if err := catalogRepo.UpsertOperators(ctx, operators); err != nil {
		return nil, fmt.Errorf("seed operators: %w", err)
	}
	logger.Info("seeded operators", "count", len(operators))

	routes := allRoutes()
	if err := catalogRepo.UpsertRoutes(ctx, routes); err != nil {
		return nil, fmt.Errorf("seed routes: %w", err)
	}
	logger.Info("seeded routes", "count", len(routes))

	routesByID := make(map[string]service.Route, len(routes))
	for _, r := range routes {
		routesByID[r.ID] = r
	}

	busTrips := generateBusTrips(routes, operators, p.daysForward, p.tripsPerDay, time.Now().UTC())
	if err := catalogRepo.UpsertTrips(ctx, busTrips, routesByID); err != nil {
		return nil, fmt.Errorf("seed bus trips: %w", err)
	}
	logger.Info("seeded bus trips", "count", len(busTrips), "days_forward", p.daysForward, "trips_per_day", p.tripsPerDay)

	trainOperators := trainOperatorsList()
	if err := catalogRepo.UpsertOperators(ctx, trainOperators); err != nil {
		return nil, fmt.Errorf("seed train operators: %w", err)
	}
	logger.Info("seeded train operators", "count", len(trainOperators))

	trainTrips := generateTrainTrips(routes, trainOperators, p.daysForward, p.tripsPerDay, time.Now().UTC())
	if err := catalogRepo.UpsertTrips(ctx, trainTrips, routesByID); err != nil {
		return nil, fmt.Errorf("seed train trips: %w", err)
	}
	logger.Info("seeded train trips", "count", len(trainTrips), "days_forward", p.daysForward, "trips_per_day", p.tripsPerDay)

	flightOperators := flightOperatorsList()
	if err := catalogRepo.UpsertOperators(ctx, flightOperators); err != nil {
		return nil, fmt.Errorf("seed flight operators: %w", err)
	}
	logger.Info("seeded flight operators", "count", len(flightOperators))

	flightTrips := generateFlightTrips(routes, flightOperators, p.daysForward, p.tripsPerDay, time.Now().UTC())
	if err := catalogRepo.UpsertTrips(ctx, flightTrips, routesByID); err != nil {
		return nil, fmt.Errorf("seed flight trips: %w", err)
	}
	logger.Info("seeded flight trips", "count", len(flightTrips), "days_forward", p.daysForward, "trips_per_day", p.tripsPerDay)

	trips := append(busTrips, trainTrips...)
	trips = append(trips, flightTrips...)
	return trips, nil
}

// maxBusDistanceKm mirrors CLAUDE.md section 9's mode-availability rule:
// "Bus only where distance <= 1500 km". This alone also produces the
// "GAU bus only to nearby pairs" behavior for free - every city is more
// than 1500km from Guwahati in the fixed distance matrix, so GAU simply
// never gets a bus trip. No special-cased GAU logic needed.
const maxBusDistanceKm = 1500

// vehicleClass is the single class a bus trip offers - CLAUDE.md section 9
// specs a bus as "36 seater or 30 sleeper", i.e. one vehicle type per
// trip, not a seater/sleeper mix on the same bus.
type vehicleClass struct {
	code     string
	capacity int
}

var busVehicleClasses = []vehicleClass{
	{code: "seater", capacity: 36},
	{code: "sleeper", capacity: 30},
	{code: "ac_sleeper", capacity: 30},
}

var vehicleStateCodes = []string{"DL", "MH", "TN", "KA", "RJ", "AS", "JK", "GJ", "MP", "PB"}

// generateVehicleNumber produces an Indian-style registration plate
// (e.g. "KA 05 AB 1234") instead of an internal-ID mashup.
func generateVehicleNumber(rng *rand.Rand) string {
	state := vehicleStateCodes[rng.IntN(len(vehicleStateCodes))]
	rto := rng.IntN(60) + 1
	l1 := byte('A' + rng.IntN(26))
	l2 := byte('A' + rng.IntN(26))
	num := rng.IntN(9999) + 1
	return fmt.Sprintf("%s %02d %c%c %04d", state, rto, l1, l2, num)
}

var busOperatorNames = []string{
	"Himalayan Express", "Deccan Cruiser", "Coastal Connect", "Northern Star Travels",
	"Golden Route Bus Co", "Rajdhani Roadways", "Southern Cross Travels", "Metro Link Bus",
	"Sunrise Voyager", "Highway Kings", "Pearl City Travels", "Silverline Roadways",
	"Green Valley Express", "Royal Comfort Coaches", "Blue Ridge Travels", "Central India Movers",
	"Eastern Gateway Bus", "Western Express Lines", "Prime Route Travels", "National Highway Coaches",
}

// seedRNGSeed is fixed so `make seed` is deterministic run to run, per
// CLAUDE.md section 9.
const seedRNGSeed = 1337

func busOperators() []service.Operator {
	ops := make([]service.Operator, 0, len(busOperatorNames))
	for i, name := range busOperatorNames {
		ops = append(ops, service.Operator{
			ID:   fmt.Sprintf("bus-op-%02d", i+1),
			Name: name,
			Mode: service.ModeBus,
		})
	}
	return ops
}

func allRoutes() []service.Route {
	pairs := geo.AllCityPairs()
	routes := make([]service.Route, 0, len(pairs))
	for _, p := range pairs {
		origin, dest := p[0], p[1]
		dist, err := geo.DistanceKm(origin, dest)
		if err != nil {
			// AllCityPairs only ever emits codes from geo.Cities, so this
			// can't happen - a panic here means the geo package itself is
			// broken, not bad input.
			panic(fmt.Errorf("allRoutes: %w", err))
		}
		routes = append(routes, service.Route{
			ID:         fmt.Sprintf("%s-%s", origin, dest),
			OriginCode: origin,
			DestCode:   dest,
			DistanceKm: dist,
		})
	}
	return routes
}

// busJourneyDuration implements CLAUDE.md section 9: distance / 45 km/h + 45 min of stops.
func busJourneyDuration(distanceKm int) time.Duration {
	hours := float64(distanceKm) / 45.0
	return time.Duration(hours*float64(time.Hour)) + 45*time.Minute
}

// generateBusTrips deterministically produces `tripsPerDay` bus departures
// per bus-eligible route per day, for `daysForward` days starting today
// (UTC). Iteration order is fixed (routes in geo.AllCityPairs order, then
// day offset, then slot index) so the same rng, consumed in the same
// sequence, always assigns the same operator and vehicle class to the same
// trip - required for idempotent, reproducible seeding.
func generateBusTrips(routes []service.Route, operators []service.Operator, daysForward, tripsPerDay int, now time.Time) []service.Trip {
	rng := rand.New(rand.NewPCG(seedRNGSeed, seedRNGSeed))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var trips []service.Trip
	for _, route := range routes {
		if route.DistanceKm > maxBusDistanceKm {
			continue
		}
		duration := busJourneyDuration(route.DistanceKm)

		for day := 0; day < daysForward; day++ {
			dayStart := today.AddDate(0, 0, day)
			for slot := 0; slot < tripsPerDay; slot++ {
				// Spread departures across the day, e.g. 2 slots -> 06:00, 18:00.
				offset := time.Duration(slot) * (24 * time.Hour / time.Duration(tripsPerDay))
				departureAt := dayStart.Add(6 * time.Hour).Add(offset)

				op := operators[rng.IntN(len(operators))]
				vc := busVehicleClasses[rng.IntN(len(busVehicleClasses))]

				fare, err := pricing.Fare(route.DistanceKm, pricing.ModeBus, vc.code, route.ID, departureAt)
				if err != nil {
					panic(fmt.Errorf("generateBusTrips: %w", err))
				}

				tripID := fmt.Sprintf("bus-%s-%s", route.ID, departureAt.Format("20060102T1504"))
				trips = append(trips, service.Trip{
					ID:            tripID,
					RouteID:       route.ID,
					OperatorID:    op.ID,
					OperatorName:  op.Name,
					Mode:          service.ModeBus,
					VehicleNumber: generateVehicleNumber(rng),
					DepartureAt:   departureAt,
					ArrivalAt:     departureAt.Add(duration),
					Classes: []service.TripClass{
						{Code: vc.code, FarePaise: fare, Capacity: vc.capacity},
					},
				})
			}
		}
	}
	return trips
}
