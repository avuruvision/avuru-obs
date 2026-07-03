//go:build e2ehelm

// The time-to-value gate: a fresh cluster with an UNINSTRUMENTED demo app
// (deploy/demo/wedge) must show a live service map in under five minutes,
// with zero app changes — the product promise, enforced. Driven by
// deploy/helm/e2e-helm.sh, which exports WEDGE_T0_UNIX (the helm-install
// start; image pulls are pre-loaded and excluded from the clock).
package e2e

import (
	"os"
	"strconv"
	"testing"
	"time"
)

const wedgeBudget = 300 * time.Second

func wedgeT0(t *testing.T) time.Time {
	t.Helper()
	raw := os.Getenv("WEDGE_T0_UNIX")
	if raw == "" {
		t.Skip("WEDGE_T0_UNIX not set — wedge gate runs via deploy/helm/e2e-helm.sh")
	}
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("invalid WEDGE_T0_UNIX %q: %v", raw, err)
	}
	return time.Unix(sec, 0)
}

func TestWedgeServiceMapUnderFiveMinutes(t *testing.T) {
	t0 := wedgeT0(t)
	deadline := t0.Add(wedgeBudget)

	type edge struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	var resp struct {
		Services []struct {
			Name string `json:"name"`
		} `json:"services"`
		Edges []edge `json:"edges"`
	}

	demo := map[string]bool{"loadgen": true, "frontend": true, "api": true}
	for {
		err := helmGetJSON("/api/v1/service-map", &resp)
		if err == nil {
			nodes := map[string]bool{}
			for _, s := range resp.Services {
				nodes[s.Name] = true
			}
			var demoEdge *edge
			for i, e := range resp.Edges {
				if demo[e.Source] && demo[e.Target] {
					demoEdge = &resp.Edges[i]
					break
				}
			}
			if nodes["frontend"] && nodes["api"] && demoEdge != nil {
				elapsed := time.Since(t0)
				t.Logf("wedge: service map live after %s (edge %s->%s, %d services)",
					elapsed.Round(time.Second), demoEdge.Source, demoEdge.Target, len(resp.Services))
				if elapsed > wedgeBudget {
					t.Fatalf("service map took %s — over the %s wedge budget", elapsed.Round(time.Second), wedgeBudget)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no zero-code service map within %s (err=%v, services=%+v, edges=%+v)",
				wedgeBudget, err, resp.Services, resp.Edges)
		}
		time.Sleep(5 * time.Second)
	}
}

func TestWedgeInfraMetrics(t *testing.T) {
	t0 := wedgeT0(t)
	deadline := t0.Add(wedgeBudget)

	var nodes struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	}
	for {
		err := helmGetJSON("/api/v1/infra/nodes", &nodes)
		if err == nil && len(nodes.Nodes) > 0 {
			t.Logf("wedge: %d node(s) reporting infra metrics", len(nodes.Nodes))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no node metrics within %s (err=%v)", wedgeBudget, err)
		}
		time.Sleep(5 * time.Second)
	}
}

// Profiles are alpha — soft assertion (warn, don't fail) during burn-in.
func TestWedgeProfilesSoft(t *testing.T) {
	t0 := wedgeT0(t)
	deadline := t0.Add(wedgeBudget)

	var services struct {
		Services []struct {
			Name string `json:"name"`
		} `json:"services"`
	}
	for {
		err := helmGetJSON("/api/v1/profiles/services", &services)
		if err == nil && len(services.Services) > 0 {
			t.Logf("wedge: %d profiled service(s)", len(services.Services))
			return
		}
		if time.Now().After(deadline) {
			t.Logf("WARNING: no profiling data within %s (err=%v) — soft assertion during alpha burn-in", wedgeBudget, err)
			return
		}
		time.Sleep(10 * time.Second)
	}
}
