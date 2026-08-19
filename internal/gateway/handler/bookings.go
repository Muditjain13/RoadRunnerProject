package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	bookingv1 "github.com/Muditjain13/transport-app/api/proto/booking/v1"
	"github.com/Muditjain13/transport-app/internal/gateway/middleware"
)

type BookingHandler struct {
	client bookingv1.BookingServiceClient
}

func NewBookingHandler(client bookingv1.BookingServiceClient) *BookingHandler {
	return &BookingHandler{client: client}
}

type bookingJSON struct {
	ID            string   `json:"id"`
	PNR           string   `json:"pnr,omitempty"`
	SupplierPNR   string   `json:"supplier_pnr,omitempty"`
	UserID        string   `json:"user_id"`
	TripID        string   `json:"trip_id"`
	Mode          string   `json:"mode"`
	SeatNos       []string `json:"seat_nos"`
	AmountPaise   int64    `json:"amount_paise"`
	Status        string   `json:"status"`
	FailureReason string   `json:"failure_reason,omitempty"`
}

func bookingToJSON(b *bookingv1.Booking) bookingJSON {
	return bookingJSON{
		ID: b.GetId(), PNR: b.GetPnr(), SupplierPNR: b.GetSupplierPnr(),
		UserID: b.GetUserId(), TripID: b.GetTripId(), Mode: b.GetMode().String(),
		SeatNos: b.GetSeatNos(), AmountPaise: b.GetAmountPaise(),
		Status: b.GetStatus().String(), FailureReason: b.GetFailureReason(),
	}
}

type createBookingRequest struct {
	TripID      string   `json:"trip_id"`
	SeatNos     []string `json:"seat_nos"`
	AmountPaise int64    `json:"amount_paise"`
}

// CreateBooking handles POST /v1/bookings. This runs the entire saga
// synchronously (hold -> pay -> supplier confirm -> ticket issued) -
// there is no separate REST step for "hold" or "pay"; those are internal
// saga states, visible in the returned booking's status if the saga
// didn't fully complete (see internal/booking/service).
func (h *BookingHandler) CreateBooking(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}

	var req createBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.TripID == "" || len(req.SeatNos) == 0 || req.AmountPaise <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id, seat_nos, and amount_paise are required"})
		return
	}

	// CreateBooking only errors for genuine request failures (validation,
	// booking never created at all) - a booking left Held or Paid by a
	// recoverable saga hiccup comes back as a normal successful response,
	// see internal/booking/handler.
	resp, err := h.client.CreateBooking(r.Context(), &bookingv1.CreateBookingRequest{
		UserId:      userID,
		TripId:      req.TripID,
		SeatNos:     req.SeatNos,
		Mode:        bookingv1.Mode_MODE_BUS,
		AmountPaise: req.AmountPaise,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, bookingToJSON(resp.GetBooking()))
}

type createTrainBookingRequest struct {
	TripID      string `json:"trip_id"`
	ClassCode   string `json:"class_code"`
	SeatCount   int32  `json:"seat_count"`
	AmountPaise int64  `json:"amount_paise"`
}

type trainBookingResultJSON struct {
	Outcome         string       `json:"outcome"` // "booked" or "waitlisted"
	Booking         *bookingJSON `json:"booking,omitempty"`
	WaitlistEntryID int64        `json:"waitlist_entry_id,omitempty"`
}

// CreateTrainBooking handles POST /v1/bookings/train. Unlike bus's
// explicit seat picker, the caller names a class and a seat count -
// inventory picks the actual seats (or queues a waitlist entry if the
// class is exhausted right now, which is a normal outcome here, not an
// error).
func (h *BookingHandler) CreateTrainBooking(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}

	var req createTrainBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.TripID == "" || req.ClassCode == "" || req.SeatCount <= 0 || req.AmountPaise <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id, class_code, seat_count, and amount_paise are required"})
		return
	}

	resp, err := h.client.CreateTrainBooking(r.Context(), &bookingv1.CreateTrainBookingRequest{
		UserId:      userID,
		TripId:      req.TripID,
		ClassCode:   req.ClassCode,
		SeatCount:   req.SeatCount,
		AmountPaise: req.AmountPaise,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	if resp.GetBooking() != nil {
		b := bookingToJSON(resp.GetBooking())
		writeJSON(w, http.StatusCreated, trainBookingResultJSON{Outcome: "booked", Booking: &b})
		return
	}
	writeJSON(w, http.StatusCreated, trainBookingResultJSON{Outcome: "waitlisted", WaitlistEntryID: resp.GetWaitlistEntryId()})
}

type createFlightBookingRequest struct {
	TripID      string `json:"trip_id"`
	SeatCount   int32  `json:"seat_count"`
	AmountPaise int64  `json:"amount_paise"`
}

// CreateFlightBooking handles POST /v1/bookings/flight. Like train,
// the caller names a passenger count, not explicit seats - flight has
// none under our control. Unlike train, there is no waitlist outcome:
// the supplier's external hold either succeeds or it doesn't,
// synchronously, so this always returns a booking (or an error).
func (h *BookingHandler) CreateFlightBooking(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}

	var req createFlightBookingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.TripID == "" || req.SeatCount <= 0 || req.AmountPaise <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id, seat_count, and amount_paise are required"})
		return
	}

	resp, err := h.client.CreateFlightBooking(r.Context(), &bookingv1.CreateFlightBookingRequest{
		UserId:      userID,
		TripId:      req.TripID,
		SeatCount:   req.SeatCount,
		AmountPaise: req.AmountPaise,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, bookingToJSON(resp.GetBooking()))
}

type claimWaitlistRequest struct {
	TripID      string `json:"trip_id"`
	AmountPaise int64  `json:"amount_paise"`
}

// ClaimWaitlistEntry handles POST /v1/waitlist/{id}/claim - "pay now" on a
// promoted waitlist entry. Fails with a conflict if the entry isn't
// promoted yet or belongs to someone else; safe to retry, since booking's
// CreateBookingFromWaitlist is idempotent by hold id.
func (h *BookingHandler) ClaimWaitlistEntry(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid waitlist entry id"})
		return
	}

	var req claimWaitlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.TripID == "" || req.AmountPaise <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip_id and amount_paise are required"})
		return
	}

	resp, err := h.client.CreateBookingFromWaitlist(r.Context(), &bookingv1.CreateBookingFromWaitlistRequest{
		WaitlistEntryId: id,
		UserId:          userID,
		TripId:          req.TripID,
		AmountPaise:     req.AmountPaise,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, bookingToJSON(resp.GetBooking()))
}

// GetBooking handles GET /v1/bookings/{id}.
func (h *BookingHandler) GetBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	resp, err := h.client.GetBooking(r.Context(), &bookingv1.GetBookingRequest{BookingId: id})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bookingToJSON(resp.GetBooking()))
}

// CancelBooking handles POST /v1/bookings/{id}/cancel.
func (h *BookingHandler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	resp, err := h.client.CancelBooking(r.Context(), &bookingv1.CancelBookingRequest{BookingId: id})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, bookingToJSON(resp.GetBooking()))
}
