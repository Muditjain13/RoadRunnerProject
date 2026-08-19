package repo

// Bulk upsert methods used only by cmd/seed. They live here, next to
// mongo.go's doc structs, so the seeder never has to duplicate catalog's
// BSON schema - upsert converts service domain types using the same
// *ToDoc mapping the read path already relies on being correct.

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/Muditjain13/transport-app/internal/catalog/service"
)

// seedBatchSize matches CLAUDE.md section 9: batch bulk loads at 1000.
const seedBatchSize = 1000

func (r *MongoRepo) UpsertOperators(ctx context.Context, ops []service.Operator) error {
	docs := make([]operatorDoc, 0, len(ops))
	for _, o := range ops {
		docs = append(docs, operatorDoc{ID: o.ID, Name: o.Name, Mode: string(o.Mode)})
	}
	return bulkUpsert(ctx, r.operators, docs, func(d operatorDoc) string { return d.ID })
}

func (r *MongoRepo) UpsertRoutes(ctx context.Context, routes []service.Route) error {
	docs := make([]routeDoc, 0, len(routes))
	for _, rt := range routes {
		docs = append(docs, routeDoc{
			ID:         rt.ID,
			OriginCode: rt.OriginCode,
			DestCode:   rt.DestCode,
			DistanceKm: rt.DistanceKm,
		})
	}
	return bulkUpsert(ctx, r.routes, docs, func(d routeDoc) string { return d.ID })
}

// UpsertTrips denormalizes origin/dest/distance onto each trip document
// (see model.go) using routesByID, keyed by service.Route.ID. Passing
// routes in-memory instead of looking each one up keeps this a pure bulk
// write with no interleaved reads.
func (r *MongoRepo) UpsertTrips(ctx context.Context, trips []service.Trip, routesByID map[string]service.Route) error {
	docs := make([]tripDoc, 0, len(trips))
	for _, t := range trips {
		if len(t.Classes) == 0 {
			return fmt.Errorf("upsert trip %s: trip has no classes", t.ID)
		}
		route, ok := routesByID[t.RouteID]
		if !ok {
			return fmt.Errorf("upsert trip %s: route %s not in routesByID", t.ID, t.RouteID)
		}
		classes := make([]tripClassDoc, 0, len(t.Classes))
		for _, c := range t.Classes {
			classes = append(classes, tripClassDoc{Code: c.Code, FarePaise: int64(c.FarePaise), Capacity: c.Capacity})
		}
		docs = append(docs, tripDoc{
			ID:            t.ID,
			RouteID:       t.RouteID,
			OperatorID:    t.OperatorID,
			OperatorName:  t.OperatorName,
			Mode:          string(t.Mode),
			OriginCode:    route.OriginCode,
			DestCode:      route.DestCode,
			DistanceKm:    route.DistanceKm,
			VehicleNumber: t.VehicleNumber,
			DepartureAt:   t.DepartureAt,
			ArrivalAt:     t.ArrivalAt,
			Classes:       classes,
		})
	}
	return bulkUpsert(ctx, r.trips, docs, func(d tripDoc) string { return d.ID })
}

// bulkUpsert replaces-or-inserts docs by _id in batches of seedBatchSize,
// making re-running the seeder a no-op on unchanged data (CLAUDE.md:
// "make seed is idempotent").
func bulkUpsert[T any](ctx context.Context, coll *mongo.Collection, docs []T, idOf func(T) string) error {
	for start := 0; start < len(docs); start += seedBatchSize {
		end := min(start+seedBatchSize, len(docs))
		batch := docs[start:end]

		models := make([]mongo.WriteModel, 0, len(batch))
		for _, d := range batch {
			models = append(models, mongo.NewReplaceOneModel().
				SetFilter(bson.D{{Key: "_id", Value: idOf(d)}}).
				SetReplacement(d).
				SetUpsert(true))
		}

		if _, err := coll.BulkWrite(ctx, models); err != nil {
			return fmt.Errorf("bulk upsert into %s: %w", coll.Name(), err)
		}
	}
	return nil
}
