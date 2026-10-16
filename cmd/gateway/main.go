// Command gateway runs the public REST API: auth, rate limiting, and
// translation to gRPC calls into search and booking.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bookingv1 "github.com/Muditjain13/transport-app/api/proto/booking/v1"
	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	searchv1 "github.com/Muditjain13/transport-app/api/proto/search/v1"
	"github.com/Muditjain13/transport-app/internal/gateway/handler"
	"github.com/Muditjain13/transport-app/internal/gateway/middleware"
	pkgmiddleware "github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("gateway exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	httpAddr := envOr("GATEWAY_HTTP_ADDR", ":8080")
	metricsAddr := envOr("GATEWAY_METRICS_ADDR", ":9100")
	searchAddr := envOr("SEARCH_GRPC_ADDR", "localhost:8086")
	bookingAddr := envOr("BOOKING_GRPC_ADDR", "localhost:8085")
	inventoryAddr := envOr("INVENTORY_GRPC_ADDR", "localhost:8082")
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "gateway", jaegerEndpoint)
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

	searchConn, err := grpc.NewClient(searchAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer searchConn.Close()

	bookingConn, err := grpc.NewClient(bookingAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer bookingConn.Close()

	inventoryConn, err := grpc.NewClient(inventoryAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer inventoryConn.Close()

	searchHandler := handler.NewSearchHandler(searchv1.NewSearchServiceClient(searchConn))
	bookingHandler := handler.NewBookingHandler(bookingv1.NewBookingServiceClient(bookingConn))
	seatsHandler := handler.NewSeatsHandler(inventoryv1.NewInventoryServiceClient(inventoryConn))
	waitlistHandler := handler.NewWaitlistHandler(inventoryv1.NewInventoryServiceClient(inventoryConn))
	rateLimiter, err := newRateLimiter()
	if err != nil {
		return err
	}
	httpMetrics := middleware.NewHTTPMetrics(prometheus.DefaultRegisterer)

	router := handler.NewRouter(searchHandler, bookingHandler, seatsHandler, waitlistHandler, rateLimiter, httpMetrics, logger)

	server := &http.Server{
		Addr:              httpAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := pkgmiddleware.ServeMetrics(ctx, metricsAddr); err != nil {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", "addr", httpAddr, "metrics_addr", metricsAddr)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		stop()
		wg.Wait()
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down gateway")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
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

// newRateLimiter builds the per-client limiter from the environment. Defaults
// match the original fixed behavior: 10 req/s sustained with a burst of 20, no
// extra write limit, X-Forwarded-For ignored.
//
//	GATEWAY_RATE_RPS, GATEWAY_RATE_BURST          general limit per client
//	GATEWAY_WRITE_RATE_RPS, GATEWAY_WRITE_RATE_BURST
//	                                              extra limit for POST and other
//	                                              mutating requests (0 = off)
//	GATEWAY_TRUSTED_PROXIES                       comma-separated CIDRs of
//	                                              reverse proxies whose
//	                                              X-Forwarded-For is believed
func newRateLimiter() (*middleware.RateLimiter, error) {
	rl := middleware.NewRateLimiter(envFloat("GATEWAY_RATE_RPS", 10), envInt("GATEWAY_RATE_BURST", 20))
	if rps := envFloat("GATEWAY_WRITE_RATE_RPS", 0); rps > 0 {
		rl = rl.WithWriteLimit(rps, envInt("GATEWAY_WRITE_RATE_BURST", 5))
	}
	if proxies := envOr("GATEWAY_TRUSTED_PROXIES", ""); proxies != "" {
		var err error
		if rl, err = rl.WithTrustedProxies(strings.Split(proxies, ",")); err != nil {
			return nil, err
		}
	}
	return rl, nil
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(envOr(key, ""), 64); err == nil {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(envOr(key, "")); err == nil {
		return v
	}
	return fallback
}
