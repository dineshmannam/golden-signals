package pubsubx

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds the native Prometheus processing metrics that back the
// fulfillment SLO. They are the message-processing analogue of the HTTP request
// metrics in internal/httpx (RED: rate, errors, duration).
//
// As with httpx, these are registered directly with client_golang so the series
// names are deterministic and stable — the Cloud Monitoring SLO in
// infra/terraform/pubsub monitoring references them by exact name. Managed
// Service for Prometheus scrapes them via the PodMonitoring in deploy/, where
// they surface as:
//
//	prometheus.googleapis.com/messages_processed_total/counter
//	prometheus.googleapis.com/message_processing_duration_seconds/histogram
//
// The result label ("success"|"error") is what the processing-success SLO uses
// to separate good from bad; the histogram buckets back a processing-latency
// view. A constant "service" label scopes the series to a single worker, exactly
// like httpx.NewMetrics.
type Metrics struct {
	processed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
}

// NewMetrics registers the processing metrics on reg with service attached as a
// constant label. Pass the same registry that backs the /metrics endpoint.
func NewMetrics(reg prometheus.Registerer, service string) *Metrics {
	constLabels := prometheus.Labels{"service": service}
	m := &Metrics{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "messages_processed_total",
			Help:        "Total messages processed, labelled by subscription and result.",
			ConstLabels: constLabels,
		}, []string{"subscription", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "message_processing_duration_seconds",
			Help:        "Message processing latency in seconds.",
			ConstLabels: constLabels,
			Buckets:     []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.2, 0.3, 0.5, 1, 2, 5},
		}, []string{"subscription"}),
	}
	reg.MustRegister(m.processed, m.duration)
	return m
}

// Observe records one processed message. result is "success" or "error". It is a
// no-op on a nil receiver.
func (m *Metrics) Observe(subscription, result string, elapsed time.Duration) {
	if m == nil {
		return
	}
	m.processed.WithLabelValues(subscription, result).Inc()
	m.duration.WithLabelValues(subscription).Observe(elapsed.Seconds())
}
