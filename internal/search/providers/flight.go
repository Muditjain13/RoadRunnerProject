package providers

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/Muditjain13/transport-app/api/proto/catalog/v1"
	"github.com/Muditjain13/transport-app/internal/search/service"
)

type FlightProvider struct {
	client catalogv1.CatalogServiceClient
}

func NewFlightProvider(client catalogv1.CatalogServiceClient) *FlightProvider {
	return &FlightProvider{client: client}
}

func (p *FlightProvider) Mode() service.Mode { return service.ModeFlight }

func (p *FlightProvider) Search(ctx context.Context, params service.SearchParams) ([]service.Trip, error) {
	resp, err := p.client.SearchTrips(ctx, &catalogv1.SearchTripsRequest{
		OriginCode: params.OriginCode,
		DestCode:   params.DestCode,
		Date:       timestamppb.New(params.Date),
		Mode:       catalogv1.Mode_MODE_FLIGHT,
	})
	if err != nil {
		return nil, fmt.Errorf("flight provider: %w", err)
	}

	trips := make([]service.Trip, 0, len(resp.GetTrips()))
	for _, t := range resp.GetTrips() {
		trips = append(trips, tripFromProto(t, service.ModeFlight))
	}
	return trips, nil
}
