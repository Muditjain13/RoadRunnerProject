package clients

import (
	"context"

	paymentv1 "github.com/Muditjain13/transport-app/api/proto/payment/v1"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type PaymentClient struct {
	client paymentv1.PaymentServiceClient
}

func NewPaymentClient(client paymentv1.PaymentServiceClient) *PaymentClient {
	return &PaymentClient{client: client}
}

func (c *PaymentClient) Charge(ctx context.Context, idempotencyKey, bookingID, userID string, amountPaise money.Paise) (string, bool, error) {
	resp, err := c.client.Charge(ctx, &paymentv1.ChargeRequest{
		IdempotencyKey: idempotencyKey,
		BookingId:      bookingID,
		UserId:         userID,
		AmountPaise:    int64(amountPaise),
	})
	if err != nil {
		return "", false, grpcToAppError(err)
	}
	p := resp.GetPayment()
	return p.GetId(), p.GetStatus() == paymentv1.PaymentStatus_PAYMENT_STATUS_SUCCEEDED, nil
}

func (c *PaymentClient) Refund(ctx context.Context, idempotencyKey, paymentID string, amountPaise money.Paise) error {
	_, err := c.client.Refund(ctx, &paymentv1.RefundRequest{
		IdempotencyKey: idempotencyKey,
		PaymentId:      paymentID,
		AmountPaise:    int64(amountPaise),
	})
	return grpcToAppError(err)
}
