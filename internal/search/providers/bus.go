// Package providers wraps each mode's upstream trip source as a
// service.Provider. Only a bus provider exists in Phase 1; train and
// flight providers land in their own phases and register the same way.
package providers

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/Muditjain13/transport-app/api/proto/catalog/v1"
	"github.com/Muditjain13/transport-app/internal/search/service"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type BusProvider struct {
	client catalogv1.CatalogServiceClient
}

func NewBusProvider(client catalogv1.CatalogServiceClient) *BusProvider {
	return &BusProvider{client: client}
}

func (p *BusProvider) Mode() service.Mode { return service.ModeBus }

func (p *BusProvider) Search(ctx context.Context, params service.SearchParams) ([]service.Trip, error) {
	resp, err := p.client.SearchTrips(ctx, &catalogv1.SearchTripsRequest{
		OriginCode: params.OriginCode,
		DestCode:   params.DestCode,
		Date:       timestamppb.New(params.Date),
		Mode:       catalogv1.Mode_MODE_BUS,
	})
	if err != nil {
		return nil, fmt.Errorf("bus provider: %w", err)
	}

	trips := make([]service.Trip, 0, len(resp.GetTrips()))
	for _, t := range resp.GetTrips() {
		trips = append(trips, tripFromProto(t, service.ModeBus))
	}
	return trips, nil
}

// tripFromProto takes mode explicitly rather than reading t.GetMode(),
// since each Provider already knows its own mode and this keeps the
// mapping a pure, provider-agnostic helper shared by bus and train.
func tripFromProto(t *catalogv1.Trip, mode service.Mode) service.Trip {
	classes := make([]service.TripClass, 0, len(t.GetClasses()))
	for _, c := range t.GetClasses() {
		classes = append(classes, service.TripClass{
			Code:      c.GetCode(),
			FarePaise: money.Paise(c.GetFarePaise()),
			Capacity:  int(c.GetCapacity()),
		})
	}
	return service.Trip{
		ID:            t.GetId(),
		RouteID:       t.GetRouteId(),
		OperatorID:    t.GetOperatorId(),
		OperatorName:  t.GetOperatorName(),
		Mode:          mode,
		VehicleNumber: t.GetVehicleNumber(),
		DepartureAt:   t.GetDepartureAt().AsTime(),
		ArrivalAt:     t.GetArrivalAt().AsTime(),
		Classes:       classes,
	}
}
