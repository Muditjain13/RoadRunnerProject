package providers

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/Muditjain13/transport-app/api/proto/catalog/v1"
	"github.com/Muditjain13/transport-app/internal/search/service"
)

type TrainProvider struct {
	client catalogv1.CatalogServiceClient
}

func NewTrainProvider(client catalogv1.CatalogServiceClient) *TrainProvider {
	return &TrainProvider{client: client}
}

func (p *TrainProvider) Mode() service.Mode { return service.ModeTrain }

func (p *TrainProvider) Search(ctx context.Context, params service.SearchParams) ([]service.Trip, error) {
	resp, err := p.client.SearchTrips(ctx, &catalogv1.SearchTripsRequest{
		OriginCode: params.OriginCode,
		DestCode:   params.DestCode,
		Date:       timestamppb.New(params.Date),
		Mode:       catalogv1.Mode_MODE_TRAIN,
	})
	if err != nil {
		return nil, fmt.Errorf("train provider: %w", err)
	}

	trips := make([]service.Trip, 0, len(resp.GetTrips()))
	for _, t := range resp.GetTrips() {
		trips = append(trips, tripFromProto(t, service.ModeTrain))
	}
	return trips, nil
}
