package api

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// An edge the destination's proxy reported gets its share; one it did not
// stays unmarked; a collapsed edge stays unmarked whatever the proxies said
// about its hops.
func TestServiceMapEdgesCarryMTLSShareWhereMeasured(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 3},
			{Name: "payments", SpanCount: 4},
			{Name: "ledger", SpanCount: 4},
			{Name: "global-waypoint.istio-waypoint", SpanCount: 9},
		},
		Labels: []storage.ServiceLabel{
			{Service: "checkout", K8sNamespace: "shop"},
			{Service: "payments", K8sNamespace: "shop"},
			{Service: "ledger", K8sNamespace: "legacy"},
			{Service: "global-waypoint.istio-waypoint", K8sNamespace: "istio-waypoint"},
		},
		Edges: []storage.ServiceEdge{
			{Source: "checkout", Target: "payments", Count: 10},
			{Source: "checkout", Target: "ledger", Count: 5},
			{Source: "checkout", Target: "global-waypoint.istio-waypoint", Count: 9},
		},
		Collapsed: []storage.ServiceEdge{{
			Source: "payments", Target: "ledger", Count: 7,
			Provenance: "collapsed", ViaTransport: []string{"global-waypoint.istio-waypoint"},
		}},
		Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK,
			Edges: []storage.MeshEdgeSecurity{
				{SourceNamespace: "shop", Source: "checkout", TargetNamespace: "shop", Target: "payments",
					Reporter: "destination", Counts: storage.MeshSecurityCounts{MTLSRequests: 8, PlaintextRequests: 2}},
				// The suffix is the sensor's; the proxies name the waypoint plainly.
				{SourceNamespace: "shop", Source: "checkout", TargetNamespace: "istio-waypoint", Target: "global-waypoint",
					Reporter: "destination", Counts: storage.MeshSecurityCounts{MTLSRequests: 9}},
				// Reported for the collapsed pair's hops, and must NOT land on it.
				{SourceNamespace: "shop", Source: "payments", TargetNamespace: "legacy", Target: "ledger",
					Reporter: "destination", Counts: storage.MeshSecurityCounts{PlaintextRequests: 7}},
			}},
	}
	edges := mapResponse(t, fake, Config{Modules: modules.AllSet()}).Edges
	find := func(src, dst string) serviceEdgeDTO {
		t.Helper()
		for _, e := range edges {
			if e.Source == src && e.Target == dst {
				return e
			}
		}
		t.Fatalf("no edge %s -> %s in %+v", src, dst, edges)
		return serviceEdgeDTO{}
	}
	if e := find("checkout", "payments"); e.MTLSShare == nil || *e.MTLSShare != 0.8 || e.PlaintextCalls != 2 {
		t.Errorf("checkout->payments = share %v plaintext %d, want 0.8 / 2", e.MTLSShare, e.PlaintextCalls)
	}
	if e := find("checkout", "global-waypoint.istio-waypoint"); e.MTLSShare == nil || *e.MTLSShare != 1 {
		t.Errorf("the dotted waypoint name did not join: %+v", e)
	}
	if e := find("checkout", "ledger"); e.MTLSShare != nil || e.PlaintextCalls != 0 {
		t.Errorf("an unreported edge was marked: %+v", e)
	}
	if e := find("payments", "ledger"); e.MTLSShare != nil || e.PlaintextCalls != 0 {
		t.Errorf("a collapsed edge was marked with one hop's security: %+v", e)
	}
}

// Without the data plane available nothing is stamped, and the fake's edges
// must not leak through an `available: false` read.
func TestServiceMapEdgesUnmarkedWhenDataPlaneSilent(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 3}, {Name: "payments", SpanCount: 4}},
		Labels:   []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}, {Service: "payments", K8sNamespace: "shop"}},
		Edges:    []storage.ServiceEdge{{Source: "checkout", Target: "payments", Count: 10}},
		Security: storage.MeshSecurity{State: storage.MeshControlPlaneUnconfigured,
			Edges: []storage.MeshEdgeSecurity{{SourceNamespace: "shop", Source: "checkout", TargetNamespace: "shop", Target: "payments",
				Counts: storage.MeshSecurityCounts{MTLSRequests: 8}}}},
	}
	for _, cfg := range []Config{
		{Modules: modules.AllSet()},
		{Modules: modules.Set{modules.Core: true, modules.Mesh: true}},
	} {
		edges := mapResponse(t, fake, cfg).Edges
		if len(edges) != 1 || edges[0].MTLSShare != nil {
			t.Errorf("edges = %+v, want one unmarked edge", edges)
		}
	}
}
