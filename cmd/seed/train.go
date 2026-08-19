package main

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/Muditjain13/transport-app/internal/catalog/service"
	"github.com/Muditjain13/transport-app/pkg/geo"
	"github.com/Muditjain13/transport-app/pkg/pricing"
)

// Unlike bus (one vehicle type per trip), a real train runs SL/3A/2A
// coaches together - a train trip offers all three classes at once.
var trainClassCodes = []string{"sl", "3a", "2a"}

// coachShare is each class's rough share of a train's total coaches,
// matching typical Indian long-distance train composition (mostly
// sleeper, a meaningful AC allocation, fewer 2A coaches).
var coachShare = map[string]float64{"sl": 0.5, "3a": 0.3, "2a": 0.2}

const bertsPerCoach = 72 // CLAUDE.md section 9: "train 72 per coach"

var trainOperatorNames = []string{
	"Northern Railway", "Western Railway", "Southern Railway", "Central Railway",
	"Eastern Railway", "South Central Railway", "North Eastern Railway", "South Eastern Railway",
	"East Coast Railway", "West Central Railway", "North Central Railway", "South Western Railway",
}

func trainOperatorsList() []service.Operator {
	ops := make([]service.Operator, 0, len(trainOperatorNames))
	for i, name := range trainOperatorNames {
		ops = append(ops, service.Operator{
			ID:   fmt.Sprintf("train-op-%02d", i+1),
			Name: name,
			Mode: service.ModeTrain,
		})
	}
	return ops
}

// trainJourneyDuration implements CLAUDE.md section 9: distance / 60 km/h + 30 min.
func trainJourneyDuration(distanceKm int) time.Duration {
	hours := float64(distanceKm) / 60.0
	return time.Duration(hours*float64(time.Hour)) + 30*time.Minute
}

// generateTrainTrips deterministically produces trainsPerDay departures per
// route per day. CLAUDE.md section 9: "No train to or from SXR" - Srinagar
// is rail-isolated in this dataset, so routes touching it are skipped
// entirely (the empty-mode path this rule exists to exercise).
func generateTrainTrips(routes []service.Route, operators []service.Operator, daysForward, tripsPerDay int, now time.Time) []service.Trip {
	// Independent RNG stream (own seed) so train generation never
	// perturbs bus's deterministic sequence if either changes later.
	rng := rand.New(rand.NewPCG(seedRNGSeed, seedRNGSeed+1))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	var trips []service.Trip
	for _, route := range routes {
		if route.OriginCode == geo.SXR || route.DestCode == geo.SXR {
			continue
		}
		duration := trainJourneyDuration(route.DistanceKm)

		for day := 0; day < daysForward; day++ {
			dayStart := today.AddDate(0, 0, day)
			for slot := 0; slot < tripsPerDay; slot++ {
				offset := time.Duration(slot) * (24 * time.Hour / time.Duration(tripsPerDay))
				departureAt := dayStart.Add(5 * time.Hour).Add(offset)

				op := operators[rng.IntN(len(operators))]
				totalCoaches := rng.IntN(11) + 8 // 8..18

				classes := make([]service.TripClass, 0, len(trainClassCodes))
				for _, code := range trainClassCodes {
					coaches := max(1, int(float64(totalCoaches)*coachShare[code]))
					fare, err := pricing.Fare(route.DistanceKm, pricing.ModeTrain, code, route.ID, departureAt)
					if err != nil {
						panic(fmt.Errorf("generateTrainTrips: %w", err))
					}
					classes = append(classes, service.TripClass{
						Code:      code,
						FarePaise: fare,
						Capacity:  coaches * bertsPerCoach,
					})
				}

				tripID := fmt.Sprintf("train-%s-%s", route.ID, departureAt.Format("20060102T1504"))
				trips = append(trips, service.Trip{
					ID:            tripID,
					RouteID:       route.ID,
					OperatorID:    op.ID,
					OperatorName:  op.Name,
					Mode:          service.ModeTrain,
					VehicleNumber: fmt.Sprintf("%d%03d", 10+rng.IntN(90), rng.IntN(999)), // Indian train number style, e.g. "12951"
					DepartureAt:   departureAt,
					ArrivalAt:     departureAt.Add(duration),
					Classes:       classes,
				})
			}
		}
	}
	return trips
}
