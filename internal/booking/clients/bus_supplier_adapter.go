package clients

import (
	"context"

	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"
)

// BusSupplierAdapter is the bus SupplierAdapter implementation (CLAUDE.md
// section 2: "One booking engine, three adapters"). The Mode is baked in
// here, not passed around by the saga - a future train or flight adapter
// wraps entirely different logic behind the same two-method interface.
type BusSupplierAdapter struct {
	client suppliersimv1.SupplierSimServiceClient
}

func NewBusSupplierAdapter(client suppliersimv1.SupplierSimServiceClient) *BusSupplierAdapter {
	return &BusSupplierAdapter{client: client}
}

func (a *BusSupplierAdapter) Confirm(ctx context.Context, bookingID, tripID string, seatNos []string) (string, error) {
	resp, err := a.client.Confirm(ctx, &suppliersimv1.ConfirmRequest{
		BookingId: bookingID,
		TripId:    tripID,
		SeatNos:   seatNos,
		Mode:      suppliersimv1.Mode_MODE_BUS,
	})
	if err != nil {
		return "", grpcToAppError(err)
	}
	return resp.GetSupplierPnr(), nil
}

func (a *BusSupplierAdapter) Cancel(ctx context.Context, supplierPNR string) error {
	_, err := a.client.Cancel(ctx, &suppliersimv1.CancelRequest{SupplierPnr: supplierPNR})
	return grpcToAppError(err)
}
