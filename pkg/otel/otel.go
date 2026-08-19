// Package otel is the one place every service configures OpenTelemetry
// tracing, so "every service is observable" (CLAUDE.md section 2) means
// one shared setup call, not eight slightly-different copies.
package otel

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup configures a global TracerProvider exporting spans via OTLP/gRPC
// to jaegerEndpoint (Jaeger's OTLP gRPC port, :4317 in deploy/compose)
// and registers the W3C trace-context propagator so trace IDs flow across
// every gRPC and HTTP call between services. Returns a shutdown func the
// caller must run (flushes buffered spans) as part of graceful shutdown.
func Setup(ctx context.Context, serviceName, jaegerEndpoint string) (shutdown func(context.Context) error, err error) {
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(jaegerEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("otel: create exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, fmt.Errorf("otel: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}
