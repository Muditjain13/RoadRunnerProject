package otel

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

// TestSetup_Succeeds proves Setup succeeds and installs a global
// TracerProvider without needing a real Jaeger collector running: the
// OTLP gRPC exporter dials lazily, so construction never blocks on
// connectivity - only actually exporting spans would.
func TestSetup_Succeeds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := Setup(ctx, "test-service", "localhost:4317")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	}()

	if otel.GetTracerProvider() == nil {
		t.Error("global TracerProvider was not set")
	}
}
