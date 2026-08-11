package httpx

import (
	"encoding/json"
	"net/http"
)

// Checker reports whether a dependency is healthy. It should be cheap and
// non-blocking-ish; readiness probes call it on a schedule.
type Checker func() error

// Health serves liveness and readiness. Register HealthHandler at /healthz
// (liveness: is the process up) and, optionally, ReadyHandler at /readyz
// (readiness: are dependencies reachable).
type Health struct {
	// Checks are the readiness dependencies, keyed by name (e.g. "postgres").
	Checks map[string]Checker
}

// LivenessHandler always returns 200 while the process can serve HTTP. Use it
// for the Kubernetes liveness probe.
func (h *Health) LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ReadinessHandler runs every check and returns 503 if any fail. Use it for the
// Kubernetes readiness probe so traffic is withheld until dependencies are up.
func (h *Health) ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := make(map[string]string, len(h.Checks))
		status := http.StatusOK
		for name, check := range h.Checks {
			if err := check(); err != nil {
				results[name] = err.Error()
				status = http.StatusServiceUnavailable
				continue
			}
			results[name] = "ok"
		}
		writeJSON(w, status, map[string]any{"status": statusText(status), "checks": results})
	}
}

func statusText(code int) string {
	if code == http.StatusOK {
		return "ok"
	}
	return "unavailable"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
