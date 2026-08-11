package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dineshmannam/golden-signals/internal/faultinject"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// okHandler returns 200 for any request.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

func TestHandlerRecordsRequestMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "gateway")
	inj, _ := faultinject.Load() // disabled

	h := Handler(okHandler, "test", inj, metrics)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// The counter must have incremented for the normalized route with a 2xx class.
	if got := counterValue(t, reg, "http_requests_total", map[string]string{
		"service": "gateway", "route": "/orders/:id", "method": "GET", "status_class": "2xx",
	}); got != 1 {
		t.Fatalf("http_requests_total = %v, want 1", got)
	}
}

func TestHandlerInjectsAbort(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg, "gateway")

	// Build an enabled injector that always aborts /checkout with 503.
	inj := mustInjector(t, `[{"name":"boom","match":{"endpoint":"/checkout"},"abort":{"probability":1,"status":503}}]`)

	h := Handler(okHandler, "test", inj, metrics)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if got := counterValue(t, reg, "http_requests_total", map[string]string{
		"service": "gateway", "route": "/checkout", "method": "POST", "status_class": "5xx",
	}); got != 1 {
		t.Fatalf("expected one 5xx request recorded, got %v", got)
	}
}

func TestHandlerScopesFaultByRegion(t *testing.T) {
	inj := mustInjector(t, `[{"name":"emea","match":{"region":"emea"},"abort":{"probability":1,"status":503}}]`)
	h := Handler(okHandler, "test", inj, NewMetrics(prometheus.NewRegistry(), "gateway"))

	// us region: unaffected.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set(HeaderRegion, "us")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("us region status = %d, want 200", rec.Code)
	}

	// emea region: aborted.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/checkout", nil)
	req.Header.Set(HeaderRegion, "emea")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("emea region status = %d, want 503", rec.Code)
	}
}

// mustInjector builds an enabled injector from a FAULT_RULES JSON string.
func mustInjector(t *testing.T, rules string) *faultinject.Injector {
	t.Helper()
	t.Setenv("FAULT_ENABLED", "true")
	t.Setenv("FAULT_RULES", rules)
	inj, err := faultinject.Load()
	if err != nil {
		t.Fatalf("loading injector: %v", err)
	}
	return inj
}

// counterValue reads a single labelled counter sample from a registry.
func counterValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range families {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelsMatch(m, labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(m *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, lp := range m.GetLabel() {
		got[lp.GetName()] = lp.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
