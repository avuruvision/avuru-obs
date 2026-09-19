package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// clusterSnapshot is a two-project cluster: "shop" is what the demo tenant's
// telemetry reports, "other" belongs to somebody else, and istio-system holds
// the mesh-wide policy neither project owns.
func clusterSnapshot() meshconfig.Snapshot {
	return meshconfig.Snapshot{
		State:    meshconfig.StateOK,
		SyncedAt: time.Now(),
		Kinds: []meshconfig.KindSync{
			{Kind: meshconfig.KindNamespace, Count: 3},
			{Kind: meshconfig.KindPod, Count: 2},
		},
		Namespaces: []meshconfig.Namespace{
			{Name: "istio-system"},
			{Name: "other", DataplaneMode: "ambient"},
			{Name: "shop", DataplaneMode: "ambient"},
		},
		Objects: []meshconfig.Object{
			{Kind: meshconfig.KindPeerAuthentication, Namespace: "istio-system", Name: "default"},
			{Kind: meshconfig.KindAuthorizationPolicy, Namespace: "other", Name: "deny-all"},
			{Kind: meshconfig.KindAuthorizationPolicy, Namespace: "shop", Name: "allow-checkout"},
			{Kind: meshconfig.KindGateway, Namespace: "other", Name: "waypoint"},
		},
		Pods: []meshconfig.Pod{
			{Namespace: "other", Name: "api-1", OwnerKind: meshconfig.KindDeployment, OwnerName: "api"},
			{Namespace: "shop", Name: "checkout-1", OwnerKind: meshconfig.KindDeployment, OwnerName: "checkout"},
		},
		Workloads: []meshconfig.Workload{
			{Namespace: "other", Name: "api", Kind: meshconfig.KindDeployment},
			{Namespace: "shop", Name: "checkout", Kind: meshconfig.KindDeployment},
		},
		Services: []meshconfig.Service{
			{Namespace: "other", Name: "api", Waypoint: "waypoint"},
			{Namespace: "shop", Name: "checkout"},
		},
	}
}

// scopedMeshMux serves the mesh routes to an anonymous viewer granted only
// "demo" — the shape of the shared demo login — over a store whose telemetry
// for that tenant names the "shop" namespace.
func scopedMeshMux(t *testing.T, fake *storagetest.Fake) *http.ServeMux {
	t.Helper()
	anon := &auth.Identity{Name: "Anonymous", Anonymous: true,
		Grants: []auth.Grant{{Scope: "demo", Role: auth.RoleViewer}}}
	svc := auth.NewService(func() storage.Store { return fake }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		Auth: svc, AnonymousIdentity: anon,
		Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: clusterSnapshot()},
	})
	return mux
}

func demoGet(t *testing.T, mux *http.ServeMux, path string, into any) int {
	t.Helper()
	w := authDo(mux, http.MethodGet, path, nil, map[string]string{"X-Avuru-Tenant": "demo"})
	if into != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return w.Code
}

