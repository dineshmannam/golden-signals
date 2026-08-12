package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when an order does not exist.
var ErrNotFound = errors.New("order not found")

// Order is a row in the orders table.
type Order struct {
	ID          int64      `json:"id"`
	Item        string     `json:"item"`
	Quantity    int        `json:"quantity"`
	Cohort      string     `json:"cohort,omitempty"`
	Region      string     `json:"region,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	FulfilledAt *time.Time `json:"fulfilled_at,omitempty"`
}

// Store is the Postgres-backed persistence layer for orders.
//
// Every query is preceded by a fault-injection "dependency" hook so an operator
// can simulate a slow database — the classic cause of a latency-SLO breach —
// scoped to whatever cohort/region they choose. The slowness is applied here,
// close to the dependency, so it lands inside the DB span in the trace.
type Store struct {
	pool *pgxpool.Pool
	inj  *faultinject.Injector
}

// NewStore connects to Postgres using a DSN/URL and verifies the connection.
func NewStore(ctx context.Context, dsn string, inj *faultinject.Injector) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("orders: connecting to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("orders: pinging postgres: %w", err)
	}
	return &Store{pool: pool, inj: inj}, nil
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Ping is used by the readiness probe.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate creates the orders table if it does not exist. For a teaching demo we
// keep schema management inline and idempotent rather than pulling in a
// migration tool; production systems would use versioned migrations.
func (s *Store) Migrate(ctx context.Context) error {
	// The fulfilled_at column is written by the async fulfillment worker
	// (services/fulfillment) once it processes the OrderCreated event. orders
	// owns the schema, so the column is defined here even though another service
	// updates it.
	const ddl = `
CREATE TABLE IF NOT EXISTS orders (
    id           BIGSERIAL PRIMARY KEY,
    item         TEXT        NOT NULL,
    quantity     INTEGER     NOT NULL CHECK (quantity > 0),
    cohort       TEXT        NOT NULL DEFAULT '',
    region       TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    fulfilled_at TIMESTAMPTZ
);`
	if _, err := s.pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("orders: migrate: %w", err)
	}
	return nil
}

// Create inserts an order and returns it with its generated ID and timestamp.
func (s *Store) Create(ctx context.Context, scope faultinject.Scope, o Order) (Order, error) {
	if err := s.inj.Dependency(ctx, scope); err != nil {
		return Order{}, err
	}
	const q = `
INSERT INTO orders (item, quantity, cohort, region)
VALUES ($1, $2, $3, $4)
RETURNING id, created_at;`
	row := s.pool.QueryRow(ctx, q, o.Item, o.Quantity, o.Cohort, o.Region)
	if err := row.Scan(&o.ID, &o.CreatedAt); err != nil {
		return Order{}, fmt.Errorf("orders: insert: %w", err)
	}
	return o, nil
}

// Get returns the order with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, scope faultinject.Scope, id int64) (Order, error) {
	if err := s.inj.Dependency(ctx, scope); err != nil {
		return Order{}, err
	}
	const q = `
SELECT id, item, quantity, cohort, region, created_at, fulfilled_at
FROM orders WHERE id = $1;`
	var o Order
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&o.ID, &o.Item, &o.Quantity, &o.Cohort, &o.Region, &o.CreatedAt, &o.FulfilledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("orders: select: %w", err)
	}
	return o, nil
}
