package middleware

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// RegisterHealth registers the standard gRPC health service on server,
// immediately marked SERVING. Kubernetes' native gRPC probe type (stable
// since 1.24, see deploy/k8s/) checks this directly - no sidecar binary
// or extra HTTP endpoint needed.
func RegisterHealth(server *grpc.Server) {
	h := health.NewServer()
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, h)
}
