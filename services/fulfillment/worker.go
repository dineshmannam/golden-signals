package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/dineshmannam/golden-signals/internal/events"
	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/pubsubx"
	"github.com/dineshmannam/golden-signals/internal/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// fulfiller is the persistence behaviour the worker depends on. *Store is the
// Postgres implementation; tests supply a fake.
type fulfiller interface {
	MarkFulfilled(ctx context.Context, scope faultinject.Scope, orderID int64) error
}

// worker consumes OrderCreated events from a Pub/Sub subscription and marks the
// corresponding orders fulfilled. It extracts the W3C trace context from each
// message so its processing span continues the trace started at the gateway —
// one trace ID from the front door, across the queue, to here.
type worker struct {
	sub     *pubsub.Subscription
	subName string
	store   fulfiller
	inj     *faultinject.Injector
	metrics *pubsubx.Metrics
	tracer  trace.Tracer
	log     *slog.Logger
}

func newWorker(sub *pubsub.Subscription, subName string, store fulfiller, inj *faultinject.Injector, metrics *pubsubx.Metrics) *worker {
	return &worker{
		sub:     sub,
		subName: subName,
		store:   store,
		inj:     inj,
		metrics: metrics,
		tracer:  otel.Tracer("github.com/dineshmannam/golden-signals/services/fulfillment"),
		log:     telemetry.Logger(),
	}
}

// Run pulls messages until ctx is cancelled. Receive blocks and dispatches each
// message to handle on its own goroutine (the pubsub client manages concurrency).
func (w *worker) Run(ctx context.Context) error {
	w.log.Info("fulfillment subscribing", "subscription", w.subName)
	if err := w.sub.Receive(ctx, w.handle); err != nil && ctx.Err() == nil {
		return fmt.Errorf("fulfillment: receive: %w", err)
	}
	return nil
}

// handle processes one message end to end: continue the trace, process, record
// the RED metrics and a structured log line, then Ack (success) or Nack
// (failure -> redelivery, and eventually the dead-letter topic).
func (w *worker) handle(ctx context.Context, msg *pubsub.Message) {
	start := time.Now()

	// Continue the distributed trace from the producer (orders). This is the
	// async-boundary crossing the whole streaming path exists to demonstrate.
	ctx = pubsubx.ExtractTraceContext(ctx, msg.Attributes)
	ctx, span := w.tracer.Start(ctx, "process "+w.subName,
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemGCPPubsub,
			semconv.MessagingDestinationName(w.subName),
			semconv.MessagingMessageID(msg.ID),
			attribute.Int("messaging.gcp_pubsub.message.delivery_attempt", deliveryAttempt(msg)),
		),
	)
	defer span.End()

	result := "success"
	if err := w.process(ctx, msg); err != nil {
		result = "error"
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
		w.log.ErrorContext(ctx, "process message failed",
			"error", err, "message_id", msg.ID, "subscription", w.subName)
		// Nack: Pub/Sub redelivers, and after maxDeliveryAttempts routes the
		// message to the dead-letter topic (configured in infra/terraform).
		msg.Nack()
	} else {
		msg.Ack()
	}

	elapsed := time.Since(start)
	w.metrics.Observe(w.subName, result, elapsed)
	w.log.LogAttrs(ctx, slog.LevelInfo, "message processed",
		slog.String("subscription", w.subName),
		slog.String("result", result),
		slog.String("message_id", msg.ID),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	)
}

// process decodes the event and marks the order fulfilled. The fault-injection
// request hook runs first (scoped by the event's cohort/region) so an operator
// can force processing failures and exercise the dead-letter path on demand.
func (w *worker) process(ctx context.Context, msg *pubsub.Message) error {
	var e events.OrderCreated
	if err := json.Unmarshal(msg.Data, &e); err != nil {
		return fmt.Errorf("unmarshal OrderCreated: %w", err)
	}

	scope := faultinject.Scope{Endpoint: "/fulfill", Cohort: e.Cohort, Region: e.Region}
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.Int64("order.id", e.OrderID),
		attribute.String("user.cohort", e.Cohort),
		attribute.String("user.region", e.Region),
	)

	if err := w.inj.Request(ctx, scope); err != nil {
		return fmt.Errorf("fault-injected: %w", err)
	}
	return w.store.MarkFulfilled(ctx, scope, e.OrderID)
}

// deliveryAttempt returns the Pub/Sub delivery attempt (1-based) when dead-letter
// tracking is enabled on the subscription, or 0 when unavailable.
func deliveryAttempt(msg *pubsub.Message) int {
	if msg.DeliveryAttempt == nil {
		return 0
	}
	return *msg.DeliveryAttempt
}
