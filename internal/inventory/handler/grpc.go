// Package handler is inventory's gRPC transport layer. Translation and
// status-code mapping only - no locking or business logic here, that all
// lives in service.
package handler

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	"github.com/Muditjain13/transport-app/internal/inventory/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

type GRPCHandler struct {
	inventoryv1.UnimplementedInventoryServiceServer
	svc *service.Service
}

func NewGRPCHandler(svc *service.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

func (h *GRPCHandler) CreateHold(ctx context.Context, req *inventoryv1.CreateHoldRequest) (*inventoryv1.CreateHoldResponse, error) {
	if req.GetTripId() == "" || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trip_id and user_id are required")
	}
	if len(req.GetSeatNos()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one seat_no is required")
	}
	if req.GetTtlSeconds() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must be positive")
	}

	hold, err := h.svc.CreateHold(ctx, req.GetTripId(), req.GetUserId(), req.GetSeatNos(), time.Duration(req.GetTtlSeconds())*time.Second)
	if err != nil {
		return nil, mapError(err)
	}

	return &inventoryv1.CreateHoldResponse{
		HoldId:    hold.ID,
		TripId:    hold.TripID,
		SeatNos:   hold.SeatNos,
		ExpiresAt: timestamppb.New(hold.ExpiresAt),
	}, nil
}

func (h *GRPCHandler) ReleaseHold(ctx context.Context, req *inventoryv1.ReleaseHoldRequest) (*inventoryv1.ReleaseHoldResponse, error) {
	if req.GetHoldId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hold_id is required")
	}
	if err := h.svc.ReleaseHold(ctx, req.GetHoldId()); err != nil {
		return nil, mapError(err)
	}
	return &inventoryv1.ReleaseHoldResponse{}, nil
}

func (h *GRPCHandler) ConfirmHold(ctx context.Context, req *inventoryv1.ConfirmHoldRequest) (*inventoryv1.ConfirmHoldResponse, error) {
	if req.GetHoldId() == "" || req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "hold_id and booking_id are required")
	}
	if err := h.svc.ConfirmHold(ctx, req.GetHoldId(), req.GetBookingId()); err != nil {
		return nil, mapError(err)
	}
	return &inventoryv1.ConfirmHoldResponse{}, nil
}

func (h *GRPCHandler) CancelBooking(ctx context.Context, req *inventoryv1.CancelBookingRequest) (*inventoryv1.CancelBookingResponse, error) {
	if req.GetTripId() == "" || req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trip_id and booking_id are required")
	}
	if len(req.GetSeatNos()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one seat_no is required")
	}
	if err := h.svc.CancelBooking(ctx, req.GetTripId(), req.GetBookingId(), req.GetSeatNos()); err != nil {
		return nil, mapError(err)
	}
	return &inventoryv1.CancelBookingResponse{}, nil
}

func (h *GRPCHandler) ListAvailableSeats(ctx context.Context, req *inventoryv1.ListAvailableSeatsRequest) (*inventoryv1.ListAvailableSeatsResponse, error) {
	if req.GetTripId() == "" || req.GetClassCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "trip_id and class_code are required")
	}
	seatNos, err := h.svc.ListAvailableSeats(ctx, req.GetTripId(), req.GetClassCode())
	if err != nil {
		return nil, mapError(err)
	}
	return &inventoryv1.ListAvailableSeatsResponse{SeatNos: seatNos}, nil
}

func (h *GRPCHandler) CreateHoldByClass(ctx context.Context, req *inventoryv1.CreateHoldByClassRequest) (*inventoryv1.CreateHoldByClassResponse, error) {
	if req.GetTripId() == "" || req.GetClassCode() == "" || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trip_id, class_code and user_id are required")
	}
	if req.GetSeatCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seat_count must be positive")
	}
	if req.GetTtlSeconds() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl_seconds must be positive")
	}

	outcome, err := h.svc.CreateHoldByClass(ctx, req.GetTripId(), req.GetClassCode(), req.GetUserId(),
		int(req.GetSeatCount()), req.GetAmountPaise(), time.Duration(req.GetTtlSeconds())*time.Second)
	if err != nil {
		return nil, mapError(err)
	}

	resp := &inventoryv1.CreateHoldByClassResponse{}
	if outcome.Hold != nil {
		resp.Hold = &inventoryv1.CreateHoldResponse{
			HoldId:    outcome.Hold.ID,
			TripId:    outcome.Hold.TripID,
			SeatNos:   outcome.Hold.SeatNos,
			ExpiresAt: timestamppb.New(outcome.Hold.ExpiresAt),
		}
	} else {
		resp.Waitlist = waitlistEntryToProto(*outcome.Waitlist)
	}
	return resp, nil
}

func (h *GRPCHandler) ClaimPromotedWaitlist(ctx context.Context, req *inventoryv1.ClaimPromotedWaitlistRequest) (*inventoryv1.ClaimPromotedWaitlistResponse, error) {
	if req.GetWaitlistEntryId() == 0 || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "waitlist_entry_id and user_id are required")
	}
	holdID, seatNos, err := h.svc.ClaimPromotedWaitlist(ctx, req.GetWaitlistEntryId(), req.GetUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &inventoryv1.ClaimPromotedWaitlistResponse{HoldId: holdID, SeatNos: seatNos}, nil
}

func (h *GRPCHandler) ListWaitlistByUser(ctx context.Context, req *inventoryv1.ListWaitlistByUserRequest) (*inventoryv1.ListWaitlistByUserResponse, error) {
	if req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	entries, err := h.svc.ListWaitlistByUser(ctx, req.GetUserId())
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*inventoryv1.WaitlistEntry, len(entries))
	for i, e := range entries {
		out[i] = waitlistEntryToProto(e)
	}
	return &inventoryv1.ListWaitlistByUserResponse{Entries: out}, nil
}

func waitlistEntryToProto(e service.WaitlistEntry) *inventoryv1.WaitlistEntry {
	return &inventoryv1.WaitlistEntry{
		Id:             e.ID,
		TripId:         e.TripID,
		ClassCode:      e.ClassCode,
		UserId:         e.UserID,
		SeatCount:      int32(e.SeatCount),
		AmountPaise:    e.AmountPaise,
		Status:         waitlistStatusToProto(e.Status),
		PromotedHoldId: e.PromotedHoldID,
	}
}

func waitlistStatusToProto(s service.WaitlistStatus) inventoryv1.WaitlistStatus {
	switch s {
	case service.WaitlistPending:
		return inventoryv1.WaitlistStatus_WAITLIST_STATUS_PENDING
	case service.WaitlistPromoted:
		return inventoryv1.WaitlistStatus_WAITLIST_STATUS_PROMOTED
	case service.WaitlistCancelled:
		return inventoryv1.WaitlistStatus_WAITLIST_STATUS_CANCELLED
	default:
		return inventoryv1.WaitlistStatus_WAITLIST_STATUS_UNSPECIFIED
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, apperrors.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, apperrors.ErrConflict):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
