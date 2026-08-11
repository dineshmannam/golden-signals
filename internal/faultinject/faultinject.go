// Package faultinject is a small, config-driven fault-injection layer shared by
// the gateway and orders services.
//
// It exists so the blog series can demonstrate SRE workflows on demand: burn an
// error budget, run a game-day, and practice high-cardinality debugging. Faults
// are expressed as a list of rules. Each rule has a *matcher* that scopes it to
// a subset of traffic (by endpoint, user cohort, or region) and one or more
// *effects* (added latency, request aborts, or dependency slowness). Because the
// matcher can key off simulated cohort/region headers, an operator can inject a
// fault that only hits, say, the "canary" cohort in the "emea" region on the
// "/checkout" endpoint — exactly the kind of narrow blast radius you want to be
// able to find in a trace or a high-cardinality metric.
//
// Configuration is read entirely from the environment (see Load) so the same
// binary can be told to misbehave in a deployment without a rebuild.
//
// # Knobs
//
// Global:
//
//	FAULT_ENABLED   "true" to activate the injector (default: disabled).
//	FAULT_SEED      optional int64 seed for the RNG (default: time-based).
//	FAULT_RULES     JSON array of rules (see Rule). Overrides the simple knobs
//	                below when set.
//
// Simple single-rule knobs (used only when FAULT_RULES is empty). They build one
// rule that matches all traffic unless a FAULT_MATCH_* scope is given:
//
//	FAULT_MATCH_ENDPOINT   glob matched against the request route (e.g. /checkout).
//	FAULT_MATCH_COHORT     glob matched against the X-Cohort header.
//	FAULT_MATCH_REGION     glob matched against the X-Region header.
//	FAULT_LATENCY_PROB     0..1 probability of adding request latency.
//	FAULT_LATENCY_MIN_MS   lower bound of the injected latency window.
//	FAULT_LATENCY_MAX_MS   upper bound of the injected latency window.
//	FAULT_ABORT_PROB       0..1 probability of aborting the request.
//	FAULT_ABORT_STATUS     HTTP status to abort with (default 503).
//	FAULT_DEP_LATENCY_PROB 0..1 probability of slowing a dependency call.
//	FAULT_DEP_LATENCY_MIN_MS / FAULT_DEP_LATENCY_MAX_MS   dependency slowdown window.
//
// Empty/unset values disable the corresponding effect, so a fresh deployment is
// fault-free until an operator flips a knob.
package faultinject

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path"
	"strconv"
	"sync"
	"time"
)

// Scope describes the attributes of an in-flight request that rules match
// against. It is deliberately small and stringly-typed: these are the same
// attributes we attach to spans and metrics, which is what lets an operator
// correlate an injected fault with what they see in the telemetry backend.
type Scope struct {
	Endpoint string // route template or path, e.g. "/checkout"
	Cohort   string // simulated user cohort, from the X-Cohort header
	Region   string // simulated region, from the X-Region header
}

// Window is an inclusive [MinMS, MaxMS] range of milliseconds. A zero window
// means "no delay".
type Window struct {
	Probability float64 `json:"probability"`
	MinMS       int     `json:"min_ms"`
	MaxMS       int     `json:"max_ms"`
}

// Abort makes a matched request fail with an HTTP status.
type Abort struct {
	Probability float64 `json:"probability"`
	Status      int     `json:"status"`
}

// Matcher scopes a rule to a subset of traffic. Empty fields match anything; a
// non-empty field is a glob (supporting '*' and '?') matched against the
// corresponding Scope attribute.
type Matcher struct {
	Endpoint string `json:"endpoint"`
	Cohort   string `json:"cohort"`
	Region   string `json:"region"`
}

func (m Matcher) matches(s Scope) bool {
	return globMatch(m.Endpoint, s.Endpoint) &&
		globMatch(m.Cohort, s.Cohort) &&
		globMatch(m.Region, s.Region)
}

// globMatch reports whether value matches pattern. An empty pattern matches
// anything. It uses path.Match semantics ('*' and '?').
func globMatch(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	ok, err := path.Match(pattern, value)
	if err != nil {
		// A malformed pattern should never silently match everything; treat it
		// as a literal comparison instead.
		return pattern == value
	}
	return ok
}

// Rule is one fault definition: a matcher plus the effects to apply when it
// matches. Request-side effects (Latency, Abort) are applied by the HTTP
// middleware; DepLatency is applied around outbound dependency calls.
type Rule struct {
	Name       string  `json:"name"`
	Match      Matcher `json:"match"`
	Latency    Window  `json:"latency"`
	Abort      Abort   `json:"abort"`
	DepLatency Window  `json:"dep_latency"`
}

// AbortError is returned by Request when a rule aborts the request. The HTTP
// middleware turns it into a response with the given Status.
type AbortError struct {
	Status int
	Rule   string
}

