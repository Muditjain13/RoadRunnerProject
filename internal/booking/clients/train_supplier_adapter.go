package clients

import (
	"context"

	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"
)

// TrainSupplierAdapter is the train SupplierAdapter implementation
// (CLAUDE.md section 2: "One booking engine, three adapters"). Identical
// shape to BusSupplierAdapter - only the wire-level Mode differs.
type TrainSupplierAdapter struct {
	client suppliersimv1.SupplierSimServiceClient
}

func NewTrainSupplierAdapter(client suppliersimv1.SupplierSimServiceClient) *TrainSupplierAdapter {
	return &TrainSupplierAdapter{client: client}
}

func (a *TrainSupplierAdapter) Confirm(ctx context.Context, bookingID, tripID string, seatNos []string) (string, error) {
	resp, err := a.client.Confirm(ctx, &suppliersimv1.ConfirmRequest{
		BookingId: bookingID,
		TripId:    tripID,
		SeatNos:   seatNos,
		Mode:      suppliersimv1.Mode_MODE_TRAIN,
	})
	if err != nil {
		return "", grpcToAppError(err)
	}
	return resp.GetSupplierPnr(), nil
}

func (a *TrainSupplierAdapter) Cancel(ctx context.Context, supplierPNR string) error {
	_, err := a.client.Cancel(ctx, &suppliersimv1.CancelRequest{SupplierPnr: supplierPNR})
	return grpcToAppError(err)
}
