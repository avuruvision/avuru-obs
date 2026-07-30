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
		`kepler_node_cpu_joules_total{node_name="node-1"} 123.45`,
		`kepler_pod_cpu_joules_total{pod_name="web-1",pod_namespace="shop"} 67.89`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("body missing line %q\nfull body:\n%s", want, body)
		}
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
