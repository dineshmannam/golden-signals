// Package httpx holds the small amount of HTTP plumbing shared by the gateway
// and orders services: OpenTelemetry instrumentation, a request-scoped fault
// hook, structured access logging, and health/readiness handlers.
//
// The middleware order matters and is fixed by Handler:
//
//	otelhttp (span + server metrics)
//	  -> access log + native request metrics
//	    -> attribute enrichment (cohort/region as high-cardinality span attrs)
//	      -> fault injection (latency / abort, scoped by request attributes)
//	        -> your handler
//
// The access-log/metrics layer wraps the fault layer deliberately: a
// fault-injected abort short-circuits the handler, so it must still be counted
// (and timed) as the final response — the whole point is that the fault is
// visible in the golden signals. otelhttp stays outermost so the span covers the
// injected latency too.
package httpx

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Headers carrying the simulated cohort/region used both for span attributes and
// for scoping fault-injection rules.
const (
	HeaderCohort = "X-Cohort"
	HeaderRegion = "X-Region"
)

// Handler wraps h with the standard middleware stack for a service. operation is
// the span name prefix used by otelhttp (e.g. the service name). metrics may be
// nil to skip the native Prometheus request metrics.
func Handler(h http.Handler, operation string, inj *faultinject.Injector, metrics *Metrics) http.Handler {
	h = faultMiddleware(inj)(h)
	h = attributeMiddleware(h)
	h = accessLog(metrics)(h)
	return otelhttp.NewHandler(h, operation)
}

// ScopeFromRequest derives a fault-injection scope from the request. Endpoint is
// the URL path; cohort and region come from the simulated headers.
func ScopeFromRequest(r *http.Request) faultinject.Scope {
	return faultinject.Scope{
		Endpoint: r.URL.Path,
		Cohort:   r.Header.Get(HeaderCohort),
		Region:   r.Header.Get(HeaderRegion),
	}
}

// attributeMiddleware attaches the simulated cohort/region to the active span.
// These are intentionally high-cardinality: the blog series uses them to show
// how you slice traces and exemplars to find a fault with a narrow blast radius.
func attributeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.SpanFromContext(r.Context())
		if c := r.Header.Get(HeaderCohort); c != "" {
			span.SetAttributes(attribute.String("user.cohort", c))
		}
		if reg := r.Header.Get(HeaderRegion); reg != "" {
			span.SetAttributes(attribute.String("user.region", reg))
		}
		next.ServeHTTP(w, r)
	})
}

// faultMiddleware applies request-side fault effects before the handler runs.
func faultMiddleware(inj *faultinject.Injector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := inj.Request(r.Context(), ScopeFromRequest(r)); err != nil {
				var abort *faultinject.AbortError
				if asAbort(err, &abort) {
					span := trace.SpanFromContext(r.Context())
					span.SetAttributes(attribute.Bool("fault.injected", true),
						attribute.String("fault.rule", abort.Rule))
					http.Error(w, "fault-injected failure", abort.Status)
					return
				}
				// Context cancelled while sleeping: let the request unwind.
				http.Error(w, "request cancelled", http.StatusRequestTimeout)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// accessLog emits one structured JSON line per request and records the native
// Prometheus request metrics that back the SLOs.
func accessLog(metrics *Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			elapsed := time.Since(start)
			metrics.Observe(r, rec.status, elapsed)
			telemetry.Logger().LogAttrs(r.Context(), slog.LevelInfo, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", elapsed.Milliseconds()),
				slog.String("cohort", r.Header.Get(HeaderCohort)),
				slog.String("region", r.Header.Get(HeaderRegion)),
			)
		})
	}
}

// statusRecorder captures the response status code for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func asAbort(err error, target **faultinject.AbortError) bool {
	ae, ok := err.(*faultinject.AbortError)
	if ok {
		*target = ae
	}
	return ok
}
