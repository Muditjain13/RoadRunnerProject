// Command payment runs the payment gRPC service.
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	paymentv1 "github.com/Muditjain13/transport-app/api/proto/payment/v1"
	"github.com/Muditjain13/transport-app/internal/payment/handler"
	"github.com/Muditjain13/transport-app/internal/payment/repo"
	"github.com/Muditjain13/transport-app/internal/payment/service"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("payment exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	mysqlDSN := envOr("PAYMENT_MYSQL_DSN", "root:root@tcp(localhost:3306)/payment?parseTime=true")
	grpcAddr := envOr("PAYMENT_GRPC_ADDR", ":8083")
	metricsAddr := envOr("PAYMENT_METRICS_ADDR", ":9103")
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "payment", jaegerEndpoint)
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

	db, err := sql.Open("mysql", mysqlDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return err
	}

	mysqlRepo := repo.NewMySQLRepo(db)
	svc := service.NewService(mysqlRepo)
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
	paymentv1.RegisterPaymentServiceServer(server, grpcHandler)
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
		logger.Info("payment listening", "addr", grpcAddr, "metrics_addr", metricsAddr)
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
		logger.Info("shutting down payment")
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
