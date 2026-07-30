package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistry_ServeHTTP(t *testing.T) {
	reg := newRegistry()
	reg.setNodeEnergy("node-1", 123.45)
	reg.setPodEnergy("web-1", "shop", 67.89)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, req)

	body := rec.Body.String()
	wantLines := []string{
		`# TYPE kepler_node_cpu_joules_total counter`,
		`kepler_node_cpu_joules_total{node_name="node-1"} 123.45`,
		`# TYPE kepler_pod_cpu_joules_total counter`,
		`kepler_pod_cpu_joules_total{pod_name="web-1",pod_namespace="shop"} 67.89`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("body missing line %q\nfull body:\n%s", want, body)
		}
	}
}

// TestRegistry_ServeHTTP_TypeHintsPrecedeSamples guards the Prometheus text
// format rule that broke this feature end-to-end: a metric with no `# TYPE`
// line is "untyped", and the otel-agent's prometheus receiver maps untyped
// series to Gauge datapoints (otel_metrics_gauge) — but the hub's green SQL
// and this estimator's whole purpose (matching Kepler's real, TYPE-hinted
// output) both require Sum/counter semantics (otel_metrics_sum). The `# TYPE`
// comment must appear before any sample line of that metric family, or the
// receiver won't associate it with the family.
func TestRegistry_ServeHTTP_TypeHintsPrecedeSamples(t *testing.T) {
	reg := newRegistry()
	reg.setNodeEnergy("node-1", 123.45)
	reg.setPodEnergy("web-1", "shop", 67.89)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, req)

	body := rec.Body.String()
	nodeType := strings.Index(body, "# TYPE kepler_node_cpu_joules_total counter")
	nodeSample := strings.Index(body, "kepler_node_cpu_joules_total{")
	podType := strings.Index(body, "# TYPE kepler_pod_cpu_joules_total counter")
	podSample := strings.Index(body, "kepler_pod_cpu_joules_total{")
	if nodeType < 0 || nodeSample < 0 || nodeType > nodeSample {
		t.Errorf("node TYPE hint must precede its sample\nfull body:\n%s", body)
	}
	if podType < 0 || podSample < 0 || podType > podSample {
		t.Errorf("pod TYPE hint must precede its sample\nfull body:\n%s", body)
	}
}

func TestRegistry_EmptyWhenDormant(t *testing.T) {
	reg := newRegistry()
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body with no samples set, got %q", rec.Body.String())
	}
}
