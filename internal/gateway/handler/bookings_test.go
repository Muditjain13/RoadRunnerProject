package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	bookingv1 "github.com/Muditjain13/transport-app/api/proto/booking/v1"
	"github.com/Muditjain13/transport-app/internal/gateway/middleware"
)

type fakeBookingClient struct {
	bookingv1.BookingServiceClient

	createResp *bookingv1.CreateBookingResponse
	createErr  error
	lastCreate *bookingv1.CreateBookingRequest

	cancelResp *bookingv1.CancelBookingResponse
	cancelErr  error

	getResp *bookingv1.GetBookingResponse
	getErr  error
}

func (f *fakeBookingClient) CreateBooking(ctx context.Context, in *bookingv1.CreateBookingRequest, opts ...grpc.CallOption) (*bookingv1.CreateBookingResponse, error) {
	f.lastCreate = in
	return f.createResp, f.createErr
}

func (f *fakeBookingClient) CancelBooking(ctx context.Context, in *bookingv1.CancelBookingRequest, opts ...grpc.CallOption) (*bookingv1.CancelBookingResponse, error) {
	return f.cancelResp, f.cancelErr
}

func (f *fakeBookingClient) GetBooking(ctx context.Context, in *bookingv1.GetBookingRequest, opts ...grpc.CallOption) (*bookingv1.GetBookingResponse, error) {
	return f.getResp, f.getErr
}

func TestCreateBooking_RequiresAuth(t *testing.T) {
	h := NewBookingHandler(&fakeBookingClient{})
	req := httptest.NewRequest("POST", "/v1/bookings", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestCreateBooking_ValidatesBody(t *testing.T) {
	h := NewBookingHandler(&fakeBookingClient{})
	req := httptest.NewRequest("POST", "/v1/bookings", bytes.NewReader([]byte(`{}`)))
	req = req.WithContext(contextWithUser(req.Context(), "user-1"))
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCreateBooking_Success(t *testing.T) {
	fake := &fakeBookingClient{
		createResp: &bookingv1.CreateBookingResponse{
			Booking: &bookingv1.Booking{Id: "b1", Status: bookingv1.Status_STATUS_CONFIRMED, Pnr: "ABC123"},
		},
	}
	h := NewBookingHandler(fake)
	body := []byte(`{"trip_id":"trip-1","seat_nos":["1","2"],"amount_paise":10000}`)
	req := httptest.NewRequest("POST", "/v1/bookings", bytes.NewReader(body))
	req = req.WithContext(contextWithUser(req.Context(), "user-1"))
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)

	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastCreate.GetUserId() != "user-1" {
		t.Errorf("UserId = %q, want user-1 (from auth context, not request body)", fake.lastCreate.GetUserId())
	}

	var out bookingJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != "b1" || out.PNR != "ABC123" {
		t.Errorf("response = %+v", out)
	}
}

// TestCreateBooking_NonTerminalStateIsNotAnError proves the gateway
// treats a booking left Held/Paid by the saga as a successful response
// (per the CreateBookingResponse.status field), not an HTTP error.
func TestCreateBooking_NonTerminalStateIsNotAnError(t *testing.T) {
	fake := &fakeBookingClient{
		createResp: &bookingv1.CreateBookingResponse{
			Booking: &bookingv1.Booking{Id: "b1", Status: bookingv1.Status_STATUS_HELD},
		},
	}
	h := NewBookingHandler(fake)
	body := []byte(`{"trip_id":"trip-1","seat_nos":["1"],"amount_paise":10000}`)
	req := httptest.NewRequest("POST", "/v1/bookings", bytes.NewReader(body))
	req = req.WithContext(contextWithUser(req.Context(), "user-1"))
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)

	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	var out bookingJSON
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Status != bookingv1.Status_STATUS_HELD.String() {
		t.Errorf("Status = %s, want %s", out.Status, bookingv1.Status_STATUS_HELD.String())
	}
}

func TestCreateBooking_UpstreamError(t *testing.T) {
	fake := &fakeBookingClient{createErr: status.Error(codes.FailedPrecondition, "seat unavailable")}
	h := NewBookingHandler(fake)
	body := []byte(`{"trip_id":"trip-1","seat_nos":["1"],"amount_paise":10000}`)
	req := httptest.NewRequest("POST", "/v1/bookings", bytes.NewReader(body))
	req = req.WithContext(contextWithUser(req.Context(), "user-1"))
	rec := httptest.NewRecorder()
	h.CreateBooking(rec, req)
	if rec.Code != 409 {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestGetBooking(t *testing.T) {
	fake := &fakeBookingClient{getResp: &bookingv1.GetBookingResponse{Booking: &bookingv1.Booking{Id: "b1"}}}
	h := NewBookingHandler(fake)
	req := httptest.NewRequest("GET", "/v1/bookings/b1", nil)
	req.SetPathValue("id", "b1")
	rec := httptest.NewRecorder()
	h.GetBooking(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestGetBooking_NotFound(t *testing.T) {
	fake := &fakeBookingClient{getErr: status.Error(codes.NotFound, "not found")}
	h := NewBookingHandler(fake)
	req := httptest.NewRequest("GET", "/v1/bookings/nope", nil)
	req.SetPathValue("id", "nope")
	rec := httptest.NewRecorder()
	h.GetBooking(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func contextWithUser(ctx context.Context, userID string) context.Context {
	return middleware.ContextWithUserID(ctx, userID)
}
