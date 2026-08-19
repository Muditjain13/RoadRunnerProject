// Command search runs the search gRPC service: fan-out to trip providers
// (bus only in Phase 1) with a Redis result cache.
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
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	catalogv1 "github.com/Muditjain13/transport-app/api/proto/catalog/v1"
	searchv1 "github.com/Muditjain13/transport-app/api/proto/search/v1"
	"github.com/Muditjain13/transport-app/internal/search/handler"
	"github.com/Muditjain13/transport-app/internal/search/providers"
	"github.com/Muditjain13/transport-app/internal/search/repo"
	"github.com/Muditjain13/transport-app/internal/search/service"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("search exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	grpcAddr := envOr("SEARCH_GRPC_ADDR", ":8086")
	catalogAddr := envOr("CATALOG_GRPC_ADDR", "localhost:8081")
	redisAddr := envOr("SEARCH_REDIS_ADDR", "localhost:6379")
	metricsAddr := envOr("SEARCH_METRICS_ADDR", ":9106")
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "search", jaegerEndpoint)
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

	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer redisClient.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		// Redis is a cache, not a dependency search's own logic requires -
		// service.Search already tolerates Get/Set errors (see
		// internal/search/service/service.go) and just skips caching.
		// Refusing to start over an unreachable cache would be the exact
		// kind of brittleness this service exists to avoid.
		logger.Warn("redis unreachable at startup, continuing without cache", "addr", redisAddr, "error", err)
	}

	catalogConn, err := grpc.NewClient(catalogAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer catalogConn.Close()

	catalogClient := catalogv1.NewCatalogServiceClient(catalogConn)
	busProvider := providers.NewBusProvider(catalogClient)
	trainProvider := providers.NewTrainProvider(catalogClient)
	flightProvider := providers.NewFlightProvider(catalogClient)
	cache := repo.NewRedisCache(redisClient)
	svc := service.NewService([]service.Provider{busProvider, trainProvider, flightProvider}, cache)
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
	searchv1.RegisterSearchServiceServer(server, grpcHandler)
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
		logger.Info("search listening", "addr", grpcAddr, "metrics_addr", metricsAddr)
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
		logger.Info("shutting down search")
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
