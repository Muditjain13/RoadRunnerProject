package middleware

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTPMetrics is gateway's HTTP-layer RED metric pair. Kept separate from
// pkg/middleware.GRPCMetrics because gateway is the only HTTP-facing
// service - this is not shared infrastructure.
type HTTPMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

func NewHTTPMetrics(reg prometheus.Registerer) *HTTPMetrics {
	m := &HTTPMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gateway_http_requests_total",
			Help: "Total HTTP requests, labeled by route pattern, method, and status code.",
		}, []string{"route", "method", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gateway_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, labeled by route pattern, method, and status code.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "method", "status"}),
	}
	reg.MustRegister(m.requestsTotal, m.requestDuration)
	return m
}

func (m *HTTPMetrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)

		// r.Pattern is the matched ServeMux pattern (e.g. "GET
		// /v1/bookings/{id}"), not the literal path - keeps the label
		// cardinality bounded regardless of how many distinct booking
		// IDs are requested.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(sw.status)
		m.requestsTotal.WithLabelValues(route, r.Method, status).Inc()
		m.requestDuration.WithLabelValues(route, r.Method, status).Observe(time.Since(start).Seconds())
	})
}
