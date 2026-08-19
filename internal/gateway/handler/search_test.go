package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	searchv1 "github.com/Muditjain13/transport-app/api/proto/search/v1"
)

type fakeSearchClient struct {
	searchv1.SearchServiceClient
	resp    *searchv1.SearchTripsResponse
	err     error
	lastReq *searchv1.SearchTripsRequest
}

func (f *fakeSearchClient) SearchTrips(ctx context.Context, in *searchv1.SearchTripsRequest, opts ...grpc.CallOption) (*searchv1.SearchTripsResponse, error) {
	f.lastReq = in
	return f.resp, f.err
}

func TestSearchHandler_MissingParams(t *testing.T) {
	h := NewSearchHandler(&fakeSearchClient{})
	req := httptest.NewRequest("GET", "/v1/search", nil)
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSearchHandler_InvalidDate(t *testing.T) {
	h := NewSearchHandler(&fakeSearchClient{})
	req := httptest.NewRequest("GET", "/v1/search?origin=DEL&dest=BOM&date=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSearchHandler_Success(t *testing.T) {
	fake := &fakeSearchClient{
		resp: &searchv1.SearchTripsResponse{
			Trips: []*searchv1.Trip{
				{Id: "bus-1", Mode: searchv1.Mode_MODE_BUS, DepartureAt: timestamppb.Now(), ArrivalAt: timestamppb.Now()},
			},
			DegradedProviders: []searchv1.Mode{searchv1.Mode_MODE_FLIGHT},
		},
	}
	h := NewSearchHandler(fake)
	req := httptest.NewRequest("GET", "/v1/search?origin=DEL&dest=BOM&date=2026-03-01", nil)
	rec := httptest.NewRecorder()
	h.Search(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastReq.GetOriginCode() != "DEL" || fake.lastReq.GetDestCode() != "BOM" {
		t.Errorf("request forwarded incorrectly: %+v", fake.lastReq)
	}

	var out searchResponseJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out.Trips) != 1 || out.Trips[0].ID != "bus-1" {
		t.Errorf("Trips = %+v", out.Trips)
	}
	if len(out.DegradedProviders) != 1 {
		t.Errorf("DegradedProviders = %v, want one entry", out.DegradedProviders)
	}
}

func TestSearchHandler_UpstreamError(t *testing.T) {
	fake := &fakeSearchClient{err: status.Error(codes.Internal, "boom")}
	h := NewSearchHandler(fake)
	req := httptest.NewRequest("GET", "/v1/search?origin=DEL&dest=BOM&date=2026-03-01", nil)
	rec := httptest.NewRecorder()
	h.Search(rec, req)
	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
