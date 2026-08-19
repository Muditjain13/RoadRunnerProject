// Package handler is search's gRPC transport layer.
package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	searchv1 "github.com/Muditjain13/transport-app/api/proto/search/v1"
	"github.com/Muditjain13/transport-app/internal/search/service"
)

type GRPCHandler struct {
	searchv1.UnimplementedSearchServiceServer
	svc *service.Service
}

func NewGRPCHandler(svc *service.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

func (h *GRPCHandler) SearchTrips(ctx context.Context, req *searchv1.SearchTripsRequest) (*searchv1.SearchTripsResponse, error) {
	if req.GetOriginCode() == "" || req.GetDestCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "origin_code and dest_code are required")
	}
	if req.GetDate() == nil {
		return nil, status.Error(codes.InvalidArgument, "date is required")
	}

	modes := make([]service.Mode, 0, len(req.GetModes()))
	for _, m := range req.GetModes() {
		if mode := modeFromProto(m); mode != "" {
			modes = append(modes, mode)
		}
	}

	result, err := h.svc.Search(ctx, service.SearchParams{
		OriginCode: req.GetOriginCode(),
		DestCode:   req.GetDestCode(),
		Date:       req.GetDate().AsTime(),
		Modes:      modes,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "search: %v", err)
	}

	resp := &searchv1.SearchTripsResponse{
		Trips:             make([]*searchv1.Trip, 0, len(result.Trips)),
		DegradedProviders: make([]searchv1.Mode, 0, len(result.DegradedProviders)),
	}
	for _, t := range result.Trips {
		resp.Trips = append(resp.Trips, tripToProto(t))
	}
	for _, m := range result.DegradedProviders {
		resp.DegradedProviders = append(resp.DegradedProviders, modeToProto(m))
	}
	return resp, nil
}

func modeFromProto(m searchv1.Mode) service.Mode {
	switch m {
	case searchv1.Mode_MODE_BUS:
		return service.ModeBus
	case searchv1.Mode_MODE_TRAIN:
		return service.ModeTrain
	case searchv1.Mode_MODE_FLIGHT:
		return service.ModeFlight
	default:
		return service.Mode("")
	}
}

func modeToProto(m service.Mode) searchv1.Mode {
	switch m {
	case service.ModeBus:
		return searchv1.Mode_MODE_BUS
	case service.ModeTrain:
		return searchv1.Mode_MODE_TRAIN
	case service.ModeFlight:
		return searchv1.Mode_MODE_FLIGHT
	default:
		return searchv1.Mode_MODE_UNSPECIFIED
	}
}

func tripToProto(t service.Trip) *searchv1.Trip {
	classes := make([]*searchv1.TripClass, 0, len(t.Classes))
	for _, c := range t.Classes {
		classes = append(classes, &searchv1.TripClass{
			Code:      c.Code,
			FarePaise: int64(c.FarePaise),
			Capacity:  int32(c.Capacity),
		})
	}
	return &searchv1.Trip{
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