// The bug this file exists for: the demo viewer, granted one project, could
// open Service Mesh and read every namespace, workload and policy on the
// cluster. A project-scoped account sees the cluster only where its own
// telemetry reaches.
func TestMeshRoutesNarrowToTheProjectsNamespacesForAScopedViewer(t *testing.T) {
	fake := &storagetest.Fake{
		Tenants: []string{"demo", "other"},
		Labels:  []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
	}
	mux := scopedMeshMux(t, fake)

	var namespaces meshNamespacesResponse
	if code := demoGet(t, mux, "/api/v1/mesh/namespaces", &namespaces); code != http.StatusOK {
		t.Fatalf("namespaces: %d", code)
	}
	if len(namespaces.Namespaces) != 1 || namespaces.Namespaces[0].Name != "shop" {
		t.Errorf("namespaces = %+v, want only shop", namespaces.Namespaces)
	}
	for _, k := range namespaces.Kinds {
		if k.Kind == meshconfig.KindNamespace && k.Count != 1 {
			t.Errorf("namespace kind count = %d, want 1: the cluster's size leaked through a number", k.Count)
		}
	}

	var workloads meshWorkloadsResponse
	if code := demoGet(t, mux, "/api/v1/mesh/workloads", &workloads); code != http.StatusOK {
		t.Fatalf("workloads: %d", code)
	}
	if len(workloads.Workloads) != 1 || workloads.Workloads[0].Namespace != "shop" {
		t.Errorf("workloads = %+v, want only shop/checkout", workloads.Workloads)
	}

	var config meshConfigResponse
	if code := demoGet(t, mux, "/api/v1/mesh/config", &config); code != http.StatusOK {
		t.Fatalf("config: %d", code)
	}
	if len(config.Objects) != 1 || config.Objects[0].Namespace != "shop" {
		t.Errorf("config objects = %+v, want only shop's policy", config.Objects)
	}
	// Asking for the other project's object by name must not answer either.
	if code := demoGet(t, mux, "/api/v1/mesh/config?namespace=other&kind=AuthorizationPolicy&name=deny-all", &config); code != http.StatusOK || len(config.Objects) != 0 {
		t.Errorf("other project's object: %d %+v, want 200 with no object", code, config.Objects)
	}

	if code := demoGet(t, mux, "/api/v1/mesh/workloads/shop/checkout", nil); code != http.StatusOK {
		t.Errorf("own workload: %d, want 200", code)
	}
	if code := demoGet(t, mux, "/api/v1/mesh/workloads/other/api", nil); code != http.StatusNotFound {
		t.Errorf("other project's workload: %d, want 404", code)
	}

	// The other project's waypoint, and what it serves, are not in this
	// caller's view: absent, like a workload outside it, not "bound to nothing".
	if code := demoGet(t, mux, "/api/v1/mesh/waypoints/other/waypoint", nil); code != http.StatusNotFound {
		t.Errorf("other project's waypoint: %d, want 404", code)
	}
}

// An identity that may see every project is the cluster operator, and the
// module's whole point for them is the namespace with no telemetry: enrolment
// is a label, and silence is what a broken enrolment looks like.
func TestMeshRoutesStayClusterWideForAWildcardViewer(t *testing.T) {
	fake := &storagetest.Fake{
		Tenants: []string{"demo", "other"},
		Labels:  []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
	}
	anon := &auth.Identity{Name: "Anonymous", Anonymous: true,
		Grants: []auth.Grant{{Scope: "*", Role: auth.RoleViewer}}}
	svc := auth.NewService(func() storage.Store { return fake }, time.Hour)
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{
		Auth: svc, AnonymousIdentity: anon,
		Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: clusterSnapshot()},
	})
	var namespaces meshNamespacesResponse
	if code := demoGet(t, mux, "/api/v1/mesh/namespaces", &namespaces); code != http.StatusOK {
		t.Fatalf("namespaces: %d", code)
	}
	if len(namespaces.Namespaces) != 3 {
		t.Errorf("wildcard viewer got %d namespaces, want the cluster's 3", len(namespaces.Namespaces))
	}
}

// When the store cannot say which namespaces the project reaches, the answer
// is an error — never the whole cluster, and never a silent empty list that
// reads as "your project has no mesh".
func TestMeshRoutesFailClosedWhenTheProjectsNamespacesCannotBeRead(t *testing.T) {
	fake := &storagetest.Fake{
		Tenants:   []string{"demo"},
		LabelsErr: errors.New("clickhouse is away"),
	}
	mux := scopedMeshMux(t, fake)
	w := authDo(mux, http.MethodGet, "/api/v1/mesh/namespaces", nil, map[string]string{"X-Avuru-Tenant": "demo"})
	if w.Code < 500 {
		t.Fatalf("namespaces with the labels read failing: %d %s, want a 5xx", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "other") {
		t.Errorf("the cluster leaked into the error body: %s", w.Body.String())
	}
}
