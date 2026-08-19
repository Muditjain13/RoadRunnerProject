// Package handler is catalog's transport layer: gRPC in, domain types out.
// No business logic lives here, only translation and status-code mapping.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	catalogv1 "github.com/Muditjain13/transport-app/api/proto/catalog/v1"
	"github.com/Muditjain13/transport-app/internal/catalog/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

type GRPCHandler struct {
	catalogv1.UnimplementedCatalogServiceServer
	svc *service.Service
}

func NewGRPCHandler(svc *service.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

func (h *GRPCHandler) SearchTrips(ctx context.Context, req *catalogv1.SearchTripsRequest) (*catalogv1.SearchTripsResponse, error) {
	if req.GetOriginCode() == "" || req.GetDestCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "origin_code and dest_code are required")
	}
	if req.GetDate() == nil {
		return nil, status.Error(codes.InvalidArgument, "date is required")
	}

	params := service.SearchParams{
		OriginCode: req.GetOriginCode(),
		DestCode:   req.GetDestCode(),
		Date:       req.GetDate().AsTime(),
		Mode:       modeFromProto(req.GetMode()),
	}

	trips, err := h.svc.SearchTrips(ctx, params)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search trips: %v", err)
	}

	resp := &catalogv1.SearchTripsResponse{Trips: make([]*catalogv1.Trip, 0, len(trips))}
	for _, t := range trips {
		resp.Trips = append(resp.Trips, tripToProto(t))
	}
	return resp, nil
}

func (h *GRPCHandler) GetTrip(ctx context.Context, req *catalogv1.GetTripRequest) (*catalogv1.GetTripResponse, error) {
	if req.GetTripId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trip_id is required")
	}

	trip, err := h.svc.GetTrip(ctx, req.GetTripId())
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "trip %s not found", req.GetTripId())
		}
		return nil, status.Errorf(codes.Internal, "get trip: %v", err)
	}
	return &catalogv1.GetTripResponse{Trip: tripToProto(trip)}, nil
}

func modeFromProto(m catalogv1.Mode) service.Mode {
	switch m {
	case catalogv1.Mode_MODE_TRAIN:
		return service.ModeTrain
	case catalogv1.Mode_MODE_FLIGHT:
		return service.ModeFlight
	default:
		return service.ModeBus
	}
}

func modeToProto(m service.Mode) catalogv1.Mode {
	switch m {
	case service.ModeBus:
		return catalogv1.Mode_MODE_BUS
	case service.ModeTrain:
		return catalogv1.Mode_MODE_TRAIN
	case service.ModeFlight:
		return catalogv1.Mode_MODE_FLIGHT
	default:
		return catalogv1.Mode_MODE_UNSPECIFIED
	}
}

func tripToProto(t service.Trip) *catalogv1.Trip {
	classes := make([]*catalogv1.TripClass, 0, len(t.Classes))
	for _, c := range t.Classes {
		classes = append(classes, &catalogv1.TripClass{
			Code:      c.Code,
			FarePaise: int64(c.FarePaise),
			Capacity:  int32(c.Capacity),
		})
	}
	return &catalogv1.Trip{
		Id:            t.ID,
		RouteId:       t.RouteID,
		OperatorId:    t.OperatorID,
		OperatorName:  t.OperatorName,
		Mode:          modeToProto(t.Mode),
		VehicleNumber: t.VehicleNumber,
		DepartureAt:   timestamppb.New(t.DepartureAt),
		ArrivalAt:     timestamppb.New(t.ArrivalAt),
		Classes:       classes,
	}
}
