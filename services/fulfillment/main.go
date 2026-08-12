// Command fulfillment is the asynchronous worker. It pull-subscribes to the
// OrderCreated events published by the orders service, marks each order
// fulfilled in Postgres, and emits the same class of RED metrics and structured
// logs as the synchronous services.
//
// Its reason for existing is the streaming-teaching piece: it continues the
// distributed trace across the Pub/Sub boundary, so a single trace ID spans
// gateway -> orders -> Pub/Sub -> fulfillment. Failed messages are retried and,
// after the subscription's maxDeliveryAttempts, routed to a dead-letter topic
// (configured in infra/terraform).
//
// It runs a small HTTP server too, but only for operational endpoints: liveness,
// readiness and the Prometheus /metrics scrape — not for business traffic.
//
// Configuration (environment):
//
//	PORT                   HTTP listen port for health/metrics (default 8082).
//	DATABASE_URL           Postgres DSN/URL (required).
//	PUBSUB_PROJECT_ID      GCP project for Pub/Sub (falls back to GOOGLE_CLOUD_PROJECT; required).
//	PUBSUB_SUBSCRIPTION_ORDER_CREATED   subscription to pull (default "order-created-fulfillment").
//	PUBSUB_EMULATOR_HOST   set by docker-compose to use the local emulator.
//	OTEL_SERVICE_NAME      service name for telemetry (default "fulfillment").
//	OTEL_EXPORTER_OTLP_ENDPOINT   collector OTLP/gRPC endpoint (default localhost:4317).
//	LOG_LEVEL              debug|info|warn|error (default info).
//	FAULT_*                see package faultinject (Request faults force processing
//	                       failures; Dependency faults slow the DB write).
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/dineshmannam/golden-signals/internal/config"
	"github.com/dineshmannam/golden-signals/internal/events"
	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/httpx"
	"github.com/dineshmannam/golden-signals/internal/pubsubx"
	"github.com/dineshmannam/golden-signals/internal/telemetry"
)

const serviceName = "fulfillment"

func main() {
	if err := run(); err != nil {
		telemetry.Logger().Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	providers, shutdown, err := telemetry.Setup(ctx, telemetry.Config{
		ServiceName:    config.String("OTEL_SERVICE_NAME", serviceName),
		ServiceVersion: config.String("SERVICE_VERSION", "dev"),
	})
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	log := telemetry.Logger()

	inj, err := faultinject.Load()
	if err != nil {
		log.Warn("fault injection config error; running without faults", "error", err)
	}
	log.Info("fault injection", "enabled", inj.Enabled())

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	store, err := NewStore(ctx, dsn, inj)
	if err != nil {
		return err
	}
	defer store.Close()

	projectID := config.String("PUBSUB_PROJECT_ID", config.String("GOOGLE_CLOUD_PROJECT", ""))
	if projectID == "" {
		return errors.New("PUBSUB_PROJECT_ID (or GOOGLE_CLOUD_PROJECT) is required")
	}
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return err
	}
	defer client.Close()

	subName := config.String("PUBSUB_SUBSCRIPTION_ORDER_CREATED", events.TopicOrderCreated+"-fulfillment")
	sub := client.Subscription(subName)

	metrics := pubsubx.NewMetrics(providers.Registry, serviceName)
	w := newWorker(sub, subName, store, inj, metrics)

	// Operational HTTP server: liveness, readiness and the Prometheus scrape.
	health := &httpx.Health{Checks: map[string]httpx.Checker{
		"postgres": func() error {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return store.Ping(c)
		},
	}}
	mux := http.NewServeMux()
	mux.Handle("/healthz", health.LivenessHandler())
	mux.Handle("/readyz", health.ReadinessHandler())
	mux.Handle("/metrics", telemetry.PrometheusHandler(providers.Registry))

	addr := ":" + config.String("PORT", "8082")
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("fulfillment health/metrics listening", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "error", err)
			stop()
		}
	}()

	// Run the subscriber until a signal cancels ctx, then it returns cleanly.
	errCh := make(chan error, 1)
	go func() { errCh <- w.Run(ctx) }()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			log.Error("subscriber stopped", "error", err)
			stop()
		}
	}

	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutCtx)
}
