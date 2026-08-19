// Package handler is booking's gRPC transport layer.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bookingv1 "github.com/Muditjain13/transport-app/api/proto/booking/v1"
	"github.com/Muditjain13/transport-app/internal/booking/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type GRPCHandler struct {
	bookingv1.UnimplementedBookingServiceServer
	svc *service.Service
}

func NewGRPCHandler(svc *service.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

func (h *GRPCHandler) CreateBooking(ctx context.Context, req *bookingv1.CreateBookingRequest) (*bookingv1.CreateBookingResponse, error) {
	if req.GetUserId() == "" || req.GetTripId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and trip_id are required")
	}
	if len(req.GetSeatNos()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one seat_no is required")
	}
	if req.GetAmountPaise() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_paise must be positive")
	}

	booking, err := h.svc.CreateBooking(ctx, req.GetUserId(), req.GetTripId(), req.GetSeatNos(), modeFromProto(req.GetMode()), money.Paise(req.GetAmountPaise()))
	if err != nil {
		// A gRPC unary response is either a message or an error, never
		// both - the wire protocol has no slot for a "partial" result. A
		// booking left Held or Paid because a saga step hit a recoverable
		// infra error is a real, meaningful result (the reconciliation
		// sweeper will resume it), not an RPC failure, so it comes back
		// as a normal successful response with its actual status - only
		// a booking that was never created at all is a genuine error.
		if booking.ID != "" {
			return &bookingv1.CreateBookingResponse{Booking: bookingToProto(booking)}, nil
		}
		return nil, mapError(err)
	}
	return &bookingv1.CreateBookingResponse{Booking: bookingToProto(booking)}, nil
}

func (h *GRPCHandler) CancelBooking(ctx context.Context, req *bookingv1.CancelBookingRequest) (*bookingv1.CancelBookingResponse, error) {
	if req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "booking_id is required")
	}
	booking, err := h.svc.CancelBooking(ctx, req.GetBookingId())
	if err != nil {
		// See CreateBooking above: a real, non-terminal booking result
		// is a successful response, not an error.
		if booking.ID != "" {
			return &bookingv1.CancelBookingResponse{Booking: bookingToProto(booking)}, nil
		}
		return nil, mapError(err)
	}
	return &bookingv1.CancelBookingResponse{Booking: bookingToProto(booking)}, nil
}

func (h *GRPCHandler) GetBooking(ctx context.Context, req *bookingv1.GetBookingRequest) (*bookingv1.GetBookingResponse, error) {
	if req.GetBookingId() == "" {
		return nil, status.Error(codes.InvalidArgument, "booking_id is required")
	}
	booking, err := h.svc.GetBooking(ctx, req.GetBookingId())
	if err != nil {
		return nil, mapError(err)
	}
	return &bookingv1.GetBookingResponse{Booking: bookingToProto(booking)}, nil
}

func (h *GRPCHandler) CreateTrainBooking(ctx context.Context, req *bookingv1.CreateTrainBookingRequest) (*bookingv1.CreateTrainBookingResponse, error) {
	if req.GetUserId() == "" || req.GetTripId() == "" || req.GetClassCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, trip_id and class_code are required")
	}
	if req.GetSeatCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seat_count must be positive")
	}
	if req.GetAmountPaise() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_paise must be positive")
	}

	outcome, err := h.svc.CreateTrainBooking(ctx, req.GetUserId(), req.GetTripId(), req.GetClassCode(), int(req.GetSeatCount()), money.Paise(req.GetAmountPaise()))
	if err != nil {
		if outcome.Booking != nil && outcome.Booking.ID != "" {
			return &bookingv1.CreateTrainBookingResponse{Booking: bookingToProto(*outcome.Booking)}, nil
		}
		return nil, mapError(err)
	}
	resp := &bookingv1.CreateTrainBookingResponse{}
	if outcome.Booking != nil {
		resp.Booking = bookingToProto(*outcome.Booking)
	} else {
		resp.WaitlistEntryId = outcome.WaitlistEntryID
	}
	return resp, nil
}

