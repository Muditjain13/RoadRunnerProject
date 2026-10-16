package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

// Gateway simulates an external payment processor's approve/decline
// decision. The default always approves - there is no real payment
// network behind this project. Tests and callers that want to exercise
// the decline path (and the saga's compensation response to it) can
// inject a different function via NewService's variadic Option.
type Gateway func(amountPaise money.Paise) bool

func alwaysApprove(money.Paise) bool { return true }

type Service struct {
	repo    Repository
	gateway Gateway
}

type Option func(*Service)

func WithGateway(g Gateway) Option {
	return func(s *Service) { s.gateway = g }
}

func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo, gateway: alwaysApprove}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Charge is idempotent on idempotencyKey: a retried call with the same key
// (e.g. booking's saga retrying after a network timeout) replays the
// already-recorded outcome instead of charging again. A gateway decline is
// not a Go error - it is a Payment with Status = PaymentFailed. The caller
// (booking's saga) is the one that decides a decline means "compensate".
func (s *Service) Charge(ctx context.Context, idempotencyKey, bookingID, userID string, amountPaise money.Paise) (Payment, error) {
	if idempotencyKey == "" {
		return Payment{}, fmt.Errorf("charge: idempotency key is required")
	}
	if bookingID == "" || userID == "" {
		return Payment{}, fmt.Errorf("charge: bookingID and userID are required")
	}
	if amountPaise <= 0 {
		return Payment{}, fmt.Errorf("charge: amountPaise must be positive")
	}

	// Two callers racing on the same key both miss the lookup below; the
	// unique index lets exactly one insert win and the loser gets
	// ErrAlreadyExists. Retry once in a fresh transaction (a new snapshot),
	// where the winner's row is visible and replays as an idempotent hit.
	var result Payment
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		result, err = s.charge(ctx, idempotencyKey, bookingID, userID, amountPaise)
		if !errors.Is(err, apperrors.ErrAlreadyExists) {
			break
		}
	}
	if err != nil {
		return Payment{}, err
	}
	return result, nil
}

func (s *Service) charge(ctx context.Context, idempotencyKey, bookingID, userID string, amountPaise money.Paise) (Payment, error) {
	var result Payment
	err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		existing, err := tx.GetPaymentByIdempotencyKey(ctx, idempotencyKey)
		if err == nil {
			result = existing
			return nil
		}
		if !errors.Is(err, apperrors.ErrNotFound) {
			return fmt.Errorf("get payment by idempotency key: %w", err)
		}

		status := PaymentFailed
		if s.gateway(amountPaise) {
			status = PaymentSucceeded
		}
		payment := Payment{
			ID:             uuid.NewString(),
			BookingID:      bookingID,
			UserID:         userID,
			AmountPaise:    amountPaise,
			Status:         status,
			IdempotencyKey: idempotencyKey,
		}
		if err := tx.InsertPayment(ctx, payment); err != nil {
			return fmt.Errorf("insert payment: %w", err)
		}
		result = payment
		return nil
	})
	if err != nil {
		return Payment{}, err
	}
	return result, nil
}

// Refund is idempotent on idempotencyKey, same as Charge. It also enforces
// that a payment's total refunded amount never exceeds what was charged,
// even under concurrent refund attempts: GetPaymentForUpdate locks the
// payment row for the rest of the transaction, so the sum-then-insert
// below can't race with another refund on the same payment.
func (s *Service) Refund(ctx context.Context, idempotencyKey, paymentID string, amountPaise money.Paise) (Refund, error) {
	if idempotencyKey == "" {
		return Refund{}, fmt.Errorf("refund: idempotency key is required")
	}
	if paymentID == "" {
		return Refund{}, fmt.Errorf("refund: paymentID is required")
	}
	if amountPaise <= 0 {
		return Refund{}, fmt.Errorf("refund: amountPaise must be positive")
	}

	var result Refund
	err := s.repo.WithinTx(ctx, func(ctx context.Context, tx TxRepository) error {
		// The row lock must be the first statement in the transaction. A
		// plain read before it would pin the transaction's snapshot
		// (REPEATABLE READ), and the sum below would then miss refunds
		// committed by other transactions while this one waited for the lock.
		payment, err := tx.GetPaymentForUpdate(ctx, paymentID)
		if err != nil {
			return fmt.Errorf("get payment for update: %w", err)
		}

		existing, err := tx.GetRefundByIdempotencyKey(ctx, idempotencyKey)
		if err == nil {
			result = existing
			return nil
		}
		if !errors.Is(err, apperrors.ErrNotFound) {
			return fmt.Errorf("get refund by idempotency key: %w", err)
		}
		if payment.Status != PaymentSucceeded {
			return fmt.Errorf("refund payment %s: %w: payment did not succeed", paymentID, apperrors.ErrConflict)
		}

		alreadyRefunded, err := tx.SumSucceededRefunds(ctx, paymentID)
		if err != nil {
			return fmt.Errorf("sum succeeded refunds: %w", err)
		}
		if alreadyRefunded+amountPaise > payment.AmountPaise {
			return fmt.Errorf("refund payment %s: %w: refund of %v would exceed charged amount %v (already refunded %v)",
				paymentID, apperrors.ErrConflict, amountPaise, payment.AmountPaise, alreadyRefunded)
		}

		status := RefundFailed
		if s.gateway(amountPaise) {
			status = RefundSucceeded
		}
		refund := Refund{
			ID:             uuid.NewString(),
			PaymentID:      paymentID,
			AmountPaise:    amountPaise,
			Status:         status,
			IdempotencyKey: idempotencyKey,
		}
		if err := tx.InsertRefund(ctx, refund); err != nil {
			return fmt.Errorf("insert refund: %w", err)
		}
		result = refund
		return nil
	})
	if err != nil {
		return Refund{}, err
	}
	return result, nil
}

func (s *Service) GetPayment(ctx context.Context, paymentID string) (Payment, error) {
	return s.repo.GetPayment(ctx, paymentID)
}
