package clients

import (
	"context"
	"errors"

	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"
	"github.com/Muditjain13/transport-app/pkg/circuitbreaker"
	apperrors "github.com/Muditjain13/transport-app/pkg/errors"
)

// FlightSupplierAdapter is the flight SupplierAdapter implementation
// (CLAUDE.md section 2: "One booking engine, three adapters"). Unlike
// bus and train, it also implements service.FlightHolder and
// service.HoldReleaser: flight owns no local inventory, so its seats are
// reserved with the provider itself before payment (CLAUDE.md: "Provider
// owns it... No local lock possible. External hold").
//
// Every call goes through a circuit breaker (CLAUDE.md section 7:
// "Circuit breaker on every outbound supplier call") - the same shared
// pkg/circuitbreaker search already uses per provider.
type FlightSupplierAdapter struct {
	client  suppliersimv1.SupplierSimServiceClient
	breaker *circuitbreaker.Breaker
}

func NewFlightSupplierAdapter(client suppliersimv1.SupplierSimServiceClient, breaker *circuitbreaker.Breaker) *FlightSupplierAdapter {
	return &FlightSupplierAdapter{client: client, breaker: breaker}
}

// doWithBreaker runs fn through the circuit breaker, but only counts
// genuine infra failures against it. A supplier declining a hold or
// confirmation (apperrors.ErrUnavailable, chaos-injected) is a normal
// business response the saga is specifically built to handle (refund and
// compensate) - it is not evidence the provider itself is slow or down,
// and must not trip the breaker. If it did, the breaker would eventually
// open under `make chaos`'s 40% decline rate for reasons unrelated to
// provider health, and every subsequent flight booking would fail with
// ErrOpen instead of exercising the hard-case compensation path this
// saga exists to prove.
func (a *FlightSupplierAdapter) doWithBreaker(ctx context.Context, fn func(ctx context.Context) error) error {
	var callErr error
	breakerErr := a.breaker.Do(ctx, func(ctx context.Context) error {
		callErr = fn(ctx)
		if errors.Is(callErr, apperrors.ErrUnavailable) {
			return nil // a decline is not a breaker failure
		}
		return callErr
	})
	if errors.Is(breakerErr, circuitbreaker.ErrOpen) {
		return breakerErr
	}
	return callErr
}

func (a *FlightSupplierAdapter) Hold(ctx context.Context, bookingID, tripID string, seatCount int) (string, error) {
	var ref string
	err := a.doWithBreaker(ctx, func(ctx context.Context) error {
		resp, err := a.client.Hold(ctx, &suppliersimv1.HoldRequest{
			BookingId: bookingID,
			TripId:    tripID,
			SeatCount: int32(seatCount),
			Mode:      suppliersimv1.Mode_MODE_FLIGHT,
		})
		if err != nil {
			return grpcToAppError(err)
		}
		ref = resp.GetHoldRef()
		return nil
	})
	return ref, err
}

func (a *FlightSupplierAdapter) ReleaseHold(ctx context.Context, externalHoldRef string) error {
	return a.doWithBreaker(ctx, func(ctx context.Context) error {
		_, err := a.client.ReleaseHold(ctx, &suppliersimv1.ReleaseHoldRequest{HoldRef: externalHoldRef})
		return grpcToAppError(err)
	})
}

func (a *FlightSupplierAdapter) Confirm(ctx context.Context, bookingID, tripID string, seatNos []string) (string, error) {
	var pnr string
	err := a.doWithBreaker(ctx, func(ctx context.Context) error {
		resp, err := a.client.Confirm(ctx, &suppliersimv1.ConfirmRequest{
			BookingId: bookingID,
			TripId:    tripID,
			SeatNos:   seatNos,
			Mode:      suppliersimv1.Mode_MODE_FLIGHT,
		})
		if err != nil {
			return grpcToAppError(err)
		}
		pnr = resp.GetSupplierPnr()
		return nil
	})
	return pnr, err
}

func (a *FlightSupplierAdapter) Cancel(ctx context.Context, supplierPNR string) error {
	return a.doWithBreaker(ctx, func(ctx context.Context) error {
		_, err := a.client.Cancel(ctx, &suppliersimv1.CancelRequest{SupplierPnr: supplierPNR})
		return grpcToAppError(err)
	})
}
