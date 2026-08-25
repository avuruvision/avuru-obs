package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// The classifier has to run BEFORE the edge query, or the ancestry walk has no
// idea which spans to step over. Asserting on the argument is the only way to
// see that ordering from outside the handler.
func TestServiceMapPassesTransportToCollapse(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 3},
			{Name: "global-waypoint.istio-waypoint", SpanCount: 9},
			{Name: "orders", SpanCount: 4},
		},
	}
	mapResponse(t, fake, Config{})
	if got := fake.LastCollapseTransport; len(got) != 1 || got[0] != "global-waypoint.istio-waypoint" {
		t.Fatalf("CollapsedEdges got transport %v, want the one waypoint", got)
	}
}

// An unmeshed install must not pay for a feature it cannot use: no transport on
// the map means the walk is never even attempted.
func TestServiceMapSkipsCollapseWithoutMesh(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 3}, {Name: "orders", SpanCount: 1}},
	}
	mapResponse(t, fake, Config{})
	if len(fake.LastCollapseTransport) != 0 {
		t.Errorf("no mesh, but transport = %v", fake.LastCollapseTransport)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/service-map", nil))
	if body := rec.Body.String(); strings.Contains(body, `"viaTransport"`) {
		t.Errorf("unmeshed map serialized viaTransport: %s", body)
	}
}

// The recovered dependency has to arrive as a normal edge, carrying the proxy
// it was recovered across — that name is the reader's only way to tell a
// collapsed edge from one the tracer saw directly.
func TestServiceMapReturnsCollapsedEdge(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 3},
			{Name: "istio-proxy", SpanCount: 9},
			{Name: "orders", SpanCount: 4},
		},
		Collapsed: []storage.ServiceEdge{{
			Source: "checkout", Target: "orders", Count: 7,
			Provenance: "collapsed", ViaTransport: []string{"istio-proxy"},
		}},
	}
	edges := mapResponse(t, fake, Config{}).Edges
	var found *serviceEdgeDTO
	for i, e := range edges {
		if e.Source == "checkout" && e.Target == "orders" {
			found = &edges[i]
		}
	}
	if found == nil {
		t.Fatal("collapsed edge missing from the map")
	}
	if found.Calls != 7 || found.Provenance != "collapsed" {
		t.Errorf("collapsed edge = %+v, want 7 calls with provenance collapsed", *found)
	}
	if len(found.ViaTransport) != 1 || found.ViaTransport[0] != "istio-proxy" {
		t.Errorf("viaTransport = %v, want [istio-proxy]", found.ViaTransport)
	}
}

// A pair that talks both through the mesh and around it is ONE dependency.
// Reporting only the direct half would understate it by exactly the traffic the
// mesh carries — which on most meshed clusters is all of it.
func TestServiceMapMergesCollapsedIntoDirectEdge(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 3},
			{Name: "istio-proxy", SpanCount: 9},
			{Name: "orders", SpanCount: 4},
		},
		Edges: []storage.ServiceEdge{{Source: "checkout", Target: "orders", Count: 2, ErrorCount: 1}},
		Collapsed: []storage.ServiceEdge{{
			Source: "checkout", Target: "orders", Count: 5, ErrorCount: 2,
			Provenance: "collapsed", ViaTransport: []string{"istio-proxy"},
		}},
	}
	edges := mapResponse(t, fake, Config{}).Edges
	var merged []serviceEdgeDTO
	for _, e := range edges {
		if e.Source == "checkout" && e.Target == "orders" {
			merged = append(merged, e)
		}
	}
	if len(merged) != 1 {
		t.Fatalf("checkout→orders drawn %d times, want once", len(merged))
	}
	if merged[0].Calls != 7 || merged[0].ErrorCount != 3 {
		t.Errorf("merged edge = %d calls / %d errors, want 7/3", merged[0].Calls, merged[0].ErrorCount)
	}
	if merged[0].Provenance != "trace" {
		t.Errorf("provenance = %q, want trace — the pair IS directly observed", merged[0].Provenance)
	}
	if len(merged[0].ViaTransport) != 1 {
		t.Errorf("viaTransport lost on merge: %v", merged[0].ViaTransport)
	}
}
