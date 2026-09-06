package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Three silences, three fixes. Before this they were one state and one
// sentence, which sent an operator to check a scrape that was working.
func TestControlPlaneSilenceNamesItsOwnFix(t *testing.T) {
	for _, tc := range []struct {
		state storage.MeshControlPlaneState
		want  string
	}{
		{storage.MeshControlPlaneUnconfigured, "set mesh.controlPlane.enabled"},
		{storage.MeshControlPlaneUnreachable, "not answering"},
		{storage.MeshControlPlaneUnrecognised, "Istio-shaped"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			fake := &storagetest.Fake{ControlPlane: storage.MeshControlPlane{State: tc.state}}
			rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/control-plane")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			var resp meshControlPlaneResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Available {
				t.Error("a silent control plane reported available")
			}
			if resp.State != string(tc.state) {
				t.Errorf("state = %q, want %q", resp.State, tc.state)
			}
			if !strings.Contains(resp.Reason, tc.want) {
				t.Errorf("reason %q does not mention %q — the fix is the point", resp.Reason, tc.want)
			}
			if resp.Kind != "" {
				t.Errorf("kind = %q on a control plane nothing recognised", resp.Kind)
			}
		})
	}
}

// A recognised control plane names itself, so an operator running something
// else can see which one the numbers describe.
func TestRecognisedControlPlaneNamesItself(t *testing.T) {
	fake := &storagetest.Fake{ControlPlane: storage.MeshControlPlane{
		Available: true, State: storage.MeshControlPlaneOK, Kind: "istio",
		ConnectedProxies: 12, RejectedConfigs: 3,
	}}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/control-plane")
	var resp meshControlPlaneResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Available || resp.State != "ok" {
		t.Fatalf("available=%v state=%q, want available/ok", resp.Available, resp.State)
	}
	if resp.Kind != "istio" {
		t.Errorf("kind = %q, want istio", resp.Kind)
	}
}

// Role and namespace are what make a fleet of forty proxies readable. Both must
// come from the reads the handler already does, and neither may be invented.
func TestMeshProxiesCarryRoleAndNamespace(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "ztunnel", SpanCount: 90},
			{Name: "global-waypoint.istio-waypoint", SpanCount: 30},
			{Name: "istio-ingressgateway-istio.istio-edge", SpanCount: 40},
			// Named after neither the product nor its job: only the label the
			// mesh wrote on it can say what this is.
			{
				Name:              "edge-front",
				SpanCount:         20,
				TransportEvidence: true,
				TransportLabels:   map[string]string{"avuru.transport.istio_component": "IngressGateways"},
			},
		},
		Labels: []storage.ServiceLabel{
			{Service: "ztunnel", K8sNamespace: "istio-system"},
			{Service: "global-waypoint.istio-waypoint", K8sNamespace: "istio-waypoint"},
			{Service: "istio-ingressgateway-istio.istio-edge", K8sNamespace: "istio-edge"},
			// edge-front declares no namespace anywhere.
		},
	}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet()}, "/api/v1/mesh/proxies")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var resp meshProxiesResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]meshProxyDTO{}
	for _, p := range resp.Proxies {
		byName[p.Name] = p
	}

	for name, want := range map[string]string{
		"ztunnel":                               "ztunnel",
		"global-waypoint.istio-waypoint":        "waypoint",
		"istio-ingressgateway-istio.istio-edge": "ingress-gateway",
		"edge-front":                            "ingress-gateway",
	} {
		if got := byName[name].Role; got != want {
			t.Errorf("%s role = %q, want %q", name, got, want)
		}
	}
	if got := byName["global-waypoint.istio-waypoint"].Namespace; got != "istio-waypoint" {
		t.Errorf("waypoint namespace = %q, want istio-waypoint", got)
	}
	// A proxy whose namespace nothing declares must arrive without the key at
	// all, so the table renders a gap instead of the word "default".
	if got := byName["edge-front"].Namespace; got != "" {
		t.Errorf("edge-front namespace = %q, want empty", got)
	}
	if proxyJSON(t, body, "edge-front")["namespace"] != nil {
		t.Error("an unknown namespace was serialized rather than omitted")
	}
	// Same rule for the role: unresolvable means absent.
	if proxyJSON(t, body, "ztunnel")["role"] != "ztunnel" {
		t.Error("ztunnel lost its role on the wire")
	}
}

// proxyJSON returns one proxy object from the response as raw JSON, so a test
// can assert a key is ABSENT — which a decode into meshProxyDTO cannot show,
// since an omitted string and an empty one land in the same Go field.
func proxyJSON(t *testing.T, body, name string) map[string]any {
	t.Helper()
	var raw struct {
		Proxies []map[string]any `json:"proxies"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, p := range raw.Proxies {
		if p["name"] == name {
			return p
		}
	}
	t.Fatalf("proxy %q not in response", name)
	return nil
}
