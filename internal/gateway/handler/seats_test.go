package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
)

type fakeInventoryClient struct {
	inventoryv1.InventoryServiceClient
	resp    *inventoryv1.ListAvailableSeatsResponse
	err     error
	lastReq *inventoryv1.ListAvailableSeatsRequest
}

func (f *fakeInventoryClient) ListAvailableSeats(ctx context.Context, in *inventoryv1.ListAvailableSeatsRequest, opts ...grpc.CallOption) (*inventoryv1.ListAvailableSeatsResponse, error) {
	f.lastReq = in
	return f.resp, f.err
}

func TestSeatsHandler_MissingParams(t *testing.T) {
	h := NewSeatsHandler(&fakeInventoryClient{})
	req := httptest.NewRequest("GET", "/v1/trips//seats", nil)
	rec := httptest.NewRecorder()
	h.ListAvailableSeats(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSeatsHandler_Success(t *testing.T) {
	fake := &fakeInventoryClient{resp: &inventoryv1.ListAvailableSeatsResponse{SeatNos: []string{"seater-1", "seater-2"}}}
	h := NewSeatsHandler(fake)
	req := httptest.NewRequest("GET", "/v1/trips/trip-1/seats?class=seater", nil)
	req.SetPathValue("tripId", "trip-1")
	rec := httptest.NewRecorder()
	h.ListAvailableSeats(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastReq.GetTripId() != "trip-1" || fake.lastReq.GetClassCode() != "seater" {
		t.Errorf("request forwarded incorrectly: %+v", fake.lastReq)
	}
	var out map[string][]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out["seat_nos"]) != 2 {
		t.Errorf("seat_nos = %v, want 2 entries", out["seat_nos"])
	}
}

func TestSeatsHandler_UpstreamError(t *testing.T) {
	fake := &fakeInventoryClient{err: status.Error(codes.NotFound, "trip not found")}
	h := NewSeatsHandler(fake)
	req := httptest.NewRequest("GET", "/v1/trips/trip-1/seats?class=seater", nil)
	req.SetPathValue("tripId", "trip-1")
	rec := httptest.NewRecorder()
	h.ListAvailableSeats(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
