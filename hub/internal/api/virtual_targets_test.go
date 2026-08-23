package api

import (
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// The node id has to be readable AND unable to collide with a service.name —
// a collision would merge a real service and a database into one graph node.
func TestVirtualNodeName(t *testing.T) {
	for _, tc := range []struct {
		system, peer, want string
	}{
		{"postgresql", "orders-db", "postgresql://orders-db"},
		{"redis", "session-cache", "redis://session-cache"},
		{"kafka", "broker-0.kafka.svc", "kafka://broker-0.kafka.svc"},
		// No peer recorded: the bare system is less specific but still true.
		{"mysql", "", "mysql"},
		// No system means nothing was classified — there is no target here.
		{"", "orders-db", ""},
	} {
		if got := virtualNodeName(tc.system, tc.peer); got != tc.want {
			t.Errorf("virtualNodeName(%q, %q) = %q, want %q", tc.system, tc.peer, got, tc.want)
		}
	}
}

// A map with no virtual targets must come back exactly as it went in — an
// install with no database is entitled to the bytes it had before.
func TestVirtualTargetsAbsentLeavesMapUnchanged(t *testing.T) {
	services := []serviceDTO{{Name: "checkout", SpanCount: 3}}
	edges := []serviceEdgeDTO{{Source: "gateway", Target: "checkout", Calls: 3}}
	gotS, gotE := appendVirtualTargets(services, edges, nil, time.Minute)
	if len(gotS) != 1 || len(gotE) != 1 {
		t.Fatalf("got %d services / %d edges, want 1 / 1", len(gotS), len(gotE))
	}
	if gotS[0].Role != "" || gotS[0].Kind != "" {
		t.Errorf("application gained role %q kind %q", gotS[0].Role, gotS[0].Kind)
	}
}

// Two services sharing one cache is the blast radius the map exists to show:
// one node, two edges into it — not two nodes.
func TestVirtualTargetsFoldSharedDependency(t *testing.T) {
	targets := []storage.VirtualTarget{
		{Service: "checkout", Kind: "cache", System: "redis", Peer: "cache", Direction: "out",
			Count: 60, P50: 2 * time.Millisecond, P95: 5 * time.Millisecond},
		{Service: "payments", Kind: "cache", System: "redis", Peer: "cache", Direction: "out",
			Count: 40, ErrorCount: 4, P50: 3 * time.Millisecond, P95: 30 * time.Millisecond},
	}
	services, edges := appendVirtualTargets(
		[]serviceDTO{{Name: "checkout"}, {Name: "payments"}}, nil, targets, time.Minute)

	if len(services) != 3 {
		t.Fatalf("got %d services, want 3 (2 apps + 1 cache)", len(services))
	}
	node := services[2]
	if node.Name != "redis://cache" || node.Role != RoleVirtual || node.Kind != "cache" {
		t.Fatalf("node = %+v", node)
	}
	if node.SpanCount != 100 {
		t.Errorf("spanCount = %d, want 100 (both callers)", node.SpanCount)
	}
	if node.RatePerSec != 100.0/60.0 {
		t.Errorf("ratePerSec = %v, want calls per second over the window", node.RatePerSec)
	}
	if node.ErrorRate != 0.04 {
		t.Errorf("errorRate = %v, want 4/100 across both callers", node.ErrorRate)
	}
	// Quantiles cannot be averaged, so the node reports the worst caller's p95
	// — a number something actually measured.
	if node.P95Ms != 30 {
		t.Errorf("p95Ms = %v, want the slowest caller's 30ms", node.P95Ms)
	}
	if node.P99Ms != 0 {
		t.Errorf("p99Ms = %v, want 0 — nothing measured a p99 here", node.P99Ms)
	}
	if len(edges) != 2 {
		t.Fatalf("got %d edges, want one per caller", len(edges))
	}
	for _, e := range edges {
		if e.Target != "redis://cache" {
			t.Errorf("edge %+v does not point at the cache", e)
		}
		if e.Provenance != "trace" {
			t.Errorf("edge provenance = %q, want trace — it came from spans", e.Provenance)
		}
	}
}

// A broker with only inbound edges reads as a dead end. The consume side runs
// the other way, so producer → broker → consumer is drawn whole.
func TestVirtualTargetsDrawBothEndsOfABroker(t *testing.T) {
	targets := []storage.VirtualTarget{
		{Service: "orders", Kind: "queue", System: "kafka", Peer: "broker", Direction: "out", Count: 10},
		{Service: "shipping", Kind: "queue", System: "kafka", Peer: "broker", Direction: "in", Count: 10},
	}
	services, edges := appendVirtualTargets(
		[]serviceDTO{{Name: "orders"}, {Name: "shipping"}}, nil, targets, time.Minute)

	if len(services) != 3 {
		t.Fatalf("got %d services, want one broker node for both directions", len(services))
	}
	var in, out bool
	for _, e := range edges {
		if e.Source == "orders" && e.Target == "kafka://broker" {
			out = true
		}
		if e.Source == "kafka://broker" && e.Target == "shipping" {
			in = true
		}
	}
	if !out || !in {
		t.Fatalf("edges = %+v, want orders→broker and broker→shipping", edges)
	}
}

// The collision guard: a duplicate node id breaks the graph outright, so a
// target whose URI is already a service name is dropped with its edges rather
// than merged into it.
func TestVirtualTargetsSkipNameCollision(t *testing.T) {
	services, edges := appendVirtualTargets(
		[]serviceDTO{{Name: "redis://cache"}},
		nil,
		[]storage.VirtualTarget{
			{Service: "checkout", Kind: "cache", System: "redis", Peer: "cache", Direction: "out", Count: 5},
		},
		time.Minute,
	)
	if len(services) != 1 {
		t.Fatalf("got %d services, want the real one only", len(services))
	}
	if len(edges) != 0 {
		t.Fatalf("got %d edges, want none — the target was dropped", len(edges))
	}
}

// End to end through the handler: the map response carries the derived node and
// its edge, and the applications beside it are untouched.
func TestServiceMapIncludesVirtualTargets(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 12}},
		Virtual: []storage.VirtualTarget{
			{Service: "checkout", Kind: "database", System: "postgresql", Peer: "orders-db",
				Direction: "out", Count: 12, P95: 40 * time.Millisecond},
		},
	}
	resp := mapResponse(t, fake, Config{})

	kinds := map[string]string{}
	for _, s := range resp.Services {
		kinds[s.Name] = s.Kind
	}
	if kinds["postgresql://orders-db"] != "database" {
		t.Fatalf("services = %+v, want a database node", resp.Services)
	}
	if kinds["checkout"] != "" {
		t.Errorf("application gained kind %q", kinds["checkout"])
	}
	if len(resp.Edges) != 1 || resp.Edges[0].Target != "postgresql://orders-db" {
		t.Fatalf("edges = %+v, want checkout → the database", resp.Edges)
	}
}
