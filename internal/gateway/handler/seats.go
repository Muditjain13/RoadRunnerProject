package handler

import (
	"net/http"

	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
)

type SeatsHandler struct {
	client inventoryv1.InventoryServiceClient
}

func NewSeatsHandler(client inventoryv1.InventoryServiceClient) *SeatsHandler {
	return &SeatsHandler{client: client}
}

// ListAvailableSeats handles GET /v1/trips/{tripId}/seats?class=seater. A
// snapshot, not a reservation - the actual claim happens via
// POST /v1/bookings, which is what really enforces availability.
func (h *SeatsHandler) ListAvailableSeats(w http.ResponseWriter, r *http.Request) {
	tripID := r.PathValue("tripId")
	class := r.URL.Query().Get("class")
	if tripID == "" || class == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "trip id and class query param are required"})
		return
	}

	resp, err := h.client.ListAvailableSeats(r.Context(), &inventoryv1.ListAvailableSeatsRequest{
		TripId:    tripID,
		ClassCode: class,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seat_nos": resp.GetSeatNos()})
}
