// Package repo is payment's MySQL implementation of service.Repository.
package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"

	"github.com/Muditjain13/transport-app/internal/payment/repo/sqlcgen"
	"github.com/Muditjain13/transport-app/internal/payment/service"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
	"github.com/Muditjain13/transport-app/pkg/money"
)

// mysqlErrDupEntry is MySQL error 1062, a unique-key violation.
const mysqlErrDupEntry = 1062

type MySQLRepo struct {
	db *sql.DB
}

func NewMySQLRepo(db *sql.DB) *MySQLRepo {
	return &MySQLRepo{db: db}
}

func (r *MySQLRepo) WithinTx(ctx context.Context, fn func(ctx context.Context, tx service.TxRepository) error) error {
	sqlTx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	if err := fn(ctx, &mysqlTx{q: sqlcgen.New(sqlTx)}); err != nil {
		if rbErr := sqlTx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			return fmt.Errorf("%w (rollback also failed: %v)", err, rbErr)
		}
		return err
	}

	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func (r *MySQLRepo) GetPayment(ctx context.Context, paymentID string) (service.Payment, error) {
	row, err := sqlcgen.New(r.db).GetPayment(ctx, paymentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Payment{}, fmt.Errorf("get payment %s: %w", paymentID, apperrors.ErrNotFound)
		}
		return service.Payment{}, fmt.Errorf("get payment: %w", err)
	}
	return service.Payment{
		ID:             row.ID,
		BookingID:      row.BookingID,
		UserID:         row.UserID,
		AmountPaise:    money.Paise(row.AmountPaise),
		Status:         service.PaymentStatus(row.Status),
		IdempotencyKey: row.IdempotencyKey,
	}, nil
}

// mysqlTx implements service.TxRepository against one open transaction.
type mysqlTx struct {
	q *sqlcgen.Queries
}

func (t *mysqlTx) GetPaymentByIdempotencyKey(ctx context.Context, key string) (service.Payment, error) {
	row, err := t.q.GetPaymentByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Payment{}, apperrors.ErrNotFound
		}
		return service.Payment{}, fmt.Errorf("get payment by idempotency key: %w", err)
	}
	return service.Payment{
		ID:             row.ID,
		BookingID:      row.BookingID,
		UserID:         row.UserID,
		AmountPaise:    money.Paise(row.AmountPaise),
		Status:         service.PaymentStatus(row.Status),
		IdempotencyKey: row.IdempotencyKey,
	}, nil
}

func (t *mysqlTx) InsertPayment(ctx context.Context, p service.Payment) error {
	err := t.q.InsertPayment(ctx, sqlcgen.InsertPaymentParams{
		ID:             p.ID,
		BookingID:      p.BookingID,
		UserID:         p.UserID,
		AmountPaise:    int64(p.AmountPaise),
		Status:         sqlcgen.PaymentsStatus(p.Status),
		IdempotencyKey: p.IdempotencyKey,
	})
	if err != nil {
		var myErr *mysql.MySQLError
		if errors.As(err, &myErr) && myErr.Number == mysqlErrDupEntry {
			return fmt.Errorf("insert payment: %w", apperrors.ErrAlreadyExists)
		}
		return fmt.Errorf("insert payment: %w", err)
	}
	return nil
}

func (t *mysqlTx) GetPaymentForUpdate(ctx context.Context, paymentID string) (service.Payment, error) {
	row, err := t.q.GetPaymentForUpdate(ctx, paymentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Payment{}, fmt.Errorf("get payment %s for update: %w", paymentID, apperrors.ErrNotFound)
		}
		return service.Payment{}, fmt.Errorf("get payment for update: %w", err)
	}
	return service.Payment{
		ID:             row.ID,
		BookingID:      row.BookingID,
		UserID:         row.UserID,
		AmountPaise:    money.Paise(row.AmountPaise),
		Status:         service.PaymentStatus(row.Status),
		IdempotencyKey: row.IdempotencyKey,
	}, nil
}

func (t *mysqlTx) GetRefundByIdempotencyKey(ctx context.Context, key string) (service.Refund, error) {
	row, err := t.q.GetRefundByIdempotencyKey(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return service.Refund{}, apperrors.ErrNotFound
		}
		return service.Refund{}, fmt.Errorf("get refund by idempotency key: %w", err)
	}
	return service.Refund{
		ID:             row.ID,
		PaymentID:      row.PaymentID,
		AmountPaise:    money.Paise(row.AmountPaise),
		Status:         service.RefundStatus(row.Status),
		IdempotencyKey: row.IdempotencyKey,
	}, nil
}

func (t *mysqlTx) InsertRefund(ctx context.Context, r service.Refund) error {
	err := t.q.InsertRefund(ctx, sqlcgen.InsertRefundParams{
		ID:             r.ID,
		PaymentID:      r.PaymentID,
		AmountPaise:    int64(r.AmountPaise),
		Status:         sqlcgen.RefundsStatus(r.Status),
		IdempotencyKey: r.IdempotencyKey,
	})
	if err != nil {
		return fmt.Errorf("insert refund: %w", err)
	}
	return nil
}

func (t *mysqlTx) SumSucceededRefunds(ctx context.Context, paymentID string) (money.Paise, error) {
	total, err := t.q.SumSucceededRefunds(ctx, paymentID)
	if err != nil {
		return 0, fmt.Errorf("sum succeeded refunds: %w", err)
	}
	return money.Paise(total), nil
}
