// Package middleware holds the gRPC server interceptors every service
// wires into its grpc.NewServer call: request logging and RED metrics.
// Trace propagation is handled separately via otelgrpc.NewServerHandler
// (grpc.StatsHandler), the standard OTel integration, not a hand-rolled
// interceptor - see each service's cmd/<service>/main.go.
//
// There is deliberately no auth interceptor here: this architecture has
// no service identity/credential system (no mTLS, no service tokens), so
// a service-to-service "auth" interceptor here would just be boilerplate
// enforcing nothing. Gateway's HTTP-facing auth (internal/gateway/middleware)
// is the real boundary; internal gRPC calls are trusted network-level
// traffic, consistent with every service in deploy/compose sharing one
// Docker network.
package middleware

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// LoggingInterceptor logs method, status code, and duration for every
// unary RPC.
func LoggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logger.Info("grpc request",
			"method", info.FullMethod,
			"code", status.Code(err).String(),
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return resp, err
	}
}

// MetricsInterceptor records RED metrics (Rate via the counter's sample
// count, Errors via the code label, Duration via the histogram) for
// every unary RPC, labeled by method and status code.
func MetricsInterceptor(m *GRPCMetrics) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		code := status.Code(err)
		m.requestsTotal.WithLabelValues(info.FullMethod, code.String()).Inc()
		m.requestDuration.WithLabelValues(info.FullMethod, code.String()).Observe(time.Since(start).Seconds())
		return resp, err
	}
}
