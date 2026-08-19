// Command booking runs the booking saga orchestrator, its outbox relay,
// and its reconciliation sweeper.
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
	"google.golang.org/grpc/credentials/insecure"

	bookingv1 "github.com/Muditjain13/transport-app/api/proto/booking/v1"
	inventoryv1 "github.com/Muditjain13/transport-app/api/proto/inventory/v1"
	paymentv1 "github.com/Muditjain13/transport-app/api/proto/payment/v1"
	suppliersimv1 "github.com/Muditjain13/transport-app/api/proto/suppliersim/v1"
	"github.com/Muditjain13/transport-app/internal/booking/clients"
	"github.com/Muditjain13/transport-app/internal/booking/handler"
	"github.com/Muditjain13/transport-app/internal/booking/repo"
	"github.com/Muditjain13/transport-app/internal/booking/service"
	"github.com/Muditjain13/transport-app/pkg/circuitbreaker"
	"github.com/Muditjain13/transport-app/pkg/kafka"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

const (
	sweepInterval     = 5 * time.Second
	sweepStaleness    = 30 * time.Second
	sweepBatchSize    = 50
	outboxRelayPeriod = 2 * time.Second
	outboxRelayBatch  = 100
	outboxKafkaTopic  = "booking-events"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("booking exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	mysqlDSN := envOr("BOOKING_MYSQL_DSN", "root:root@tcp(localhost:3306)/booking?parseTime=true")
	grpcAddr := envOr("BOOKING_GRPC_ADDR", ":8085")
	metricsAddr := envOr("BOOKING_METRICS_ADDR", ":9105")
	inventoryAddr := envOr("INVENTORY_GRPC_ADDR", "localhost:8082")
	paymentAddr := envOr("PAYMENT_GRPC_ADDR", "localhost:8083")
	supplierSimAddr := envOr("SUPPLIER_SIM_GRPC_ADDR", "localhost:8084")
	kafkaBrokers := []string{envOr("KAFKA_BROKER", "localhost:9092")}
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "booking", jaegerEndpoint)
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

	inventoryConn, err := grpc.NewClient(inventoryAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer inventoryConn.Close()

	paymentConn, err := grpc.NewClient(paymentAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer paymentConn.Close()

	supplierSimConn, err := grpc.NewClient(supplierSimAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	)
	if err != nil {
		return err
	}
	defer supplierSimConn.Close()

	inventoryClient := clients.NewInventoryClient(inventoryv1.NewInventoryServiceClient(inventoryConn))
	paymentClient := clients.NewPaymentClient(paymentv1.NewPaymentServiceClient(paymentConn))
	busAdapter := clients.NewBusSupplierAdapter(suppliersimv1.NewSupplierSimServiceClient(supplierSimConn))
	trainAdapter := clients.NewTrainSupplierAdapter(suppliersimv1.NewSupplierSimServiceClient(supplierSimConn))
	// Same threshold/reset window search uses per provider - flight is
	// the mode `make chaos` exercises against a genuinely unreliable
	// dependency, so this is where the breaker actually gets tripped.
	flightBreaker := circuitbreaker.New(5, 30*time.Second)
	flightAdapter := clients.NewFlightSupplierAdapter(suppliersimv1.NewSupplierSimServiceClient(supplierSimConn), flightBreaker)

	adapters := map[service.Mode]service.SupplierAdapter{
		service.ModeBus:    busAdapter,
		service.ModeTrain:  trainAdapter,
		service.ModeFlight: flightAdapter,
	}

	mysqlRepo := repo.NewMySQLRepo(db)
	svc := service.NewService(mysqlRepo, inventoryClient, paymentClient, adapters, logger)

	producer := kafka.NewProducer(kafkaBrokers, outboxKafkaTopic)
	defer producer.Close()

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
	bookingv1.RegisterBookingServiceServer(server, grpcHandler)
	middleware.RegisterHealth(server)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		svc.RunReconciliationSweeper(ctx, sweepInterval, sweepStaleness, sweepBatchSize, logger)
	}()
	go func() {
		defer wg.Done()
		svc.RunOutboxRelay(ctx, outboxRelayPeriod, outboxRelayBatch, producer, logger)
	}()
	go func() {
		defer wg.Done()
		if err := middleware.ServeMetrics(ctx, metricsAddr); err != nil {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("booking listening", "addr", grpcAddr, "metrics_addr", metricsAddr)
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
		logger.Info("shutting down booking")
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
