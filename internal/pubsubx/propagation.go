// Package pubsubx holds the small amount of Pub/Sub plumbing shared by the
// producer (orders) and the async consumer (fulfillment): W3C trace-context
// propagation through message attributes, and the native Prometheus processing
// metrics (RED) that back the fulfillment SLO.
//
// The trace-context propagation is the centrepiece of the streaming path. HTTP
// gets this for free from otelhttp, but Pub/Sub has no equivalent, so we inject
// the active span context into the message attributes on publish and extract it
// again on receive. That is what keeps a single trace ID flowing from the
// gateway, through orders, across the queue, and into fulfillment.
package pubsubx

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// messageCarrier adapts a Pub/Sub message's attribute map to the OpenTelemetry
// TextMapCarrier interface so the global propagator can read and write the
// standard traceparent/tracestate keys directly into it.
type messageCarrier map[string]string

func (c messageCarrier) Get(key string) string { return c[key] }
func (c messageCarrier) Set(key, value string) { c[key] = value }
func (c messageCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// compile-time check that we satisfy the carrier contract.
var _ propagation.TextMapCarrier = messageCarrier(nil)

// InjectTraceContext writes the trace context from ctx into attrs using the
// globally configured propagator (see internal/telemetry.Setup). Call it when
// building a message so the consumer can continue the trace. attrs must be
// non-nil.
func InjectTraceContext(ctx context.Context, attrs map[string]string) {
	otel.GetTextMapPropagator().Inject(ctx, messageCarrier(attrs))
}

// ExtractTraceContext returns a context carrying the trace context found in a
// received message's attrs, as the remote parent of any span started next. Call
// it as the first thing a consumer does with a message.
func ExtractTraceContext(ctx context.Context, attrs map[string]string) context.Context {
	return otel.GetTextMapPropagator().Extract(ctx, messageCarrier(attrs))
}
