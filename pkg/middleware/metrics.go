package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// GRPCMetrics is the RED metric pair every gRPC service registers once
// and passes to MetricsInterceptor.
type GRPCMetrics struct {
	requestsTotal   *prometheus.CounterVec
	requestDuration *prometheus.HistogramVec
}

// NewGRPCMetrics registers the metrics against reg (use
// prometheus.DefaultRegisterer in normal operation; a fresh registry in
// tests to avoid cross-test collisions).
func NewGRPCMetrics(reg prometheus.Registerer) *GRPCMetrics {
	m := &GRPCMetrics{
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "grpc_server_requests_total",
			Help: "Total gRPC requests, labeled by method and status code.",
		}, []string{"method", "code"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "grpc_server_request_duration_seconds",
			Help:    "gRPC request duration in seconds, labeled by method and status code.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "code"}),
	}
	reg.MustRegister(m.requestsTotal, m.requestDuration)
	return m
}

// ServeMetrics starts a minimal HTTP server exposing /metrics on addr
// until ctx is canceled. Every service runs one of these alongside its
// gRPC (or HTTP) server - this is the Prometheus scrape target referenced
// in deploy/compose/prometheus.yml.
func ServeMetrics(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}
