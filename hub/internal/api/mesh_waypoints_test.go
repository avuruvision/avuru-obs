package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// waypointSnapshot binds one waypoint at all three levels the mesh allows —
// a whole namespace, one Service, one workload — and deploys it.
func waypointSnapshot() meshconfig.Snapshot {
	return meshconfig.Snapshot{
		State:    meshconfig.StateOK,
		SyncedAt: time.Now(),
		Namespaces: []meshconfig.Namespace{
			{Name: "shop", DataplaneMode: "ambient", Waypoint: "global-waypoint", WaypointNamespace: "istio-waypoint"},
			{Name: "quiet", DataplaneMode: "ambient"},
			// Same waypoint NAME in another namespace: a different waypoint.
			{Name: "other", DataplaneMode: "ambient", Waypoint: "global-waypoint", WaypointNamespace: "other"},
		},
		Objects: []meshconfig.Object{{
			Kind: meshconfig.KindGateway, Namespace: "istio-waypoint", Name: "global-waypoint",
			Labels: map[string]string{"istio.io/waypoint-for": "all"},
			Spec:   map[string]any{"gatewayClassName": "istio-waypoint"},
		}},
		Services: []meshconfig.Service{
			{Namespace: "quiet", Name: "api", Waypoint: "global-waypoint", WaypointNamespace: "istio-waypoint", WaypointSource: meshconfig.SourceService},
			{Namespace: "shop", Name: "checkout", Waypoint: "global-waypoint", WaypointNamespace: "istio-waypoint", WaypointSource: meshconfig.SourceNamespace},
		},
		Workloads: []meshconfig.Workload{
			{Namespace: "quiet", Name: "batch", Waypoint: "global-waypoint", WaypointNamespace: "istio-waypoint", WaypointSource: meshconfig.SourceWorkload},
			{Namespace: "quiet", Name: "worker"},
		},
		Pods: []meshconfig.Pod{{
			Namespace: "istio-waypoint", Name: "global-waypoint-abc", Phase: "Running",
			Labels: map[string]string{"gateway.networking.k8s.io/gateway-name": "global-waypoint"},
		}},
	}
}

func getWaypoint(t *testing.T, snap meshconfig.Snapshot, path string) (int, meshWaypointResponse, string) {
	t.Helper()
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}
	rec := meshGet(t, &storagetest.Fake{}, cfg, path)
	var resp meshWaypointResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, resp, rec.Body.String()
}

// A waypoint says what it serves, from every level a binding can be made at,
// and only the bindings that name THIS waypoint — not a namesake elsewhere.
func TestMeshWaypointListsWhatItServes(t *testing.T) {
	code, resp, body := getWaypoint(t, waypointSnapshot(), "/api/v1/mesh/waypoints/istio-waypoint/global-waypoint")
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	if resp.Waypoint == nil || resp.Waypoint.Scope != "all" {
		t.Fatalf("waypoint = %+v, want scope all from its Gateway", resp.Waypoint)
	}
	if resp.Waypoint.Running == nil || !*resp.Waypoint.Running {
		t.Errorf("running = %v, want true: a Running pod carries the gateway-name label", resp.Waypoint.Running)
	}
	want := map[string][]string{
		"namespaces": {"shop"},
		"services":   {"quiet/api", "shop/checkout"},
		"workloads":  {"quiet/batch"},
	}
	got := map[string][]string{"namespaces": resp.Namespaces, "services": resp.Services, "workloads": resp.Workloads}
	for level, list := range want {
		if strings.Join(got[level], ",") != strings.Join(list, ",") {
			t.Errorf("%s = %v, want %v", level, got[level], list)
		}
	}
}

// Deployed and not running, not deployed at all, and pods unreadable are three
// different answers, and none of them is "false".
func TestMeshWaypointRunningIsHonest(t *testing.T) {
	notRunning := waypointSnapshot()
	notRunning.Pods[0].Phase = "Pending"
	_, resp, _ := getWaypoint(t, notRunning, "/api/v1/mesh/waypoints/istio-waypoint/global-waypoint")
	if resp.Waypoint.Running == nil || *resp.Waypoint.Running {
		t.Errorf("a Pending pod counted as running: %v", resp.Waypoint.Running)
	}

	unreadable := waypointSnapshot()
	unreadable.Pods = nil
	unreadable.MissingKinds = []string{meshconfig.KindPod}
	_, resp, _ = getWaypoint(t, unreadable, "/api/v1/mesh/waypoints/istio-waypoint/global-waypoint")
	if resp.Waypoint.Running != nil {
		t.Errorf("running = %v with pods unreadable; could not look is not false", *resp.Waypoint.Running)
	}

	// Bound to, never deployed: the bindings are listed and the scope is
	// absent, because there is no Gateway to read one from.
	undeployed := waypointSnapshot()
	undeployed.Objects = nil
	_, resp, _ = getWaypoint(t, undeployed, "/api/v1/mesh/waypoints/istio-waypoint/global-waypoint")
	if resp.Waypoint == nil || resp.Waypoint.Scope != "" || len(resp.Namespaces) != 1 {
		t.Errorf("undeployed waypoint = %+v / %v", resp.Waypoint, resp.Namespaces)
	}
}

// Nothing of that name and nothing bound to it is a 404 that says so; a
// cluster that could not be read is the usual 200 with its state.
func TestMeshWaypointAbsence(t *testing.T) {
	code, _, body := getWaypoint(t, waypointSnapshot(), "/api/v1/mesh/waypoints/istio-waypoint/ghost")
	if code != http.StatusNotFound || !strings.Contains(body, "no waypoint istio-waypoint/ghost") {
		t.Errorf("unknown waypoint: %d %s", code, body)
	}

	forbidden := meshconfig.Snapshot{State: meshconfig.StateForbidden, Reason: meshconfig.Reason(meshconfig.StateForbidden, "avuruobs-mesh-config")}
	code, resp, body := getWaypoint(t, forbidden, "/api/v1/mesh/waypoints/istio-waypoint/global-waypoint")
	if code != http.StatusOK || resp.State != "forbidden" || resp.Reason == "" || resp.Waypoint != nil {
		t.Errorf("forbidden cluster: %d %s", code, body)
	}
}
