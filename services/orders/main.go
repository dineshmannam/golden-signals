// Command orders is the business-logic service. It owns the orders table in
// Cloud SQL (Postgres) and exposes a small JSON API consumed by the gateway.
//
// It is fully instrumented: OTLP traces and metrics to the collector, a
// Prometheus /metrics endpoint, structured JSON logs, and the shared
// fault-injection layer wired into every database call.
//
// Configuration (environment):
//
//	PORT                   HTTP listen port (default 8081).
//	DATABASE_URL           Postgres DSN/URL (required), e.g.
//	                       postgres://user:pass@host:5432/orders?sslmode=disable
//	OTEL_SERVICE_NAME      service name for telemetry (default "orders").
//	OTEL_EXPORTER_OTLP_ENDPOINT   collector OTLP/gRPC endpoint (default localhost:4317).
//	LOG_LEVEL              debug|info|warn|error (default info).
//	FAULT_*                see package faultinject.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dineshmannam/golden-signals/internal/config"
	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/httpx"
	"github.com/dineshmannam/golden-signals/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const serviceName = "orders"

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
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	srv, err := newServer(store, inj)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/orders", srv)
	mux.Handle("/orders/", srv)

	health := &httpx.Health{Checks: map[string]httpx.Checker{
		"postgres": func() error {
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			return store.Ping(c)
		},
	}}
	mux.Handle("/healthz", health.LivenessHandler())
	mux.Handle("/readyz", health.ReadinessHandler())
	mux.Handle("/metrics", telemetry.PrometheusHandler(providers.Registry))

	handler := httpx.Handler(mux, serviceName, inj, httpx.NewMetrics(providers.Registry, serviceName))
	addr := ":" + config.String("PORT", "8081")
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("orders listening", "addr", addr)
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

// storage is the persistence behaviour the API depends on. *Store is the
// Postgres implementation; tests supply a fake so the handlers can be exercised
// without a database.
type storage interface {
	Create(ctx context.Context, scope faultinject.Scope, o Order) (Order, error)
	Get(ctx context.Context, scope faultinject.Scope, id int64) (Order, error)
}

// server implements the orders JSON API. It routes /orders and /orders/{id}.
type server struct {
	store   storage
	inj     *faultinject.Injector
	created metric.Int64Counter
}

func newServer(store storage, inj *faultinject.Injector) (*server, error) {
	meter := otel.Meter("github.com/dineshmannam/golden-signals/services/orders")
	created, err := meter.Int64Counter("orders.created",
		metric.WithDescription("Number of orders created"),
		metric.WithUnit("{order}"),
	)
	if err != nil {
		return nil, err
	}
	return &server{store: store, inj: inj, created: created}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /orders            -> POST create
	// /orders/{id}       -> GET fetch
	id, hasID := parseID(r.URL.Path)
	switch {
	case r.Method == http.MethodPost && !hasID:
		s.create(w, r)
	case r.Method == http.MethodGet && hasID:
		s.get(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// createRequest is the JSON body for creating an order.
type createRequest struct {
	Item     string `json:"item"`
	Quantity int    `json:"quantity"`
}

func (s *server) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Item == "" || req.Quantity <= 0 {
		writeError(w, http.StatusBadRequest, "item and positive quantity are required")
		return
	}
	scope := httpx.ScopeFromRequest(r)
	order, err := s.store.Create(r.Context(), scope, Order{
		Item:     req.Item,
		Quantity: req.Quantity,
		Cohort:   scope.Cohort,
		Region:   scope.Region,
	})
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.created.Add(r.Context(), 1, metric.WithAttributes(
		attribute.String("cohort", scope.Cohort),
		attribute.String("region", scope.Region),
	))
	writeJSON(w, http.StatusCreated, order)
}

func (s *server) get(w http.ResponseWriter, r *http.Request, id int64) {
	order, err := s.store.Get(r.Context(), httpx.ScopeFromRequest(r), id)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "order not found")
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *server) writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	// A cancelled context is the fault-injection dependency slowness firing past
	// the client deadline; report it as a gateway timeout rather than a 500.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusGatewayTimeout, "dependency timeout")
		return
	}
	telemetry.Logger().ErrorContext(r.Context(), "store error", "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

// parseID extracts a numeric id from /orders/{id}. Returns (0, false) for the
// collection path /orders.
func parseID(path string) (int64, bool) {
	rest := strings.TrimPrefix(path, "/orders")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
