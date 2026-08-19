package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeRepo struct {
	trips        []Trip
	findTripsErr error
}

func (f *fakeRepo) FindTrips(ctx context.Context, params SearchParams) ([]Trip, error) {
	if f.findTripsErr != nil {
		return nil, f.findTripsErr
	}
	return f.trips, nil
}

func (f *fakeRepo) GetTrip(ctx context.Context, tripID string) (Trip, error) {
	for _, t := range f.trips {
		if t.ID == tripID {
			return t, nil
		}
	}
	return Trip{}, errors.New("not found")
}

func (f *fakeRepo) GetRoute(ctx context.Context, routeID string) (Route, error) {
	return Route{}, errors.New("not implemented")
}

func (f *fakeRepo) GetOperator(ctx context.Context, operatorID string) (Operator, error) {
	return Operator{}, errors.New("not implemented")
}

func TestService_SearchTrips(t *testing.T) {
	now := time.Date(2026, 3, 1, 6, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		repo    *fakeRepo
		want    int
		wantErr bool
	}{
		{
			name: "returns trips from repo",
			repo: &fakeRepo{trips: []Trip{{ID: "bus-DEL-BOM-1"}, {ID: "bus-DEL-BOM-2"}}},
			want: 2,
		},
		{
			name: "empty result is not an error",
			repo: &fakeRepo{trips: nil},
			want: 0,
		},
		{
			name:    "propagates repo error",
			repo:    &fakeRepo{findTripsErr: errors.New("mongo down")},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.repo)
			got, err := svc.SearchTrips(context.Background(), SearchParams{
				OriginCode: "DEL", DestCode: "BOM", Date: now, Mode: ModeBus,
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("SearchTrips() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && len(got) != tt.want {
				t.Errorf("SearchTrips() returned %d trips, want %d", len(got), tt.want)
			}
		})
	}
}
