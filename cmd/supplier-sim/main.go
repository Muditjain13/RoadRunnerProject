// Command supplier-sim runs the fake external supplier gRPC service (see
// internal/suppliersim). Chaos flags let `make chaos` demonstrate the
// booking saga degrading gracefully against an unreliable provider.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"
	"github.com/Muditjain13/transport-app/internal/suppliersim/handler"
	"github.com/Muditjain13/transport-app/internal/suppliersim/service"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	grpcAddr := flag.String("grpc-addr", envOr("SUPPLIER_SIM_GRPC_ADDR", ":8084"), "gRPC listen address")
	metricsAddr := flag.String("metrics-addr", envOr("SUPPLIER_SIM_METRICS_ADDR", ":9104"), "Prometheus metrics listen address")
	busErrorRate := flag.Float64("bus-error-rate", 0, "probability (0-1) that a bus supplier confirm is declined")
	busP99 := flag.Duration("bus-p99", 0, "added latency before a bus supplier confirm responds")
	trainErrorRate := flag.Float64("train-error-rate", 0, "probability (0-1) that a train supplier confirm is declined")
	trainP99 := flag.Duration("train-p99", 0, "added latency before a train supplier confirm responds")
	flightErrorRate := flag.Float64("flight-error-rate", 0, "probability (0-1) that a flight supplier confirm is declined")
	flightP99 := flag.Duration("flight-p99", 0, "added latency before a flight supplier confirm responds")
	flag.Parse()

	svc := service.NewService(
		service.WithChaos(service.ModeBus, service.Chaos{ErrorRate: *busErrorRate, Latency: *busP99}),
		service.WithChaos(service.ModeTrain, service.Chaos{ErrorRate: *trainErrorRate, Latency: *trainP99}),
		service.WithChaos(service.ModeFlight, service.Chaos{ErrorRate: *flightErrorRate, Latency: *flightP99}),
	)

	if err := run(logger, svc, *grpcAddr, *metricsAddr); err != nil {
		logger.Error("supplier-sim exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, svc *service.Service, grpcAddr, metricsAddr string) error {
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "supplier-sim", jaegerEndpoint)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Error("tracing shutdown failed", "error", err)
		}
	}()

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}

	metrics := middleware.NewGRPCMetrics(prometheus.DefaultRegisterer)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.LoggingInterceptor(logger), middleware.MetricsInterceptor(metrics)),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	suppliersimv1.RegisterSupplierSimServiceServer(server, handler.NewGRPCHandler(svc))
	middleware.RegisterHealth(server)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := middleware.ServeMetrics(ctx, metricsAddr); err != nil {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("supplier-sim listening", "addr", grpcAddr, "metrics_addr", metricsAddr)
		serveErr <- server.Serve(lis)
	}()

	select {
	case err := <-serveErr:
		stop()
		wg.Wait()
		if err != nil {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down supplier-sim")
		stopped := make(chan struct{})
		go func() {
			server.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			logger.Warn("graceful stop timed out, forcing")
			server.Stop()
		}
		wg.Wait()
	}
	return nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
