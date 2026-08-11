// Package telemetry wires up OpenTelemetry (traces + metrics) and structured
// JSON logging for the golden-signals services.
//
// The design goal is a single Setup call that a service's main can defer the
// shutdown of, after which the standard OTel global providers are configured:
//
//   - Traces are exported over OTLP/gRPC to a collector (Config.OTLPEndpoint).
//   - Metrics are exported two ways at once from one MeterProvider:
//     an OTLP/gRPC push to the same collector, and a Prometheus pull endpoint
//     (see PrometheusHandler) so the service is directly scrapable.
//   - Logs are emitted as structured JSON via slog, with the active trace and
//     span IDs injected so logs can be correlated with traces (see logging.go).
//
// Everything is configured from the environment (see Config and the standard
// OTEL_* variables honoured by the exporters) so behaviour can change per
// deployment without a rebuild.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// Config controls telemetry setup. Zero values fall back to sensible defaults
// or the corresponding environment variable.
type Config struct {
	// ServiceName is the logical service name reported to all backends. Falls
	// back to OTEL_SERVICE_NAME then "unknown-service".
	ServiceName string
	// ServiceVersion is reported as the service.version resource attribute.
	ServiceVersion string
	// OTLPEndpoint is the collector's OTLP/gRPC endpoint, host:port. Falls back
	// to OTEL_EXPORTER_OTLP_ENDPOINT then "localhost:4317".
	OTLPEndpoint string
	// Insecure disables transport security on the OTLP connection. In-cluster
	// traffic to a local collector is typically plaintext, so this defaults to
	// true unless OTEL_EXPORTER_OTLP_INSECURE is set to "false".
	Insecure bool
	// MetricInterval is how often metrics are pushed over OTLP. Defaults to 30s.
	MetricInterval time.Duration
	// PrometheusRegistry receives the Prometheus collectors. If nil a new
	// registry is created; retrieve the handler with PrometheusHandler.
	PrometheusRegistry *prometheus.Registry
}

// Providers holds the configured SDK providers and the Prometheus registry so
// the caller can expose a /metrics endpoint. Call Shutdown to flush and close.
type Providers struct {
	Tracer   *sdktrace.TracerProvider
	Meter    *sdkmetric.MeterProvider
	Registry *prometheus.Registry
}

// Setup configures the global OTel providers, the global logger (slog), and
// returns Providers plus a shutdown function. The shutdown function flushes and
// closes exporters; call it (e.g. via defer) before the process exits.
func Setup(ctx context.Context, cfg Config) (*Providers, func(context.Context) error, error) {
	cfg = withDefaults(cfg)

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
		resource.WithFromEnv(), // honour OTEL_RESOURCE_ATTRIBUTES
		resource.WithHost(),
		resource.WithProcessRuntimeName(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: building resource: %w", err)
	}

	// --- Traces: OTLP/gRPC exporter -> batch span processor. ---
	traceExp, err := otlptracegrpc.New(ctx, traceGRPCOptions(cfg)...)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: otlp trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler()),
	)

	// --- Metrics: one MeterProvider, two readers (OTLP push + Prometheus pull). ---
	metricExp, err := otlpmetricgrpc.New(ctx, metricGRPCOptions(cfg)...)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: otlp metric exporter: %w", err)
	}
	promReader, err := promexporter.New(promexporter.WithRegisterer(cfg.PrometheusRegistry))
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: prometheus exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(cfg.MetricInterval))),
		sdkmetric.WithReader(promReader),
	)

	// Install as globals so instrumentation libraries pick them up.
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		Logger().Warn("otel error", "error", err)
	}))

	SetupLogging(cfg.ServiceName)

	providers := &Providers{Tracer: tp, Meter: mp, Registry: cfg.PrometheusRegistry}

	shutdown := func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}
	return providers, shutdown, nil
}

func withDefaults(cfg Config) Config {
	if cfg.ServiceName == "" {
		if v := os.Getenv("OTEL_SERVICE_NAME"); v != "" {
			cfg.ServiceName = v
		} else {
			cfg.ServiceName = "unknown-service"
		}
	}
	if cfg.OTLPEndpoint == "" {
		if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
			cfg.OTLPEndpoint = v
		} else {
			cfg.OTLPEndpoint = "localhost:4317"
		}
	}
	// Default to plaintext unless explicitly told otherwise.
	cfg.Insecure = os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") != "false"
	if cfg.MetricInterval == 0 {
		cfg.MetricInterval = 30 * time.Second
	}
	if cfg.PrometheusRegistry == nil {
		cfg.PrometheusRegistry = prometheus.NewRegistry()
	}
	return cfg
}

func traceGRPCOptions(cfg Config) []otlptracegrpc.Option {
	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	return opts
}

func metricGRPCOptions(cfg Config) []otlpmetricgrpc.Option {
	opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.Insecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	}
	return opts
}

// sampler honours OTEL_TRACES_SAMPLER=parentbased_traceidratio with
// OTEL_TRACES_SAMPLER_ARG as the ratio; otherwise it samples everything, which
// is what you want for a low-traffic demo where every request is interesting.
func sampler() sdktrace.Sampler {
	ratio := os.Getenv("OTEL_TRACES_SAMPLER_ARG")
	if os.Getenv("OTEL_TRACES_SAMPLER") == "parentbased_traceidratio" && ratio != "" {
		if r, err := parseFloat(ratio); err == nil {
			return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(r))
		}
	}
	return sdktrace.ParentBased(sdktrace.AlwaysSample())
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%g", &f)
	return f, err
}