func (h *GRPCHandler) CreateBookingFromWaitlist(ctx context.Context, req *bookingv1.CreateBookingFromWaitlistRequest) (*bookingv1.CreateBookingFromWaitlistResponse, error) {
	if req.GetWaitlistEntryId() == 0 || req.GetUserId() == "" || req.GetTripId() == "" {
		return nil, status.Error(codes.InvalidArgument, "waitlist_entry_id, user_id and trip_id are required")
	}
	if req.GetAmountPaise() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_paise must be positive")
	}

	booking, err := h.svc.CreateBookingFromWaitlist(ctx, req.GetWaitlistEntryId(), req.GetUserId(), req.GetTripId(), money.Paise(req.GetAmountPaise()))
	if err != nil {
		if booking.ID != "" {
			return &bookingv1.CreateBookingFromWaitlistResponse{Booking: bookingToProto(booking)}, nil
		}
		return nil, mapError(err)
	}
	return &bookingv1.CreateBookingFromWaitlistResponse{Booking: bookingToProto(booking)}, nil
}

func (h *GRPCHandler) CreateFlightBooking(ctx context.Context, req *bookingv1.CreateFlightBookingRequest) (*bookingv1.CreateFlightBookingResponse, error) {
	if req.GetUserId() == "" || req.GetTripId() == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and trip_id are required")
	}
	if req.GetSeatCount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seat_count must be positive")
	}
	if req.GetAmountPaise() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_paise must be positive")
	}

	booking, err := h.svc.CreateFlightBooking(ctx, req.GetUserId(), req.GetTripId(), int(req.GetSeatCount()), money.Paise(req.GetAmountPaise()))
	if err != nil {
		if booking.ID != "" {
			return &bookingv1.CreateFlightBookingResponse{Booking: bookingToProto(booking)}, nil
		}
		return nil, mapError(err)
	}
	return &bookingv1.CreateFlightBookingResponse{Booking: bookingToProto(booking)}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, apperrors.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, apperrors.ErrConflict):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, apperrors.ErrUnavailable):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func modeFromProto(m bookingv1.Mode) service.Mode {
	switch m {
	case bookingv1.Mode_MODE_TRAIN:
		return service.ModeTrain
	case bookingv1.Mode_MODE_FLIGHT:
		return service.ModeFlight
	default:
		return service.ModeBus
	}
}

func modeToProto(m service.Mode) bookingv1.Mode {
	switch m {
	case service.ModeBus:
		return bookingv1.Mode_MODE_BUS
	case service.ModeTrain:
		return bookingv1.Mode_MODE_TRAIN
	case service.ModeFlight:
		return bookingv1.Mode_MODE_FLIGHT
	default:
		return bookingv1.Mode_MODE_UNSPECIFIED
	}
}

func statusToProto(s service.Status) bookingv1.Status {
	switch s {
	case service.StatusPending:
		return bookingv1.Status_STATUS_PENDING
	case service.StatusHeld:
		return bookingv1.Status_STATUS_HELD
	case service.StatusPaid:
		return bookingv1.Status_STATUS_PAID
	case service.StatusConfirmed:
		return bookingv1.Status_STATUS_CONFIRMED
	case service.StatusRefunding:
		return bookingv1.Status_STATUS_REFUNDING
	case service.StatusFailed:
		return bookingv1.Status_STATUS_FAILED
	case service.StatusCancelled:
		return bookingv1.Status_STATUS_CANCELLED
	default:
		return bookingv1.Status_STATUS_UNSPECIFIED
	}
}

func bookingToProto(b service.Booking) *bookingv1.Booking {
	return &bookingv1.Booking{
		Id:            b.ID,
		Pnr:           b.PNR,
		SupplierPnr:   b.SupplierPNR,
		UserId:        b.UserID,
		TripId:        b.TripID,
		Mode:          modeToProto(b.Mode),
		SeatNos:       b.SeatNos,
		AmountPaise:   int64(b.AmountPaise),
		Status:        statusToProto(b.Status),
		FailureReason: b.FailureReason,
	}
}
