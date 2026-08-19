// Command notification runs the Kafka consumer that fans out on booking
// lifecycle events published by booking's outbox relay.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/Muditjain13/transport-app/internal/notification/service"
	"github.com/Muditjain13/transport-app/pkg/middleware"
	otelpkg "github.com/Muditjain13/transport-app/pkg/otel"
)

const (
	kafkaTopic   = "booking-events"
	kafkaGroupID = "notification"
	workerCount  = 8
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	brokers := []string{envOr("KAFKA_BROKER", "localhost:9092")}
	metricsAddr := envOr("NOTIFICATION_METRICS_ADDR", ":9107")
	jaegerEndpoint := envOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	workers := workerCount
	if v := os.Getenv("NOTIFICATION_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := otelpkg.Setup(ctx, "notification", jaegerEndpoint)
	if err != nil {
		logger.Error("notification exited with error", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Error("tracing shutdown failed", "error", err)
		}
	}()

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: brokers,
		Topic:   kafkaTopic,
		GroupID: kafkaGroupID,
	})
	defer reader.Close()

	metrics := service.NewMetrics(prometheus.DefaultRegisterer)
	svc := service.NewService(service.NewLogNotifier(logger), metrics, logger)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := middleware.ServeMetrics(ctx, metricsAddr); err != nil {
			logger.Error("metrics server failed", "error", err)
		}
	}()

	logger.Info("notification consuming", "topic", kafkaTopic, "group_id", kafkaGroupID, "workers", workers, "metrics_addr", metricsAddr)
	runErr := svc.Run(ctx, reader, workers)
	stop()
	wg.Wait()
	if runErr != nil {
		logger.Error("notification exited with error", "error", runErr)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
