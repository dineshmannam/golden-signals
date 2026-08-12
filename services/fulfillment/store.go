package main

import (
	"context"
	"fmt"

	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed persistence layer for the fulfillment worker.
//
// It shares the orders table (orders owns the schema, including the fulfilled_at
// column) and only ever marks an order fulfilled. As in the orders store, every
// query is preceded by a fault-injection "dependency" hook so an operator can
// simulate a slow database on the async path, scoped by cohort/region.
type Store struct {
	pool *pgxpool.Pool
	inj  *faultinject.Injector
}

// NewStore connects to Postgres using a DSN/URL and verifies the connection.
func NewStore(ctx context.Context, dsn string, inj *faultinject.Injector) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("fulfillment: connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("fulfillment: pinging postgres: %w", err)
	}
	return &Store{pool: pool, inj: inj}, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Ping is used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// MarkFulfilled stamps fulfilled_at on the order. The WHERE clause makes it
// idempotent: redelivery of an already-fulfilled order (Pub/Sub is at-least-once)
// is a no-op success rather than an error, so duplicate messages do not poison
// the subscription. A missing row is reported so the message can be retried
// (e.g. the event arrived before the row's transaction was visible).
func (s *Store) MarkFulfilled(ctx context.Context, scope faultinject.Scope, orderID int64) error {
	if err := s.inj.Dependency(ctx, scope); err != nil {
		return err
	}
	const q = `
UPDATE orders SET fulfilled_at = now()
WHERE id = $1 AND fulfilled_at IS NULL;`
	tag, err := s.pool.Exec(ctx, q, orderID)
	if err != nil {
		return fmt.Errorf("fulfillment: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either already fulfilled (fine) or not yet visible. Distinguish with a
		// cheap existence check so a genuinely missing row is retried.
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM orders WHERE id = $1)`, orderID).Scan(&exists); err != nil {
			return fmt.Errorf("fulfillment: existence check: %w", err)
		}
		if !exists {
			return fmt.Errorf("fulfillment: order %d not found", orderID)
		}
	}
	return nil
}
