// Package clients wraps gRPC clients to inventory, payment, and
// supplier-sim as implementations of the interfaces internal/booking/service
// depends on. The saga logic never imports a proto package directly.
package clients

import (
	"context"
	"time"

	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	"github.com/Muditjain13/transport-app/pkg/money"
)

type InventoryClient struct {
	client inventoryv1.InventoryServiceClient
}

func NewInventoryClient(client inventoryv1.InventoryServiceClient) *InventoryClient {
	return &InventoryClient{client: client}
}

func (c *InventoryClient) CreateHold(ctx context.Context, tripID, userID string, seatNos []string, ttl time.Duration) (string, error) {
	resp, err := c.client.CreateHold(ctx, &inventoryv1.CreateHoldRequest{
		TripId:     tripID,
		UserId:     userID,
		SeatNos:    seatNos,
		TtlSeconds: int32(ttl.Seconds()),
	})
	if err != nil {
		return "", grpcToAppError(err)
	}
	return resp.GetHoldId(), nil
}

func (c *InventoryClient) ReleaseHold(ctx context.Context, holdID string) error {
	_, err := c.client.ReleaseHold(ctx, &inventoryv1.ReleaseHoldRequest{HoldId: holdID})
	return grpcToAppError(err)
}

func (c *InventoryClient) ConfirmHold(ctx context.Context, holdID, bookingID string) error {
	_, err := c.client.ConfirmHold(ctx, &inventoryv1.ConfirmHoldRequest{HoldId: holdID, BookingId: bookingID})
	return grpcToAppError(err)
}

func (c *InventoryClient) CancelBooking(ctx context.Context, tripID, bookingID string, seatNos []string) error {
	_, err := c.client.CancelBooking(ctx, &inventoryv1.CancelBookingRequest{
		TripId:    tripID,
		BookingId: bookingID,
		SeatNos:   seatNos,
	})
	return grpcToAppError(err)
}

func (c *InventoryClient) CreateHoldByClass(ctx context.Context, tripID, classCode, userID string, seatCount int, amountPaise money.Paise, ttl time.Duration) (string, []string, int64, error) {
	resp, err := c.client.CreateHoldByClass(ctx, &inventoryv1.CreateHoldByClassRequest{
		TripId:      tripID,
		ClassCode:   classCode,
		UserId:      userID,
		SeatCount:   int32(seatCount),
		AmountPaise: int64(amountPaise),
		TtlSeconds:  int32(ttl.Seconds()),
	})
	if err != nil {
		return "", nil, 0, grpcToAppError(err)
	}
	if hold := resp.GetHold(); hold != nil {
		return hold.GetHoldId(), hold.GetSeatNos(), 0, nil
	}
	return "", nil, resp.GetWaitlist().GetId(), nil
}

func (c *InventoryClient) ClaimPromotedWaitlist(ctx context.Context, waitlistEntryID int64, userID string) (string, []string, error) {
	resp, err := c.client.ClaimPromotedWaitlist(ctx, &inventoryv1.ClaimPromotedWaitlistRequest{
		WaitlistEntryId: waitlistEntryID,
		UserId:          userID,
	})
	if err != nil {
		return "", nil, grpcToAppError(err)
	}
	return resp.GetHoldId(), resp.GetSeatNos(), nil
}