func (e *AbortError) Error() string {
	return fmt.Sprintf("fault-injected abort (rule %q, status %d)", e.Rule, e.Status)
}

// Injector applies fault rules. It is safe for concurrent use.
type Injector struct {
	enabled bool
	rules   []Rule

	mu  sync.Mutex
	rng *rand.Rand
}

// Load builds an Injector from the environment. It never fails: on a malformed
// FAULT_RULES value it returns a disabled injector and a non-nil error so the
// caller can log it and continue serving traffic.
func Load() (*Injector, error) {
	enabled := os.Getenv("FAULT_ENABLED") == "true"

	seed := time.Now().UnixNano()
	if v := os.Getenv("FAULT_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	inj := &Injector{
		enabled: enabled,
		rng:     rand.New(rand.NewSource(seed)),
	}

	if raw := os.Getenv("FAULT_RULES"); raw != "" {
		var rules []Rule
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			inj.enabled = false
			return inj, fmt.Errorf("faultinject: parsing FAULT_RULES: %w", err)
		}
		inj.rules = rules
		return inj, nil
	}

	inj.rules = []Rule{ruleFromSimpleEnv()}
	return inj, nil
}

// ruleFromSimpleEnv assembles a single rule from the FAULT_* scalar knobs.
func ruleFromSimpleEnv() Rule {
	return Rule{
		Name: "env",
		Match: Matcher{
			Endpoint: os.Getenv("FAULT_MATCH_ENDPOINT"),
			Cohort:   os.Getenv("FAULT_MATCH_COHORT"),
			Region:   os.Getenv("FAULT_MATCH_REGION"),
		},
		Latency: Window{
			Probability: envFloat("FAULT_LATENCY_PROB"),
			MinMS:       envInt("FAULT_LATENCY_MIN_MS"),
			MaxMS:       envInt("FAULT_LATENCY_MAX_MS"),
		},
		Abort: Abort{
			Probability: envFloat("FAULT_ABORT_PROB"),
			Status:      envIntDefault("FAULT_ABORT_STATUS", 503),
		},
		DepLatency: Window{
			Probability: envFloat("FAULT_DEP_LATENCY_PROB"),
			MinMS:       envInt("FAULT_DEP_LATENCY_MIN_MS"),
			MaxMS:       envInt("FAULT_DEP_LATENCY_MAX_MS"),
		},
	}
}

// Enabled reports whether the injector will do anything.
func (i *Injector) Enabled() bool { return i != nil && i.enabled }

// Request applies request-side effects (latency, then abort) for the first rule
// that matches scope. It blocks for any injected latency, honouring ctx
// cancellation, and returns a non-nil *AbortError if the request should be
// failed. When the injector is disabled it is a no-op.
func (i *Injector) Request(ctx context.Context, scope Scope) error {
	if !i.Enabled() {
		return nil
	}
	rule, ok := i.match(scope)
	if !ok {
		return nil
	}
	if d := i.roll(rule.Latency); d > 0 {
		if err := sleep(ctx, d); err != nil {
			return err
		}
	}
	if i.hit(rule.Abort.Probability) {
		status := rule.Abort.Status
		if status == 0 {
			status = 503
		}
		return &AbortError{Status: status, Rule: rule.Name}
	}
	return nil
}

// Dependency applies dependency-slowness for the first rule matching scope. Call
// it immediately before an outbound dependency call (an RPC, a database query)
// to simulate a slow downstream. When disabled it is a no-op.
func (i *Injector) Dependency(ctx context.Context, scope Scope) error {
	if !i.Enabled() {
		return nil
	}
	rule, ok := i.match(scope)
	if !ok {
		return nil
	}
	if d := i.roll(rule.DepLatency); d > 0 {
		return sleep(ctx, d)
	}
	return nil
}

func (i *Injector) match(scope Scope) (Rule, bool) {
	for _, r := range i.rules {
		if r.Match.matches(scope) {
			return r, true
		}
	}
	return Rule{}, false
}

// roll returns a randomised delay for a window, or 0 if the window does not fire.
func (i *Injector) roll(w Window) time.Duration {
	if w.MaxMS <= 0 || !i.hit(w.Probability) {
		return 0
	}
	lo, hi := w.MinMS, w.MaxMS
	if lo > hi {
		lo, hi = hi, lo
	}
	i.mu.Lock()
	n := lo
	if hi > lo {
		n = lo + i.rng.Intn(hi-lo+1)
	}
	i.mu.Unlock()
	return time.Duration(n) * time.Millisecond
}

// hit reports whether an event with the given probability fires. p <= 0 never
// fires; p >= 1 always fires.
func (i *Injector) hit(p float64) bool {
	if p <= 0 {
		return false
	}
	if p >= 1 {
		return true
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.rng.Float64() < p
}

// sleep blocks for d or until ctx is done, whichever comes first.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func envFloat(key string) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return 0
}

func envInt(key string) int { return envIntDefault(key, 0) }

func envIntDefault(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
