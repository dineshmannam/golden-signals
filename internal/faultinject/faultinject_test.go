package faultinject

import (
	"context"
	"testing"
	"time"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"", "anything", true},
		{"/checkout", "/checkout", true},
		{"/checkout", "/orders", false},
		{"/api/*", "/api/orders", true},
		{"canary", "canary", true},
		{"*", "emea", true},
	}
	for _, c := range cases {
		if got := globMatch(c.pattern, c.value); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.value, got, c.want)
		}
	}
}

func TestDisabledInjectorIsNoop(t *testing.T) {
	i := &Injector{enabled: false}
	if err := i.Request(context.Background(), Scope{Endpoint: "/x"}); err != nil {
		t.Fatalf("disabled Request returned %v, want nil", err)
	}
	if err := i.Dependency(context.Background(), Scope{Endpoint: "/x"}); err != nil {
		t.Fatalf("disabled Dependency returned %v, want nil", err)
	}
}

func TestAbortAlwaysFiresWhenProbabilityOne(t *testing.T) {
	i := newTestInjector(Rule{
		Name:  "always-503",
		Match: Matcher{Endpoint: "/checkout"},
		Abort: Abort{Probability: 1, Status: 503},
	})
	err := i.Request(context.Background(), Scope{Endpoint: "/checkout"})
	var ae *AbortError
	if err == nil {
		t.Fatal("expected abort error, got nil")
	}
	if !asAbort(err, &ae) || ae.Status != 503 {
		t.Fatalf("expected *AbortError status 503, got %v", err)
	}
}

func TestAbortRespectsScope(t *testing.T) {
	i := newTestInjector(Rule{
		Name:  "emea-only",
		Match: Matcher{Region: "emea"},
		Abort: Abort{Probability: 1, Status: 503},
	})
	// A non-matching region must pass untouched.
	if err := i.Request(context.Background(), Scope{Region: "us"}); err != nil {
		t.Fatalf("non-matching scope should be no-op, got %v", err)
	}
	// The matching region must abort.
	if err := i.Request(context.Background(), Scope{Region: "emea"}); err == nil {
		t.Fatal("matching scope should abort")
	}
}

func TestLatencyInjectsDelay(t *testing.T) {
	i := newTestInjector(Rule{
		Name:    "slow",
		Latency: Window{Probability: 1, MinMS: 40, MaxMS: 40},
	})
	start := time.Now()
	if err := i.Request(context.Background(), Scope{Endpoint: "/x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Fatalf("expected ~40ms delay, got %v", elapsed)
	}
}

func TestLatencyHonoursContextCancellation(t *testing.T) {
	i := newTestInjector(Rule{
		Name:    "very-slow",
		Latency: Window{Probability: 1, MinMS: 5000, MaxMS: 5000},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := i.Request(ctx, Scope{Endpoint: "/x"})
	if err == nil {
		t.Fatal("expected context error")
	}
	if time.Since(start) > time.Second {
		t.Fatal("Request did not honour context cancellation")
	}
}

func TestDependencyLatency(t *testing.T) {
	i := newTestInjector(Rule{
		Name:       "slow-dep",
		DepLatency: Window{Probability: 1, MinMS: 30, MaxMS: 30},
	})
	start := time.Now()
	if err := i.Dependency(context.Background(), Scope{Endpoint: "/x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("expected dependency delay")
	}
}

// newTestInjector builds an enabled injector with a fixed seed for determinism.
func newTestInjector(rules ...Rule) *Injector {
	i, _ := Load()
	i.enabled = true
	i.rules = rules
	return i
}

func asAbort(err error, target **AbortError) bool {
	ae, ok := err.(*AbortError)
	if ok {
		*target = ae
	}
	return ok
}
