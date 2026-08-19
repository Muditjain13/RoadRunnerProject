// Package handler is payment's gRPC transport layer.
package handler

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	paymentv1 "github.com/Muditjain13/transport-app/api/proto/payment/v1"
	"github.com/Muditjain13/transport-app/internal/payment/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type GRPCHandler struct {
	paymentv1.UnimplementedPaymentServiceServer
	svc *service.Service
}

func NewGRPCHandler(svc *service.Service) *GRPCHandler {
	return &GRPCHandler{svc: svc}
}

func (h *GRPCHandler) Charge(ctx context.Context, req *paymentv1.ChargeRequest) (*paymentv1.ChargeResponse, error) {
	if req.GetIdempotencyKey() == "" || req.GetBookingId() == "" || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key, booking_id, and user_id are required")
	}
	if req.GetAmountPaise() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_paise must be positive")
	}

	payment, err := h.svc.Charge(ctx, req.GetIdempotencyKey(), req.GetBookingId(), req.GetUserId(), money.Paise(req.GetAmountPaise()))
	if err != nil {
		return nil, mapError(err)
	}
	return &paymentv1.ChargeResponse{Payment: paymentToProto(payment)}, nil
}

func (h *GRPCHandler) Refund(ctx context.Context, req *paymentv1.RefundRequest) (*paymentv1.RefundResponse, error) {
	if req.GetIdempotencyKey() == "" || req.GetPaymentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key and payment_id are required")
	}
	if req.GetAmountPaise() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_paise must be positive")
	}

	refund, err := h.svc.Refund(ctx, req.GetIdempotencyKey(), req.GetPaymentId(), money.Paise(req.GetAmountPaise()))
	if err != nil {
		return nil, mapError(err)
	}
	return &paymentv1.RefundResponse{Refund: refundToProto(refund)}, nil
}

func (h *GRPCHandler) GetPayment(ctx context.Context, req *paymentv1.GetPaymentRequest) (*paymentv1.GetPaymentResponse, error) {
	if req.GetPaymentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "payment_id is required")
	}
	payment, err := h.svc.GetPayment(ctx, req.GetPaymentId())
	if err != nil {
		return nil, mapError(err)
	}
	return &paymentv1.GetPaymentResponse{Payment: paymentToProto(payment)}, nil
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

func paymentToProto(p service.Payment) *paymentv1.Payment {
	return &paymentv1.Payment{
		Id:             p.ID,
		BookingId:      p.BookingID,
		UserId:         p.UserID,
		AmountPaise:    int64(p.AmountPaise),
		Status:         paymentStatusToProto(p.Status),
		IdempotencyKey: p.IdempotencyKey,
	}
}

func refundToProto(r service.Refund) *paymentv1.Refund {
	return &paymentv1.Refund{
		Id:             r.ID,
		PaymentId:      r.PaymentID,
		AmountPaise:    int64(r.AmountPaise),
		Status:         refundStatusToProto(r.Status),
		IdempotencyKey: r.IdempotencyKey,
	}
}

func paymentStatusToProto(s service.PaymentStatus) paymentv1.PaymentStatus {
	switch s {
	case service.PaymentSucceeded:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED
	case service.PaymentFailed:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_FAILED
	default:
		return paymentv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED
	}
}

func refundStatusToProto(s service.RefundStatus) paymentv1.RefundStatus {
	switch s {
	case service.RefundSucceeded:
		return paymentv1.RefundStatus_REFUND_STATUS_SUCCEEDED
	case service.RefundFailed:
		return paymentv1.RefundStatus_REFUND_STATUS_FAILED
	default:
		return paymentv1.RefundStatus_REFUND_STATUS_UNSPECIFIED
	}
}
