package handler

import (
	"net/http"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	searchv1 "github.com/Muditjain13/transport-app/api/proto/search/v1"
)

type SearchHandler struct {
	client searchv1.SearchServiceClient
}

func NewSearchHandler(client searchv1.SearchServiceClient) *SearchHandler {
	return &SearchHandler{client: client}
}

type tripClassJSON struct {
	Code      string `json:"code"`
	FarePaise int64  `json:"fare_paise"`
	Capacity  int32  `json:"capacity"`
}

type tripJSON struct {
	ID            string          `json:"id"`
	RouteID       string          `json:"route_id"`
	OperatorID    string          `json:"operator_id"`
	OperatorName  string          `json:"operator_name"`
	Mode          string          `json:"mode"`
	VehicleNumber string          `json:"vehicle_number"`
	DepartureAt   time.Time       `json:"departure_at"`
	ArrivalAt     time.Time       `json:"arrival_at"`
	Classes       []tripClassJSON `json:"classes"`
}

type searchResponseJSON struct {
	Trips             []tripJSON `json:"trips"`
	DegradedProviders []string   `json:"degraded_providers,omitempty"`
}

// Search handles GET /v1/search?origin=DEL&dest=BOM&date=2026-03-01[&mode=train].
// mode is optional; omitted (or "any") means every mode, exactly as
// before this parameter existed.
func (h *SearchHandler) Search(w http.ResponseWriter, r *http.Request) {
	origin := r.URL.Query().Get("origin")
	dest := r.URL.Query().Get("dest")
	dateStr := r.URL.Query().Get("date")
	if origin == "" || dest == "" || dateStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "origin, dest, and date are required"})
		return
	}
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "date must be YYYY-MM-DD"})
		return
	}

	var modes []searchv1.Mode
	switch r.URL.Query().Get("mode") {
	case "", "any":
		// no filter - every provider
	case "bus":
		modes = []searchv1.Mode{searchv1.Mode_MODE_BUS}
	case "train":
		modes = []searchv1.Mode{searchv1.Mode_MODE_TRAIN}
	case "flight":
		modes = []searchv1.Mode{searchv1.Mode_MODE_FLIGHT}
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be one of: any, bus, train, flight"})
		return
	}

	resp, err := h.client.SearchTrips(r.Context(), &searchv1.SearchTripsRequest{
		OriginCode: origin,
		DestCode:   dest,
		Date:       timestamppb.New(date),
		Modes:      modes,
	})
	if err != nil {
		writeError(w, err)
		return
	}

	out := searchResponseJSON{Trips: make([]tripJSON, 0, len(resp.GetTrips()))}
	for _, t := range resp.GetTrips() {
		out.Trips = append(out.Trips, tripToJSON(t))
	}
	for _, m := range resp.GetDegradedProviders() {
		out.DegradedProviders = append(out.DegradedProviders, m.String())
	}
	writeJSON(w, http.StatusOK, out)
}

func tripToJSON(t *searchv1.Trip) tripJSON {
	classes := make([]tripClassJSON, 0, len(t.GetClasses()))
	for _, c := range t.GetClasses() {
		classes = append(classes, tripClassJSON{Code: c.GetCode(), FarePaise: c.GetFarePaise(), Capacity: c.GetCapacity()})
	}
	return tripJSON{
		ID:            t.GetId(),
		RouteID:       t.GetRouteId(),
		OperatorID:    t.GetOperatorId(),
		OperatorName:  t.GetOperatorName(),
		Mode:          t.GetMode().String(),
		VehicleNumber: t.GetVehicleNumber(),
		DepartureAt:   t.GetDepartureAt().AsTime(),
		ArrivalAt:     t.GetArrivalAt().AsTime(),
		Classes:       classes,
	}
}
