// Command catalog runs the catalog gRPC service.
package main

import (
	"context"
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

	catalogv1 "github.com/Muditjain13/transport-app/api/proto/catalog/v1"
	"github.com/Muditjain13/transport-app/internal/catalog/handler"
	"github.com/Muditjain13/transport-app/internal/catalog/repo"
	"github.com/Muditjain13/transport-app/internal/catalog/service"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("catalog exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	mongoURI := envOr("CATALOG_MONGO_URI", "mongodb://localhost:27017")
	grpcAddr := envOr("CATALOG_GRPC_ADDR", ":8081")
	metricsAddr := envOr("CATALOG_METRICS_ADDR", ":9101")
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "catalog", jaegerEndpoint)
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

	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(shutdownCtx); err != nil {
			logger.Error("mongo disconnect failed", "error", err)
		}
	}()
	if err := client.Ping(connectCtx, nil); err != nil {
		return err
	}

	repository := repo.NewMongoRepo(client.Database("catalog"))
	if err := repository.EnsureIndexes(connectCtx); err != nil {
		return err
	}

	svc := service.NewService(repository)
	grpcHandler := handler.NewGRPCHandler(svc)

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}

	metrics := middleware.NewGRPCMetrics(prometheus.DefaultRegisterer)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.LoggingInterceptor(logger), middleware.MetricsInterceptor(metrics)),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	catalogv1.RegisterCatalogServiceServer(server, grpcHandler)
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
		logger.Info("catalog listening", "addr", grpcAddr, "metrics_addr", metricsAddr)
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
		logger.Info("shutting down catalog")
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
