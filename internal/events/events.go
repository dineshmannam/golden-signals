// Package events defines the schema of the asynchronous domain events that flow
// between services over Pub/Sub. Keeping the payload types in one shared package
// stops the producer (orders) and the consumer (fulfillment) from drifting.
//
// The wire format is JSON — human-readable in the emulator and in a dead-letter
// message dump, which matters more for a teaching demo than raw throughput.
package events

import "time"

// TopicOrderCreated is the logical name of the topic OrderCreated is published
// to. The concrete Pub/Sub topic/subscription IDs are configured per-service via
// the environment (see each service's docs); this constant is only the default.
const TopicOrderCreated = "order-created"

// OrderCreated is emitted by the orders service after an order row is committed
// to Postgres. The fulfillment worker consumes it, marks the order fulfilled,
// and emits its own RED metrics — with the trace context propagated in the
// Pub/Sub message attributes so the whole flow is one distributed trace.
type OrderCreated struct {
	OrderID   int64     `json:"order_id"`
	Item      string    `json:"item"`
	Quantity  int       `json:"quantity"`
	Cohort    string    `json:"cohort,omitempty"`
	Region    string    `json:"region,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
