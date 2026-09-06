package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// stubReader serves a fixed snapshot, so these tests are about the JOIN and the
// absence handling rather than about Kubernetes.
type stubReader struct{ snap meshconfig.Snapshot }

func (s stubReader) Snapshot(context.Context) meshconfig.Snapshot { return s.snap }

// The row that justifies the module: a namespace enrolled in the mesh and
// silent must appear, with zero services, because enrolment is a label and
// silence is exactly what a broken enrolment looks like.
func TestMeshNamespacesListsConfiguredButSilentNamespaces(t *testing.T) {
	reader := stubReader{snap: meshconfig.Snapshot{
		State:    meshconfig.StateOK,
		SyncedAt: time.Now(),
		Namespaces: []meshconfig.Namespace{
			{Name: "shop", DataplaneMode: "ambient", Waypoint: "global-waypoint", WaypointNamespace: "istio-waypoint", MTLSMode: "STRICT"},
			{Name: "quiet", DataplaneMode: "ambient", MTLSMode: "STRICT"},
			{Name: "outside"},
		},
	}}
	fake := &storagetest.Fake{Labels: []storage.ServiceLabel{
		{Service: "checkout", K8sNamespace: "shop"},
		{Service: "payments", K8sNamespace: "shop"},
	}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: reader}

	rec := meshGet(t, fake, cfg, "/api/v1/mesh/namespaces")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshNamespacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]meshNamespaceDTO{}
	for _, n := range resp.Namespaces {
		byName[n.Name] = n
	}
	if got := byName["shop"]; got.Services != 2 || got.DataplaneMode != "ambient" || got.MTLSMode != "STRICT" {
		t.Errorf("shop = %+v", got)
	}
	if got := byName["shop"].WaypointNamespace; got != "istio-waypoint" {
		t.Errorf("shop waypoint namespace = %q", got)
	}
	quiet, ok := byName["quiet"]
	if !ok {
		t.Fatal("a namespace with no traffic vanished — this is the whole point of reading config")
	}
	if quiet.Services != 0 {
		t.Errorf("quiet services = %d, want 0", quiet.Services)
	}
	// Out of mesh is a real answer and must not render as a mode.
	if byName["outside"].DataplaneMode != "" {
		t.Errorf("outside got a dataplane mode: %q", byName["outside"].DataplaneMode)
	}
}

// A cluster we cannot read must say why. Rendering an empty list would report a
// mesh with no configuration, which is the reassuring lie this whole surface is
// built to avoid.
func TestMeshNamespacesStatesWhyItIsEmpty(t *testing.T) {
	reader := stubReader{snap: meshconfig.Snapshot{
		State:  meshconfig.StateForbidden,
		Reason: meshconfig.Reason(meshconfig.StateForbidden, "avuruobs-mesh-config"),
	}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: reader}

	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/namespaces")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d — an unreadable cluster is a legitimate question, not an error", rec.Code)
	}
	var resp meshNamespacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State != string(meshconfig.StateForbidden) {
		t.Errorf("state = %q", resp.State)
	}
	if resp.Reason == "" {
		t.Error("no reason given for an empty list")
	}
	if resp.SyncedAt != nil {
		t.Error("a read that never happened carried a timestamp")
	}
}

// The routes belong to mesh-config, not mesh: an install with the screen and
// without the permission must not appear to have the data.
func TestMeshNamespacesRouteNeedsItsOwnModule(t *testing.T) {
	active := modules.Set{modules.Core: true, modules.Mesh: true}
	cfg := Config{Modules: active, MeshConfigReader: stubReader{}}

	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/namespaces")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404 with mesh-config off", rec.Code)
	}
}

// The config browser: a list stays a list, and only a single-object request
// carries the spec. A list of two hundred objects with their specs inlined is a
// payload nobody reads.
func TestMeshConfigSendsSpecOnlyForOneObject(t *testing.T) {
	reader := stubReader{snap: meshconfig.Snapshot{
		State:    meshconfig.StateOK,
		SyncedAt: time.Now(),
		Objects: []meshconfig.Object{
			{Kind: "HTTPRoute", Namespace: "shop", Name: "web", Spec: map[string]any{"rules": []any{}},
				Findings: []meshconfig.Finding{{
					Code: meshconfig.CodeRouteBackendMissing, Severity: meshconfig.SeverityError,
					Message: "backendRef names Service shop/payments, which does not exist",
					Hint:    "create the Service or fix the reference", Ref: "shop/payments",
				}}},
			{Kind: "Gateway", Namespace: "istio-edge", Name: "public", Spec: map[string]any{"gatewayClassName": "istio"}},
		},
	}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: reader}

	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/config")
	var list meshConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Objects) != 2 {
		t.Fatalf("objects = %d, want 2", len(list.Objects))
	}
	for _, o := range list.Objects {
		if o.Spec != nil {
			t.Errorf("%s/%s carried its spec in a list response", o.Kind, o.Name)
		}
	}
	// Findings ride the list: they are what makes it scannable.
	var withFindings int
	for _, o := range list.Objects {
		withFindings += len(o.Findings)
	}
	if withFindings != 1 {
		t.Errorf("findings in list = %d, want 1", withFindings)
	}

	rec = meshGet(t, &storagetest.Fake{}, cfg,
		"/api/v1/mesh/config?kind=HTTPRoute&namespace=shop&name=web")
	var one meshConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(one.Objects) != 1 {
		t.Fatalf("objects = %d, want the one asked for", len(one.Objects))
	}
	if one.Objects[0].Spec == nil {
		t.Error("a single-object request came back without its spec")
	}
	f := one.Objects[0].Findings
	if len(f) != 1 || f[0].Hint == "" {
		t.Errorf("findings = %+v — a finding without a hint sends the reader looking", f)
	}
}

// Findings roll up onto the namespace rows, which is what makes a long
// namespace list scannable: the row that needs attention says so.
func TestNamespaceRowsCountTheirFindings(t *testing.T) {
	reader := stubReader{snap: meshconfig.Snapshot{
		State:      meshconfig.StateOK,
		SyncedAt:   time.Now(),
		Namespaces: []meshconfig.Namespace{{Name: "shop", DataplaneMode: "ambient"}},
		Objects: []meshconfig.Object{
			{Kind: "HTTPRoute", Namespace: "shop", Name: "a", Findings: []meshconfig.Finding{
				{Code: meshconfig.CodeRouteBackendMissing, Severity: meshconfig.SeverityError},
				{Code: meshconfig.CodeHostUnresolved, Severity: meshconfig.SeverityWarning},
			}},
			{Kind: "Gateway", Namespace: "shop", Name: "b", Findings: []meshconfig.Finding{
				{Code: meshconfig.CodeGatewayNoRoutes, Severity: meshconfig.SeverityWarning},
			}},
		},
	}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: reader}

	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/namespaces")
	var resp meshNamespacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := resp.Namespaces[0]; got.Errors != 1 || got.Warnings != 2 {
		t.Errorf("shop findings = %d errors / %d warnings, want 1/2", got.Errors, got.Warnings)
	}
}
