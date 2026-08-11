package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

// SetupLogging installs a structured JSON slog handler as the default logger.
//
// Every record carries the service name and, when the context of a log call
// belongs to an active span, the trace_id and span_id. Cloud Logging (and any
// OTLP log pipeline) can then join a log line to its trace. The log level is
// read from LOG_LEVEL (debug|info|warn|error), defaulting to info.
func SetupLogging(serviceName string) {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL"))); err != nil {
		level = slog.LevelInfo
	}
	handler := &traceHandler{
		Handler: slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level: level,
			// Use "severity"/"message" so Cloud Logging parses the level and
			// summary line out of structured payloads without extra config.
			ReplaceAttr: cloudLoggingKeys,
		}),
	}
	logger := slog.New(handler).With(slog.String("service", serviceName))
	slog.SetDefault(logger)
}

// Logger returns the process-wide structured logger.
func Logger() *slog.Logger { return slog.Default() }

// cloudLoggingKeys renames slog's default keys to the field names Cloud Logging
// recognises in structured (jsonPayload) entries.
func cloudLoggingKeys(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.LevelKey:
		a.Key = "severity"
		if lvl, ok := a.Value.Any().(slog.Level); ok {
			a.Value = slog.StringValue(cloudSeverity(lvl))
		}
	case slog.MessageKey:
		a.Key = "message"
	case slog.TimeKey:
		a.Key = "timestamp"
	}
	return a
}

func cloudSeverity(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "ERROR"
	case l >= slog.LevelWarn:
		return "WARNING"
	case l >= slog.LevelInfo:
		return "INFO"
	default:
		return "DEBUG"
	}
}

// traceHandler decorates each record with the active trace/span IDs.
type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}

// PrometheusHandler returns an http.Handler that serves the metrics collected by
// the given registry in the Prometheus text format, suitable for scraping by
// Managed Service for Prometheus.
func PrometheusHandler(reg *prometheus.Registry) http.Handler {
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
}
