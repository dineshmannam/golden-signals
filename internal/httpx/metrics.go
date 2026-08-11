package httpx

import (
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the native Prometheus request metrics that back the SLOs.
//
// We register these directly with client_golang (rather than via OpenTelemetry)
// so the exported series names are deterministic and stable across library
// upgrades — the Cloud Monitoring SLOs in infra/terraform reference them by
// exact name. Managed Service for Prometheus scrapes them via the PodMonitoring
// resource in deploy/, so in Cloud Monitoring they appear as:
//
//	prometheus.googleapis.com/http_requests_total/counter
//	prometheus.googleapis.com/http_request_duration_seconds/histogram
//
// The status_class label ("2xx".."5xx") is what the availability SLO uses to
// separate good from bad; the histogram buckets back the latency SLO.
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// NewMetrics registers the request metrics on reg. Pass the same registry that
// backs the /metrics endpoint so the series are scrapable. The service name is
// attached as a constant "service" label so the Cloud Monitoring SLOs can scope
// to a single service (the gateway) deterministically.
func NewMetrics(reg prometheus.Registerer, service string) *Metrics {
	constLabels := prometheus.Labels{"service": service}
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "http_requests_total",
			Help:        "Total HTTP requests handled, labelled by route, method and status class.",
			ConstLabels: constLabels,
		}, []string{"route", "method", "status_class"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "http_request_duration_seconds",
			Help:        "HTTP request latency in seconds.",
			ConstLabels: constLabels,
			// Buckets chosen around the p99 < 300ms latency objective so the SLO
			// has a bucket boundary exactly at the threshold.
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1, 2, 5},
		}, []string{"route", "method"}),
	}
	reg.MustRegister(m.requests, m.duration)
	return m
}

// Observe records one completed request. It is a no-op on a nil receiver.
func (m *Metrics) Observe(r *http.Request, status int, elapsed time.Duration) {
	if m == nil {
		return
	}
	route := normalizeRoute(r.URL.Path)
	m.requests.WithLabelValues(route, r.Method, statusClass(status)).Inc()
	m.duration.WithLabelValues(route, r.Method).Observe(elapsed.Seconds())
}

// statusClass buckets an HTTP status code into "2xx".."5xx" to keep the metric
// low-cardinality while still separating success from failure for the SLO.
func statusClass(status int) string {
	return strconv.Itoa(status/100) + "xx"
}

var numericSegment = regexp.MustCompile(`/\d+`)

// normalizeRoute collapses numeric path segments (e.g. /orders/42 -> /orders/:id)
// so per-ID requests do not explode metric cardinality.
func normalizeRoute(path string) string {
	return numericSegment.ReplaceAllString(path, "/:id")
}
