// Command inventory runs the inventory gRPC service and its hold-expiry
// sweeper (see docs/adr/0001-hold-expiry-sweeper.md).
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
	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	"github.com/Muditjain13/transport-app/internal/inventory/handler"
	"github.com/Muditjain13/transport-app/internal/inventory/repo"
	"github.com/Muditjain13/transport-app/internal/inventory/service"
	"github.com/Muditjain13/transport-app/pkg/kafka"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

const (
	sweepInterval        = 5 * time.Second
	sweepBatchSize       = 100
	outboxRelayPeriod    = 2 * time.Second
	outboxRelayBatch     = 100
	seatsFreedKafkaTopic = "inventory-seats-freed"
	promotionGroupID     = "inventory-promotion"
	promotionWorkers     = 4
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("inventory exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	mysqlDSN := envOr("INVENTORY_MYSQL_DSN", "root:root@tcp(localhost:3306)/inventory?parseTime=true")
	grpcAddr := envOr("INVENTORY_GRPC_ADDR", ":8082")
	metricsAddr := envOr("INVENTORY_METRICS_ADDR", ":9102")
	kafkaBrokers := []string{envOr("KAFKA_BROKER", "localhost:9092")}
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "inventory", jaegerEndpoint)
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

	producer := kafka.NewProducer(kafkaBrokers, seatsFreedKafkaTopic)
	defer producer.Close()

	// Kafka may not be reachable in every environment this runs in (see
	// cmd/booking's identical pattern) - the reader is lazy, so this never
	// blocks startup. tryPromoteAfterFree (in service.go) already promotes
	// synchronously on the happy path; this consumer is the durable retry
	// path if that attempt's process crashed or the event was otherwise
	// missed.
	promotionReader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: kafkaBrokers,
		Topic:   seatsFreedKafkaTopic,
		GroupID: promotionGroupID,
	})
	defer promotionReader.Close()

	lis, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		return err
	}

	metrics := middleware.NewGRPCMetrics(prometheus.DefaultRegisterer)
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.LoggingInterceptor(logger), middleware.MetricsInterceptor(metrics)),
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
	)
	inventoryv1.RegisterInventoryServiceServer(server, grpcHandler)
	middleware.RegisterHealth(server)

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		svc.RunSweeper(ctx, sweepInterval, sweepBatchSize, logger)
	}()
	go func() {
		defer wg.Done()
		svc.RunOutboxRelay(ctx, outboxRelayPeriod, outboxRelayBatch, producer, logger)
	}()
	go func() {
		defer wg.Done()
		if err := svc.RunPromotionConsumer(ctx, promotionReader, promotionWorkers, logger); err != nil {
			logger.Error("promotion consumer failed", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := middleware.ServeMetrics(ctx, metricsAddr); err != nil {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("inventory listening", "addr", grpcAddr)
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
		logger.Info("shutting down inventory")
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
