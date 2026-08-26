package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

func mapResponse(t *testing.T, fake *storagetest.Fake, cfg Config) struct {
	Services []serviceDTO     `json:"services"`
	Edges    []serviceEdgeDTO `json:"edges"`
} {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, cfg)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/service-map", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Services []serviceDTO     `json:"services"`
		Edges    []serviceEdgeDTO `json:"edges"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// The map must say which nodes are mesh transport. Without it a waypoint hop
// renders as an application dependency between two services that never call
// each other — the defect this stamping exists to make visible.
func TestServiceMapStampsTransportRole(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "auth-service", SpanCount: 5},
			{Name: "global-waypoint.istio-waypoint", SpanCount: 9},
			{Name: "istio-ingressgateway-istio.istio-edge", SpanCount: 4},
			{Name: "apisix-gateway", SpanCount: 2},
		},
	}
	roles := map[string]string{}
	for _, s := range mapResponse(t, fake, Config{}).Services {
		roles[s.Name] = s.Role
	}
	for name, want := range map[string]string{
		"auth-service":                          "",
		"apisix-gateway":                        "",
		"global-waypoint.istio-waypoint":        "transport",
		"istio-ingressgateway-istio.istio-edge": "transport",
	} {
		if roles[name] != want {
			t.Errorf("role(%q) = %q, want %q", name, roles[name], want)
		}
	}
}

// An install can rescue a service the built-ins misread — the escape hatch has
// to reach all the way through the handler, not just the classifier.
func TestServiceMapTopologyConfigOverrides(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "waypoint", SpanCount: 1},
			{Name: "edge-relay", SpanCount: 1},
		},
	}
	cfg := Config{Topology: func() topology.Config {
		return topology.Config{Transport: []string{"edge-*"}, Applications: []string{"waypoint"}}
	}}
	roles := map[string]string{}
	for _, s := range mapResponse(t, fake, cfg).Services {
		roles[s.Name] = s.Role
	}
	if roles["waypoint"] != "" {
		t.Errorf("applications override ignored: waypoint role = %q", roles["waypoint"])
	}
	if roles["edge-relay"] != "transport" {
		t.Errorf("configured transport pattern ignored: edge-relay role = %q", roles["edge-relay"])
	}
}

// A map with no mesh on it must serialize exactly as it did before this
// existed: role is omitempty, so an application carries no role key at all.
func TestServiceMapRoleOmittedForApplications(t *testing.T) {
	fake := &storagetest.Fake{Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 1}}}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/service-map", nil))
	if body := rec.Body.String(); strings.Contains(body, `"role"`) {
		t.Errorf("application node serialized a role key: %s", body)
	}
}

// The whole rider in one test: a gateway an operator called "public-edge"
// matches no built-in pattern and never will, so before this it was drawn as an
// application and its hops as dependencies. The mesh labelled its own workload;
// the map now reads that label.
func TestGatewayNamedAnythingIsTransportWhenTheMeshSaysSo(t *testing.T) {
	withEvidence := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 10},
			{Name: "public-edge", SpanCount: 40, TransportEvidence: true},
		},
	}
	resp := mapResponse(t, withEvidence, Config{})
	roles := map[string]string{}
	for _, s := range resp.Services {
		roles[s.Name] = s.Role
	}
	if roles["public-edge"] != string(topology.RoleTransport) {
		t.Errorf("public-edge role = %q, want transport — its spans carried a mesh label", roles["public-edge"])
	}
	// "service" is the default and rides the wire unstamped, as the existing
	// role test above already establishes.
	if roles["checkout"] != "" {
		t.Errorf("checkout role = %q, want unstamped (service)", roles["checkout"])
	}

	// The same names with no label on any span: unchanged, because a name is
	// all there is to go on and "public-edge" is not a pattern anyone would
	// dare add to the built-ins.
	noEvidence := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 10},
			{Name: "public-edge", SpanCount: 40},
		},
	}
	resp = mapResponse(t, noEvidence, Config{})
	for _, s := range resp.Services {
		if s.Role != "" {
			t.Errorf("%s role = %q with no evidence, want unstamped (service)", s.Name, s.Role)
		}
	}
}

// An operator who declared a workload an application keeps it, even when the
// cluster labelled it. Their word is the final one on this map.
func TestOperatorOverrideStillBeatsAMeshLabel(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "payments-gateway", SpanCount: 5, TransportEvidence: true}},
	}
	cfg := Config{Topology: func() topology.Config {
		return topology.Config{Applications: []string{"payments-gateway"}}
	}}
	resp := mapResponse(t, fake, cfg)
	if resp.Services[0].Role != "" {
		t.Errorf("role = %q, want unstamped (service) — the applications list is an override", resp.Services[0].Role)
	}
}
