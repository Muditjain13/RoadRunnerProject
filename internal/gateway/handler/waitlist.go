package handler

import (
	"net/http"

	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	"github.com/Muditjain13/transport-app/internal/gateway/middleware"
)

type WaitlistHandler struct {
	client inventoryv1.InventoryServiceClient
}

func NewWaitlistHandler(client inventoryv1.InventoryServiceClient) *WaitlistHandler {
	return &WaitlistHandler{client: client}
}

type waitlistEntryJSON struct {
	ID             int64  `json:"id"`
	TripID         string `json:"trip_id"`
	ClassCode      string `json:"class_code"`
	SeatCount      int32  `json:"seat_count"`
	AmountPaise    int64  `json:"amount_paise"`
	Status         string `json:"status"`
	PromotedHoldID string `json:"promoted_hold_id,omitempty"`
}

func waitlistEntryToJSON(e *inventoryv1.WaitlistEntry) waitlistEntryJSON {
	return waitlistEntryJSON{
		ID:             e.GetId(),
		TripID:         e.GetTripId(),
		ClassCode:      e.GetClassCode(),
		SeatCount:      e.GetSeatCount(),
		AmountPaise:    e.GetAmountPaise(),
		Status:         e.GetStatus().String(),
		PromotedHoldID: e.GetPromotedHoldId(),
	}
}

// ListMyWaitlist handles GET /v1/waitlist - goes straight to inventory
// (booking has no concept of a waitlist entry until one is claimed into a
// booking), listing every entry for the signed-in user, newest first.
func (h *WaitlistHandler) ListMyWaitlist(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}

	resp, err := h.client.ListWaitlistByUser(r.Context(), &inventoryv1.ListWaitlistByUserRequest{UserId: userID})
	if err != nil {
		writeError(w, err)
		return
	}
	entries := make([]waitlistEntryJSON, 0, len(resp.GetEntries()))
	for _, e := range resp.GetEntries() {
		entries = append(entries, waitlistEntryToJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
