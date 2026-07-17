//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestNetworkEdgesIntegration exercises NetworkEdges against real ClickHouse.
// Unit tests cover the merge via the fake store, so the parts that can only
// break here are the ones this guards: that sum(Value) — a Float64 column —
// scans into the uint64 Bytes field, that the Attributes map access resolves
// the OBI k8s owner keys, and that the self/unlabeled/metric filters actually
// prune in SQL rather than in Go.
func TestNetworkEdgesIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	tr := storage.TimeRange{Start: base, End: base.Add(10 * time.Minute)}
	noRes := map[string]string{} // no avuru.tenant -> Tenant defaults to "default"

	edge := func(src, dst string) map[string]string {
		return map[string]string{
			"k8s.src.owner.name": src, "k8s.src.namespace": "shop",
			"k8s.dst.owner.name": dst, "k8s.dst.namespace": "shop",
		}
	}

	// web -> cart: two cumulative points in-window; both are summed (the
	// documented approximation), so bytes = 4000.
	insertSum(t, store, base.Add(1*time.Minute), networkFlowMetric, noRes, edge("web", "cart"), 1000)
	insertSum(t, store, base.Add(5*time.Minute), networkFlowMetric, noRes, edge("web", "cart"), 3000)
	// cart -> postgres: the un-instrumented peer the whole feature exists for.
	insertSum(t, store, base.Add(2*time.Minute), networkFlowMetric, noRes, edge("cart", "postgres"), 500)
	// Must be pruned: self-edge, unlabeled dst, out-of-window, other metric.
	insertSum(t, store, base.Add(2*time.Minute), networkFlowMetric, noRes, edge("web", "web"), 900)
	insertSum(t, store, base.Add(2*time.Minute), networkFlowMetric, noRes, map[string]string{"k8s.src.owner.name": "web"}, 700)
	insertSum(t, store, base.Add(30*time.Minute), networkFlowMetric, noRes, edge("web", "outside-window"), 800)
	insertSum(t, store, base.Add(2*time.Minute), "k8s.node.network.io", noRes, edge("web", "other-metric"), 12345)

	edges, err := store.NetworkEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("NetworkEdges: %v", err)
	}

	got := make(map[string]storage.ServiceEdge, len(edges))
	for _, e := range edges {
		got[e.Source+"->"+e.Target] = e
	}
	if len(got) != 2 {
		t.Fatalf("got %d edges %v, want exactly web->cart and cart->postgres", len(got), got)
	}

	web, ok := got["web->cart"]
	if !ok {
		t.Fatalf("web->cart edge missing; got %v", got)
	}
	if web.Bytes != 4000 {
		t.Errorf("web->cart Bytes = %d, want 4000 (1000+3000 summed in-window)", web.Bytes)
	}
	if web.Provenance != "flow" {
		t.Errorf("web->cart Provenance = %q, want %q", web.Provenance, "flow")
	}
	if web.Count != 0 || web.ErrorCount != 0 {
		t.Errorf("web->cart carries call volume %d/%d, want 0/0 (flows are bytes, not calls)", web.Count, web.ErrorCount)
	}

	if db, ok := got["cart->postgres"]; !ok {
		t.Errorf("cart->postgres edge missing — the un-instrumented peer is the point of the feature; got %v", got)
	} else if db.Bytes != 500 {
		t.Errorf("cart->postgres Bytes = %d, want 500", db.Bytes)
	}

	// Ordering contract: heaviest edge first.
	if len(edges) > 1 && edges[0].Bytes < edges[1].Bytes {
		t.Errorf("edges not ordered by bytes desc: %v", edges)
	}
}

// TestNetworkEdgesTenantIsolation guards the multi-tenant seam: one tenant's
// flows must never surface on another's map.
func TestNetworkEdgesTenantIsolation(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	tr := storage.TimeRange{Start: base, End: base.Add(10 * time.Minute)}
	attrs := map[string]string{
		"k8s.src.owner.name": "web", "k8s.dst.owner.name": "cart",
	}

	insertSum(t, store, base.Add(1*time.Minute), networkFlowMetric,
		map[string]string{"avuru.tenant": "acme"}, attrs, 1000)

	edges, err := store.NetworkEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("NetworkEdges(default): %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("tenant leak: default sees %v, want none", edges)
	}

	edges, err = store.NetworkEdges(ctx, storage.ServiceQuery{Tenant: "acme", Range: tr})
	if err != nil {
		t.Fatalf("NetworkEdges(acme): %v", err)
	}
	if len(edges) != 1 || edges[0].Source != "web" || edges[0].Target != "cart" || edges[0].Bytes != 1000 {
		t.Errorf("acme edges = %v, want one web->cart with 1000 bytes", edges)
	}
}
