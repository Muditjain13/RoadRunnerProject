package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestHTTPMetrics_RecordsRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewHTTPMetrics(reg)

	mux := http.NewServeMux()
	mux.Handle("GET /v1/bookings/{id}", m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest(http.MethodGet, "/v1/bookings/abc123", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	metric := &dto.Metric{}
	if err := m.requestsTotal.WithLabelValues("GET /v1/bookings/{id}", "GET", "200").(prometheus.Metric).Write(metric); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if metric.GetCounter().GetValue() != 1 {
		t.Errorf("count = %v, want 1 (route pattern label, not literal path)", metric.GetCounter().GetValue())
	}
}
