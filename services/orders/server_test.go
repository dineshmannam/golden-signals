package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dineshmannam/golden-signals/internal/faultinject"
)

// fakeStore is an in-memory storage used to exercise the handlers without a DB.
type fakeStore struct {
	orders map[int64]Order
	nextID int64
}

func newFakeStore() *fakeStore { return &fakeStore{orders: map[int64]Order{}, nextID: 1} }

func (f *fakeStore) Create(_ context.Context, scope faultinject.Scope, o Order) (Order, error) {
	o.ID = f.nextID
	f.nextID++
	f.orders[o.ID] = o
	return o, nil
}

func (f *fakeStore) Get(_ context.Context, _ faultinject.Scope, id int64) (Order, error) {
	o, ok := f.orders[id]
	if !ok {
		return Order{}, ErrNotFound
	}
	return o, nil
}

func newTestServer(t *testing.T) *server {
	t.Helper()
	inj, _ := faultinject.Load()
	srv, err := newServer(newFakeStore(), inj, noopPublisher{})
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	return srv
}

func TestCreateAndGetOrder(t *testing.T) {
	srv := newTestServer(t)

	// Create.
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"item":"widget","quantity":3}`)
	req := httptest.NewRequest(http.MethodPost, "/orders", body)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var created Order
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding create response: %v", err)
	}
	if created.ID == 0 || created.Item != "widget" || created.Quantity != 3 {
		t.Fatalf("unexpected created order: %+v", created)
	}

	// Get it back.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/orders/1", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", rec.Code)
	}
}

func TestCreateValidation(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"item":"","quantity":0}`))
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetMissingOrder(t *testing.T) {
	srv := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders/999", nil)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
