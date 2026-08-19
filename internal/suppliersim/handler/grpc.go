// Package handler is supplier-sim's gRPC transport layer.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"
	"github.com/Muditjain13/transport-app/internal/suppliersim/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

type GRPCHandler struct {
	suppliersimv1.UnimplementedSupplierSimServiceServer
	svc *service.Service
}

func NewGRPCHandler(svc *service.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

func (h *GRPCHandler) Hold(ctx context.Context, req *suppliersimv1.HoldRequest) (*suppliersimv1.HoldResponse, error) {
	if req.GetBookingId() == "" || req.GetTripId() == "" {
		return nil, status.Error(codes.InvalidArgument, "booking_id and trip_id are required")
	}
	if req.GetSeatCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seat_count must be positive")
	}

	ref, err := h.svc.Hold(ctx, req.GetBookingId(), req.GetTripId(), int(req.GetSeatCount()), modeFromProto(req.GetMode()))
	if err != nil {
		return nil, mapError(err)
	}
	return &suppliersimv1.HoldResponse{HoldRef: ref}, nil
}

func (h *GRPCHandler) ReleaseHold(ctx context.Context, req *suppliersimv1.ReleaseHoldRequest) (*suppliersimv1.ReleaseHoldResponse, error) {
	if req.GetHoldRef() == "" {
		return nil, status.Error(codes.InvalidArgument, "hold_ref is required")
	}
	if err := h.svc.ReleaseHold(ctx, req.GetHoldRef()); err != nil {
		return nil, mapError(err)
	}
	return &suppliersimv1.ReleaseHoldResponse{}, nil
}

func (h *GRPCHandler) Confirm(ctx context.Context, req *suppliersimv1.ConfirmRequest) (*suppliersimv1.ConfirmResponse, error) {
	if req.GetBookingId() == "" || req.GetTripId() == "" {
		return nil, status.Error(codes.InvalidArgument, "booking_id and trip_id are required")
	}
	if len(req.GetSeatNos()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one seat_no is required")
	}

	pnr, err := h.svc.Confirm(ctx, req.GetBookingId(), req.GetTripId(), req.GetSeatNos(), modeFromProto(req.GetMode()))
	if err != nil {
		return nil, mapError(err)
	}
	return &suppliersimv1.ConfirmResponse{SupplierPnr: pnr}, nil
}

func (h *GRPCHandler) Cancel(ctx context.Context, req *suppliersimv1.CancelRequest) (*suppliersimv1.CancelResponse, error) {
	if req.GetSupplierPnr() == "" {
		return nil, status.Error(codes.InvalidArgument, "supplier_pnr is required")
	}
	if err := h.svc.Cancel(ctx, req.GetSupplierPnr()); err != nil {
		return nil, mapError(err)
	}
	return &suppliersimv1.CancelResponse{}, nil
}

func modeFromProto(m suppliersimv1.Mode) service.Mode {
	switch m {
	case suppliersimv1.Mode_MODE_TRAIN:
		return service.ModeTrain
	case suppliersimv1.Mode_MODE_FLIGHT:
		return service.ModeFlight
	default:
		return service.ModeBus
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, apperrors.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, apperrors.ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
