package main

import (
	"context"
	"encoding/json"
	"testing"

	"cloud.google.com/go/pubsub"
	"github.com/dineshmannam/golden-signals/internal/events"
	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/pubsubx"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// fakeFulfiller records the order IDs it was asked to fulfill and can be told to
// fail, to exercise the error/Nack path.
type fakeFulfiller struct {
	fulfilled []int64
	err       error
}

func (f *fakeFulfiller) MarkFulfilled(_ context.Context, _ faultinject.Scope, id int64) error {
	if f.err != nil {
		return f.err
	}
	f.fulfilled = append(f.fulfilled, id)
	return nil
}

func newTestWorker(t *testing.T, store fulfiller) *worker {
	t.Helper()
	inj, _ := faultinject.Load()
	metrics := pubsubx.NewMetrics(prometheus.NewRegistry(), serviceName)
	// sub is nil: handle/process never touch it, only Run does.
	return newWorker(nil, "test-sub", store, inj, metrics)
}

func TestProcessMarksFulfilled(t *testing.T) {
	store := &fakeFulfiller{}
	w := newTestWorker(t, store)

	data, _ := json.Marshal(events.OrderCreated{OrderID: 42, Item: "widget", Quantity: 2})
	if err := w.process(context.Background(), &pubsub.Message{Data: data}); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(store.fulfilled) != 1 || store.fulfilled[0] != 42 {
		t.Fatalf("fulfilled = %v, want [42]", store.fulfilled)
	}
}

func TestProcessRejectsMalformedPayload(t *testing.T) {
	w := newTestWorker(t, &fakeFulfiller{})
	if err := w.process(context.Background(), &pubsub.Message{Data: []byte("not json")}); err == nil {
		t.Fatal("expected error for malformed payload")
	}
}

// TestTraceContextContinues is the centerpiece assertion: a trace context
// injected by the producer into message attributes is extracted by the worker so
// its span shares the producer's trace ID — the async boundary is crossed.
func TestTraceContextContinues(t *testing.T) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetTracerProvider(sdktrace.NewTracerProvider())

	// Producer side: start a span and inject its context into attributes.
	producerCtx, producerSpan := otel.Tracer("producer").Start(context.Background(), "publish")
	wantTraceID := producerSpan.SpanContext().TraceID()
	attrs := map[string]string{}
	pubsubx.InjectTraceContext(producerCtx, attrs)
	producerSpan.End()

	if attrs["traceparent"] == "" {
		t.Fatal("expected traceparent attribute to be injected")
	}

	// Consumer side: extract, then start the processing span.
	w := newTestWorker(t, &fakeFulfiller{})
	consumerCtx := pubsubx.ExtractTraceContext(context.Background(), attrs)
	_, span := w.tracer.Start(consumerCtx, "process")
	defer span.End()

	if got := span.SpanContext().TraceID(); got != wantTraceID {
		t.Fatalf("consumer trace ID = %s, want %s (trace did not cross the queue)", got, wantTraceID)
	}
}
