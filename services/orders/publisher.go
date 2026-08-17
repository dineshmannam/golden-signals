package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/dineshmannam/golden-signals/internal/config"
	"github.com/dineshmannam/golden-signals/internal/events"
	"github.com/dineshmannam/golden-signals/internal/pubsubx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// newPublisher builds the event publisher from the environment. When no Pub/Sub
// project is configured it returns a no-op publisher and logs that async
// fulfillment is disabled, so the synchronous API still runs (e.g. a bare
// `go run` with no broker).
func newPublisher(ctx context.Context, log *slog.Logger) (publisher, error) {
	projectID := config.String("PUBSUB_PROJECT_ID", config.String("GOOGLE_CLOUD_PROJECT", ""))
	if projectID == "" {
		log.Warn("PUBSUB_PROJECT_ID unset; OrderCreated publishing disabled")
		return noopPublisher{}, nil
	}
	topicID := config.String("PUBSUB_TOPIC_ORDER_CREATED", events.TopicOrderCreated)
	p, err := newPubsubPublisher(ctx, projectID, topicID)
	if err != nil {
		return nil, err
	}
	log.Info("event publishing enabled", "topic", topicID, "project", projectID)
	return p, nil
}

// publisher emits domain events. The orders API depends only on this interface
// so tests can substitute a no-op and the service degrades gracefully when
// Pub/Sub is not configured.
type publisher interface {
	PublishOrderCreated(ctx context.Context, e events.OrderCreated) error
	Close()
}

// noopPublisher drops events. Used in unit tests and when no topic is
// configured, so the synchronous order API keeps working without a broker.
type noopPublisher struct{}

func (noopPublisher) PublishOrderCreated(context.Context, events.OrderCreated) error { return nil }
func (noopPublisher) Close()                                                         {}

// pubsubPublisher publishes OrderCreated events to a Pub/Sub topic, propagating
// the active trace context into the message attributes so the fulfillment
// consumer continues the same distributed trace across the async boundary.
type pubsubPublisher struct {
	client *pubsub.Client
	topic  *pubsub.Topic
	name   string
	tracer trace.Tracer
}

// newPubsubPublisher connects a Pub/Sub client and resolves the topic. The
// PUBSUB_EMULATOR_HOST env var (set in docker-compose) transparently redirects
// the client to the local emulator, so no code path differs for local dev.
func newPubsubPublisher(ctx context.Context, projectID, topicID string) (*pubsubPublisher, error) {
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("orders: pubsub client: %w", err)
	}
	return &pubsubPublisher{
		client: client,
		topic:  client.Topic(topicID),
		name:   topicID,
		tracer: otel.Tracer("github.com/dineshmannam/golden-signals/services/orders"),
	}, nil
}

// PublishOrderCreated marshals the event, injects the trace context into the
// message attributes, and publishes it. It opens a PRODUCER span so the publish
// is a visible link in the trace between the DB write and the consumer.
func (p *pubsubPublisher) PublishOrderCreated(ctx context.Context, e events.OrderCreated) error {
	ctx, span := p.tracer.Start(ctx, "publish "+p.name,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			semconv.MessagingSystemGCPPubsub,
			semconv.MessagingDestinationName(p.name),
			attribute.Int64("order.id", e.OrderID),
		),
	)
	defer span.End()

	data, err := json.Marshal(e)
	if err != nil {
		span.SetStatus(codes.Error, "marshal event")
		span.RecordError(err)
		return fmt.Errorf("orders: marshal OrderCreated: %w", err)
	}

	attrs := map[string]string{"type": "OrderCreated"}
	// Inject AFTER the span starts so the consumer's parent is this producer span.
	pubsubx.InjectTraceContext(ctx, attrs)

	// Bound the publish confirmation so a wedged broker cannot hang the
	// (already-committed) synchronous request for its whole deadline.
	pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result := p.topic.Publish(pubCtx, &pubsub.Message{Data: data, Attributes: attrs})
	id, err := result.Get(pubCtx) // block until the broker acknowledges the publish
	if err != nil {
		span.SetStatus(codes.Error, "publish")
		span.RecordError(err)
		return fmt.Errorf("orders: publish OrderCreated: %w", err)
	}
	span.SetAttributes(semconv.MessagingMessageID(id))
	return nil
}

// Close flushes buffered publishes and releases the client.
func (p *pubsubPublisher) Close() {
	p.topic.Stop()
	_ = p.client.Close()
}
