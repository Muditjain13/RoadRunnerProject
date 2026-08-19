// Package service implements payment's core correctness properties:
// idempotent charges (a retried Charge with the same key never charges
// twice) and refunds that can never exceed what was actually paid, even
// under concurrent refund attempts against the same payment.
package service

import (
	"context"

	"github.com/Muditjain13/transport-app/pkg/money"
)

type PaymentStatus string

const (
	PaymentSucceeded PaymentStatus = "succeeded"
	PaymentFailed    PaymentStatus = "failed"
)

type RefundStatus string

const (
	RefundSucceeded RefundStatus = "succeeded"
	RefundFailed    RefundStatus = "failed"
)

type Payment struct {
	ID             string
	BookingID      string
	UserID         string
	AmountPaise    money.Paise
	Status         PaymentStatus
	IdempotencyKey string
}

type Refund struct {
	ID             string
	PaymentID      string
	AmountPaise    money.Paise
	Status         RefundStatus
	IdempotencyKey string
}

// TxRepository is everything the service needs from a single transaction.
// Charge and Refund each run entirely inside one Repository.WithinTx call.
type TxRepository interface {
	// GetPaymentByIdempotencyKey and GetRefundByIdempotencyKey return
	// apperrors.ErrNotFound when no row matches - the service treats that
	// as "not a replay, proceed", anything else as a real error.
	GetPaymentByIdempotencyKey(ctx context.Context, key string) (Payment, error)
	InsertPayment(ctx context.Context, p Payment) error
	// GetPaymentForUpdate locks the payment row so concurrent refund
	// attempts against the same payment serialize.
	GetPaymentForUpdate(ctx context.Context, paymentID string) (Payment, error)

	GetRefundByIdempotencyKey(ctx context.Context, key string) (Refund, error)
	InsertRefund(ctx context.Context, r Refund) error
	// SumSucceededRefunds must be called after GetPaymentForUpdate in the
	// same transaction, so no concurrent refund can land between the sum
	// and the caller's over-refund check.
	SumSucceededRefunds(ctx context.Context, paymentID string) (money.Paise, error)
}

type Repository interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context, tx TxRepository) error) error
	GetPayment(ctx context.Context, paymentID string) (Payment, error)
}
