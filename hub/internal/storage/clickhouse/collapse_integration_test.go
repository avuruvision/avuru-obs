//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// edgeKey renders an edge for map-style comparison.
func edgeKey(e storage.ServiceEdge) string { return e.Source + "→" + e.Target }

func collapsedByPair(edges []storage.ServiceEdge) map[string]storage.ServiceEdge {
	out := make(map[string]storage.ServiceEdge, len(edges))
	for _, e := range edges {
		out[edgeKey(e)] = e
	}
	return out
}

// The sidecar shape: checkout's Client span is intercepted by a proxy, which
// forwards to orders. The map has to report checkout→orders — the dependency
// that exists — and name the proxy it was recovered across.
func TestCollapsedEdgesSidecarHop(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	insertSpans(t, store, []testSpan{
		{base, "t1", "c1", "", "POST /orders", "Client", "checkout", 90 * time.Millisecond, "Unset"},
		{base, "t1", "p1", "c1", "POST /orders", "Server", "istio-proxy", 85 * time.Millisecond, "Unset"},
		{base, "t1", "s1", "p1", "POST /orders", "Server", "orders", 70 * time.Millisecond, "Unset"},
	})

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(time.Minute)}
	got, err := store.CollapsedEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr}, []string{"istio-proxy"})
	if err != nil {
		t.Fatalf("CollapsedEdges: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d edges, want 1: %+v", len(got), got)
	}
	e := got[0]
	if edgeKey(e) != "checkout→orders" || e.Count != 1 {
		t.Errorf("edge = %s with %d calls, want checkout→orders with 1", edgeKey(e), e.Count)
	}
	if len(e.ViaTransport) != 1 || e.ViaTransport[0] != "istio-proxy" {
		t.Errorf("viaTransport = %v, want [istio-proxy]", e.ViaTransport)
	}
	// Client-side latency, as ServiceEdges reports it: what the caller waited
	// for, proxy overhead included.
	if e.P95 < 80*time.Millisecond {
		t.Errorf("p95 = %v, want the CALLER's ~90ms, not the callee's 70ms", e.P95)
	}
}

// Istio ambient interposes three proxy spans (client ztunnel → waypoint →
// server ztunnel). This is the depth bound's reason for existing, so it has to
// be exercised, not asserted in a comment.
func TestCollapsedEdgesAmbientThreeHops(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	insertSpans(t, store, []testSpan{
		{base, "t1", "c1", "", "GET /q", "Client", "checkout", 50 * time.Millisecond, "Unset"},
		{base, "t1", "z1", "c1", "GET /q", "Server", "ztunnel", 48 * time.Millisecond, "Unset"},
		{base, "t1", "w1", "z1", "GET /q", "Server", "global-waypoint", 46 * time.Millisecond, "Unset"},
		{base, "t1", "z2", "w1", "GET /q", "Server", "ztunnel", 44 * time.Millisecond, "Unset"},
		{base, "t1", "s1", "z2", "GET /q", "Server", "orders", 40 * time.Millisecond, "Unset"},
	})

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(time.Minute)}
	got, err := store.CollapsedEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr},
		[]string{"global-waypoint", "ztunnel"})
	if err != nil {
		t.Fatalf("CollapsedEdges: %v", err)
	}
	if len(got) != 1 || edgeKey(got[0]) != "checkout→orders" {
		t.Fatalf("got %+v, want one checkout→orders edge", got)
	}
	if len(got[0].ViaTransport) != 2 {
		t.Errorf("viaTransport = %v, want both proxy names (deduped)", got[0].ViaTransport)
	}
}

// THE test this feature had to pass before it was allowed to exist.
//
// Two callers and two backends share one proxy. Pairing the proxy's inbound
// edges with its outbound ones in aggregate — the approach
// design/2026-08-23-service-map-transport.md rejected — yields four edges, two
// of which describe calls that never happened. Per-trace ancestry must yield
// exactly the two that did.
func TestCollapsedEdgesNoCrossProduct(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	insertSpans(t, store, []testSpan{
		// checkout → proxy → orders
		{base, "t1", "c1", "", "POST /a", "Client", "checkout", 10 * time.Millisecond, "Unset"},
		{base, "t1", "p1", "c1", "POST /a", "Server", "istio-proxy", 9 * time.Millisecond, "Unset"},
		{base, "t1", "s1", "p1", "POST /a", "Server", "orders", 8 * time.Millisecond, "Unset"},
		// billing → proxy → inventory
		{base, "t2", "c2", "", "POST /b", "Client", "billing", 10 * time.Millisecond, "Unset"},
		{base, "t2", "p2", "c2", "POST /b", "Server", "istio-proxy", 9 * time.Millisecond, "Unset"},
		{base, "t2", "s2", "p2", "POST /b", "Server", "inventory", 8 * time.Millisecond, "Unset"},
	})

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(time.Minute)}
	got, err := store.CollapsedEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr}, []string{"istio-proxy"})
	if err != nil {
		t.Fatalf("CollapsedEdges: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d edges, want exactly 2 — the cross-product would be 4: %+v", len(got), got)
	}
	pairs := collapsedByPair(got)
	for _, want := range []string{"checkout→orders", "billing→inventory"} {
		if _, ok := pairs[want]; !ok {
			t.Errorf("missing real edge %s (got %+v)", want, got)
		}
	}
	for _, invented := range []string{"checkout→inventory", "billing→orders"} {
		if _, ok := pairs[invented]; ok {
			t.Errorf("invented edge %s — this is the cross-product the AEP forbids", invented)
		}
	}
}

// A cluster with no mesh must not pay for this: an empty transport set short
// -circuits before any SQL is issued.
func TestCollapsedEdgesNoTransportIsFree(t *testing.T) {
	store := startClickHouse(t)
	tr := storage.TimeRange{Start: time.Now().Add(-time.Hour), End: time.Now()}
	got, err := store.CollapsedEdges(context.Background(), storage.ServiceQuery{Tenant: "default", Range: tr}, nil)
	if err != nil {
		t.Fatalf("CollapsedEdges with no transport: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil", got)
	}
}

// A proxy calling a real service on its own behalf (a control-plane fetch, a
// health probe) is not a hop, and the walk must not report the proxy's peer as
// somebody else's dependency.
func TestCollapsedEdgesIgnoresDirectCalls(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)

	insertSpans(t, store, []testSpan{
		// A plain, unmeshed call: ServiceEdges' job, not this one.
		{base, "t1", "c1", "", "GET /x", "Client", "checkout", 10 * time.Millisecond, "Unset"},
		{base, "t1", "s1", "c1", "GET /x", "Server", "orders", 8 * time.Millisecond, "Unset"},
	})

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(time.Minute)}
	got, err := store.CollapsedEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr}, []string{"istio-proxy"})
	if err != nil {
		t.Fatalf("CollapsedEdges: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v, want none — a direct call is already an edge", got)
	}
}
