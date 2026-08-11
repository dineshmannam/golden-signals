// Command gateway is the public HTTP entrypoint. It performs no business logic
// of its own: it validates and forwards requests to the orders service and
// relays the response. It is the front door where the availability and latency
// SLOs are measured.
//
// It is fully instrumented: OTLP traces and metrics to the collector, a
// Prometheus /metrics endpoint, structured JSON logs, and the shared
// fault-injection layer wired into every call to orders.
//
// Configuration (environment):
//
//	PORT                   HTTP listen port (default 8080).
//	ORDERS_URL             base URL of the orders service (default http://localhost:8081).
//	OTEL_SERVICE_NAME      service name for telemetry (default "gateway").
//	OTEL_EXPORTER_OTLP_ENDPOINT   collector OTLP/gRPC endpoint (default localhost:4317).
//	LOG_LEVEL              debug|info|warn|error (default info).
//	FAULT_*                see package faultinject.
package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dineshmannam/golden-signals/internal/config"
	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/httpx"
	"github.com/dineshmannam/golden-signals/internal/telemetry"
)

const serviceName = "gateway"

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

	orders := newOrdersClient(config.String("ORDERS_URL", "http://localhost:8081"), inj)
	srv := &server{orders: orders}

	mux := http.NewServeMux()
	mux.HandleFunc("/checkout", srv.checkout)
	mux.HandleFunc("/orders/", srv.getOrder)

	health := &httpx.Health{Checks: map[string]httpx.Checker{
		"orders": func() error {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return orders.ping(c)
		},
	}}
	mux.Handle("/healthz", health.LivenessHandler())
	mux.Handle("/readyz", health.ReadinessHandler())
	mux.Handle("/metrics", telemetry.PrometheusHandler(providers.Registry))

	handler := httpx.Handler(mux, serviceName, inj, httpx.NewMetrics(providers.Registry, serviceName))
	addr := ":" + config.String("PORT", "8080")
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("gateway listening", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutCtx)
}

// server holds the gateway's dependencies.
type server struct {
	orders *ordersClient
}

// checkout forwards a POST /checkout to the orders service's POST /orders.
func (s *server) checkout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	scope := httpx.ScopeFromRequest(r)
	resp, err := s.orders.forward(r.Context(), scope, http.MethodPost, "/orders", r.Body)
	s.relay(w, r, resp, err)
}

// getOrder forwards GET /orders/{id} to the orders service unchanged.
func (s *server) getOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	scope := httpx.ScopeFromRequest(r)
	resp, err := s.orders.forward(r.Context(), scope, http.MethodGet, r.URL.Path, nil)
	s.relay(w, r, resp, err)
}

// relay copies an orders-service response back to the caller, translating
// transport-level failures into 502/504 so the availability SLO reflects them.
func (s *server) relay(w http.ResponseWriter, r *http.Request, resp *http.Response, err error) {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			http.Error(w, "orders timeout", http.StatusGatewayTimeout)
			return
		}
		telemetry.Logger().ErrorContext(r.Context(), "orders call failed", "error", err)
		http.Error(w, "orders unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
