// Package repo is catalog's MongoDB implementation of service.Repository.
package repo

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/Muditjain13/transport-app/internal/catalog/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type MongoRepo struct {
	operators *mongo.Collection
	routes    *mongo.Collection
	trips     *mongo.Collection
}

func NewMongoRepo(db *mongo.Database) *MongoRepo {
	return &MongoRepo{
		operators: db.Collection("operators"),
		routes:    db.Collection("routes"),
		trips:     db.Collection("trips"),
	}
}

// EnsureIndexes creates the indexes catalog's query patterns need. Called
// once at service startup, not per-request.
func (r *MongoRepo) EnsureIndexes(ctx context.Context) error {
	_, err := r.trips.Indexes().CreateOne(ctx, mongo.IndexModel{
		// Search filters by origin+dest+mode and range-queries departure_at;
		// this compound index serves that query without a collection scan.
		Keys: bson.D{
			{Key: "origin_code", Value: 1},
			{Key: "dest_code", Value: 1},
			{Key: "mode", Value: 1},
			{Key: "departure_at", Value: 1},
		},
		Options: options.Index().SetName("trip_search"),
	})
	if err != nil {
		return fmt.Errorf("ensure trip_search index: %w", err)
	}
	return nil
}

func (r *MongoRepo) FindTrips(ctx context.Context, params service.SearchParams) ([]service.Trip, error) {
	dayStart := time.Date(params.Date.Year(), params.Date.Month(), params.Date.Day(), 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour)

	filter := bson.D{
		{Key: "origin_code", Value: params.OriginCode},
		{Key: "dest_code", Value: params.DestCode},
		{Key: "mode", Value: string(params.Mode)},
		{Key: "departure_at", Value: bson.D{
			{Key: "$gte", Value: dayStart},
			{Key: "$lt", Value: dayEnd},
		}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "departure_at", Value: 1}})

	cur, err := r.trips.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find trips: %w", err)
	}
	defer cur.Close(ctx)

	var docs []tripDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("decode trips: %w", err)
	}

	trips := make([]service.Trip, 0, len(docs))
	for _, d := range docs {
		trips = append(trips, tripFromDoc(d))
	}
	return trips, nil
}

func (r *MongoRepo) GetTrip(ctx context.Context, tripID string) (service.Trip, error) {
	var d tripDoc
	err := r.trips.FindOne(ctx, bson.D{{Key: "_id", Value: tripID}}).Decode(&d)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return service.Trip{}, fmt.Errorf("get trip %s: %w", tripID, apperrors.ErrNotFound)
		}
		return service.Trip{}, fmt.Errorf("get trip %s: %w", tripID, err)
	}
	return tripFromDoc(d), nil
}

func (r *MongoRepo) GetRoute(ctx context.Context, routeID string) (service.Route, error) {
	var d routeDoc
	err := r.routes.FindOne(ctx, bson.D{{Key: "_id", Value: routeID}}).Decode(&d)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return service.Route{}, fmt.Errorf("get route %s: %w", routeID, apperrors.ErrNotFound)
		}
		return service.Route{}, fmt.Errorf("get route %s: %w", routeID, err)
	}
	return service.Route{
		ID:         d.ID,
		OriginCode: d.OriginCode,
		DestCode:   d.DestCode,
		DistanceKm: d.DistanceKm,
	}, nil
}

func (r *MongoRepo) GetOperator(ctx context.Context, operatorID string) (service.Operator, error) {
	var d operatorDoc
	err := r.operators.FindOne(ctx, bson.D{{Key: "_id", Value: operatorID}}).Decode(&d)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return service.Operator{}, fmt.Errorf("get operator %s: %w", operatorID, apperrors.ErrNotFound)
		}
		return service.Operator{}, fmt.Errorf("get operator %s: %w", operatorID, err)
	}
	return service.Operator{ID: d.ID, Name: d.Name, Mode: service.Mode(d.Mode)}, nil
}

func tripFromDoc(d tripDoc) service.Trip {
	classes := make([]service.TripClass, 0, len(d.Classes))
	for _, c := range d.Classes {
		classes = append(classes, service.TripClass{
			Code:      c.Code,
			FarePaise: money.Paise(c.FarePaise),
			Capacity:  c.Capacity,
		})
	}
	return service.Trip{
		ID:            d.ID,
		RouteID:       d.RouteID,
		OperatorID:    d.OperatorID,
		OperatorName:  d.OperatorName,
		Mode:          service.Mode(d.Mode),
		VehicleNumber: d.VehicleNumber,
		DepartureAt:   d.DepartureAt,
		ArrivalAt:     d.ArrivalAt,
		Classes:       classes,
	}
}
