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
