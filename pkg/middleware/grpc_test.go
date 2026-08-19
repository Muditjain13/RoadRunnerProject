package middleware

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLoggingInterceptor_PassesThroughResponse(t *testing.T) {
	interceptor := LoggingInterceptor(testLogger())
	handler := func(ctx context.Context, req any) (any, error) {
		return "response", nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

	resp, err := interceptor(context.Background(), "request", info, handler)
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
	if resp != "response" {
		t.Errorf("resp = %v, want response", resp)
	}
}

func TestLoggingInterceptor_PassesThroughError(t *testing.T) {
	wantErr := status.Error(codes.NotFound, "not found")
	interceptor := LoggingInterceptor(testLogger())
	handler := func(ctx context.Context, req any) (any, error) {
		return nil, wantErr
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

	_, err := interceptor(context.Background(), "request", info, handler)
	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
}

func TestMetricsInterceptor_RecordsSuccessAndError(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGRPCMetrics(reg)
	interceptor := MetricsInterceptor(m)
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

	okHandler := func(ctx context.Context, req any) (any, error) { return nil, nil }
	errHandler := func(ctx context.Context, req any) (any, error) { return nil, status.Error(codes.Internal, "boom") }

	if _, err := interceptor(context.Background(), nil, info, okHandler); err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
	if _, err := interceptor(context.Background(), nil, info, errHandler); err == nil {
		t.Fatal("expected error")
	}

	okCount := counterValue(t, m.requestsTotal, "/test.Service/Method", codes.OK.String())
	if okCount != 1 {
		t.Errorf("OK count = %v, want 1", okCount)
	}
	internalCount := counterValue(t, m.requestsTotal, "/test.Service/Method", codes.Internal.String())
	if internalCount != 1 {
		t.Errorf("Internal count = %v, want 1", internalCount)
	}
}

func TestMetricsInterceptor_RecordsDuration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewGRPCMetrics(reg)
	interceptor := MetricsInterceptor(m)
	info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

	handler := func(ctx context.Context, req any) (any, error) { return nil, nil }
	if _, err := interceptor(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("interceptor error = %v", err)
	}

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	var sampleCount uint64
	for _, mf := range mfs {
		if mf.GetName() != "grpc_server_request_duration_seconds" {
			continue
		}
		for _, mm := range mf.GetMetric() {
			sampleCount = mm.GetHistogram().GetSampleCount()
		}
	}
	if sampleCount != 1 {
		t.Errorf("duration histogram sample count = %d, want 1", sampleCount)
	}
}

func counterValue(t *testing.T, vec *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := vec.WithLabelValues(labels...).(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return m.GetCounter().GetValue()
}
