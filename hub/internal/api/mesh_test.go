package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func meshGet(t *testing.T, fake *storagetest.Fake, cfg Config, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, cfg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// The screen's subject is the mesh, so it must contain the mesh and nothing
// else — an application leaking into this list would make the proxy fleet
// unreadable at exactly the scale it matters.
func TestMeshProxiesListsOnlyTransport(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 10},
			{Name: "istio-ingressgateway-istio.istio-edge", SpanCount: 40, ErrorCount: 4},
			{Name: "global-waypoint.istio-waypoint", SpanCount: 30},
		},
		Edges: []storage.ServiceEdge{
			{Source: "checkout", Target: "global-waypoint.istio-waypoint", Count: 7},
			{Source: "global-waypoint.istio-waypoint", Target: "checkout", Count: 5},
		},
	}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/proxies")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshProxiesResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Proxies) != 2 {
		t.Fatalf("got %d proxies, want the two mesh workloads: %+v", len(resp.Proxies), resp.Proxies)
	}
	byName := map[string]meshProxyDTO{}
	for _, p := range resp.Proxies {
		byName[p.Name] = p
	}
	if _, leaked := byName["checkout"]; leaked {
		t.Error("an application leaked into the proxy list")
	}
	wp := byName["global-waypoint.istio-waypoint"]
	// Traffic in and traffic out are separate numbers on purpose: a proxy
	// taking calls and forwarding none is broken in a way its own error rate
	// need not show.
	if wp.CallsIn != 7 || wp.CallsOut != 5 {
		t.Errorf("waypoint carried in=%d out=%d, want 7/5", wp.CallsIn, wp.CallsOut)
	}
}

// A control plane nobody is scraping reports zero rejected configs, which reads
// as perfect health. The endpoint must refuse to imply that.
func TestMeshControlPlaneStatesAbsence(t *testing.T) {
	fake := &storagetest.Fake{} // zero-value MeshControlPlane: nothing scraped
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/control-plane")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var resp meshControlPlaneResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Available {
		t.Fatal("reported an available control plane with nothing scraped")
	}
	if resp.Reason == "" {
		t.Error("unavailable with no reason — the reason is the actionable half")
	}
	// The numbers must be absent, not zero: `"rejectedConfigs": 0` in the body
	// is the reassuring lie this whole shape exists to prevent.
	if jsonHasKey(t, body, "rejectedConfigs") {
		t.Errorf("serialized a zeroed metric on an unavailable control plane: %s", body)
	}
}

func TestMeshControlPlaneReportsRejects(t *testing.T) {
	seen := time.Now().UTC()
	fake := &storagetest.Fake{ControlPlane: storage.MeshControlPlane{
		Available: true, LastSeen: seen,
		ConnectedProxies: 12, Pushes: 400, RejectedConfigs: 3, ConvergenceP95Ms: 250,
	}}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/control-plane")
	var resp meshControlPlaneResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Available || resp.RejectedConfigs != 3 || resp.ConnectedProxies != 12 {
		t.Errorf("control plane = %+v, want 12 proxies and 3 rejects", resp)
	}
}

// Without the module the screen does not half-exist: no routes at all.
func TestMeshRoutesAbsentWithoutModule(t *testing.T) {
	active := modules.AllSet()
	delete(active, modules.Mesh)
	for _, path := range []string{"/api/v1/mesh/proxies", "/api/v1/mesh/control-plane"} {
		rec := meshGet(t, &storagetest.Fake{}, Config{Modules: active}, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d with the mesh module off, want 404", path, rec.Code)
		}
	}
}

// The control-plane read needs the metrics tables. With infra-metrics off it
// has to say which switch is missing rather than 500 on a table that is not
// there.
func TestMeshControlPlaneWithoutInfraMetrics(t *testing.T) {
	active := modules.AllSet()
	delete(active, modules.InfraMetrics)
	rec := meshGet(t, &storagetest.Fake{}, Config{Modules: active}, "/api/v1/mesh/control-plane")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshControlPlaneResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Available || resp.Reason == "" {
		t.Errorf("want an unavailable control plane naming the missing module, got %+v", resp)
	}
}

func jsonHasKey(t *testing.T, body, key string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, ok := m[key]
	return ok
}
