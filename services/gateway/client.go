package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/dineshmannam/golden-signals/internal/httpx"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ordersClient calls the orders service over HTTP. The transport is wrapped with
// otelhttp so the outgoing request carries the W3C trace context and produces a
// client span — that is what stitches the gateway and orders spans into one
// distributed trace.
type ordersClient struct {
	baseURL string
	http    *http.Client
	inj     *faultinject.Injector
}

func newOrdersClient(baseURL string, inj *faultinject.Injector) *ordersClient {
	return &ordersClient{
		baseURL: baseURL,
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
		inj: inj,
	}
}

// forward proxies an inbound request to the orders service at the given path,
// preserving the body and the simulated cohort/region headers. The
// fault-injection dependency hook runs first so an operator can simulate a slow
// orders service scoped by cohort/region.
func (c *ordersClient) forward(ctx context.Context, scope faultinject.Scope, method, path string, body io.Reader) (*http.Response, error) {
	if err := c.inj.Dependency(ctx, scope); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("gateway: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if scope.Cohort != "" {
		req.Header.Set(httpx.HeaderCohort, scope.Cohort)
	}
	if scope.Region != "" {
		req.Header.Set(httpx.HeaderRegion, scope.Region)
	}
	return c.http.Do(req)
}

// ping checks that the orders service is reachable, for the readiness probe.
func (c *ordersClient) ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("orders healthz returned %d", resp.StatusCode)
	}
	return nil
}
