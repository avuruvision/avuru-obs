package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// overviewSnapshot is workloadSnapshot with what a workload page reads on top
// of the row: the controller's record, the pods behind it, and a route that
// reaches it through its Service — carrying a finding of its own.
func overviewSnapshot() meshconfig.Snapshot {
	snap := workloadSnapshot()
	born := time.Date(2026, 7, 9, 10, 23, 0, 0, time.UTC)
	for i := range snap.Workloads {
		w := &snap.Workloads[i]
		if w.Name != "checkout" {
			continue
		}
		w.Labels = map[string]string{"app": "checkout", "version": "v2", "pod-template-hash": "abc12"}
		w.CreatedAt, w.CreatedFrom = born, meshconfig.CreatedFromController
		w.Annotations = map[string]string{"deployment.kubernetes.io/revision": "3"}
		w.Routes = []meshconfig.RouteRef{{Kind: meshconfig.KindHTTPRoute, Namespace: "shop", Name: "web",
			Service: "shop/checkout", Host: "checkout"}}
	}
	snap.Objects = append(snap.Objects, meshconfig.Object{
		Kind: meshconfig.KindHTTPRoute, Namespace: "shop", Name: "web",
		Findings: []meshconfig.Finding{{Code: meshconfig.CodeRouteParentMissing, Severity: meshconfig.SeverityError,
			Message: "parentRef shop/edge names a Gateway that does not exist"}},
	})
	for _, name := range []string{"checkout-abc12-x1", "checkout-abc12-x2"} {
		snap.Pods = append(snap.Pods, meshconfig.Pod{
			Namespace: "shop", Name: name, Labels: map[string]string{"app": "checkout", "pod-template-hash": "abc12"},
			OwnerKind: meshconfig.KindReplicaSet, OwnerName: "checkout-abc12", NodeName: "node-a",
			Phase: "Running", CreatedAt: born.Add(time.Hour),
			Annotations: map[string]string{"ambient.istio.io/redirection": "enabled"},
		})
	}
	return snap
}

func decodeWorkloadDetail(t *testing.T, body []byte) meshWorkloadResponse {
	t.Helper()
	var resp meshWorkloadResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

// The page reads like the cluster's own record of the workload: the
// controller's date and annotations, its labels without the template hash,
// each pod with its revision, and the routes that reach it with their own
// findings — beside the policies that already were there.
func TestMeshWorkloadDetailReadsLikeTheClusterRecord(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 60, ErrorCount: 6}},
		Labels:   []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
	}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: overviewSnapshot()}}
	rec := meshGet(t, fake, cfg, "/api/v1/mesh/workloads/shop/checkout")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeWorkloadDetail(t, rec.Body.Bytes())

	if resp.Labels["app"] != "checkout" || resp.Labels["version"] != "v2" {
		t.Errorf("labels = %v", resp.Labels)
	}
	if _, has := resp.Labels["pod-template-hash"]; has {
		t.Error("pod-template-hash is the ReplicaSet's bookkeeping, not a label of the workload")
	}
	if resp.Annotations["deployment.kubernetes.io/revision"] != "3" {
		t.Errorf("annotations = %v", resp.Annotations)
	}
	if resp.Workload == nil || resp.Workload.CreatedAt == nil || *resp.Workload.CreatedAt != "2026-07-09T10:23:00Z" ||
		resp.Workload.CreatedFrom != meshconfig.CreatedFromController {
		t.Errorf("created = %+v", resp.Workload)
	}
	if resp.Workload.App != "checkout" || resp.Workload.Version != "v2" {
		t.Errorf("app/version = %q/%q", resp.Workload.App, resp.Workload.Version)
	}
	if len(resp.Routes) != 1 || resp.Routes[0].Kind != meshconfig.KindHTTPRoute || resp.Routes[0].Host != "checkout" ||
		len(resp.Routes[0].Findings) != 1 || resp.Routes[0].Findings[0].Code != string(meshconfig.CodeRouteParentMissing) {
		t.Errorf("routes = %+v, want the HTTPRoute with its own finding", resp.Routes)
	}
	if len(resp.Pods) != 2 || resp.Pods[0].Revision != "abc12" || resp.Pods[0].CreatedAt == nil || resp.Pods[0].Node != "node-a" {
		t.Errorf("pods = %+v", resp.Pods)
	}
	// Two of two running, but one request in ten failed: degraded, and the
	// reason says which of the two it was.
	if resp.Health == nil || resp.Health.Status != "degraded" || resp.Health.Reason == "" {
		t.Errorf("health = %+v, want degraded with a reason", resp.Health)
	}
}

// The list carries app and version so the table can show them, and nothing
// heavier: labels and annotations stay on the page.
func TestMeshWorkloadsListCarriesAppAndVersionOnly(t *testing.T) {
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: overviewSnapshot()}}
	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads")
	rows := decodeWorkloads(t, rec.Body.Bytes())
	if rows["shop/checkout"].App != "checkout" || rows["shop/checkout"].Version != "v2" {
		t.Errorf("checkout = %+v", rows["shop/checkout"])
	}
	if body := rec.Body.String(); strings.Contains(body, `"annotations"`) || strings.Contains(body, `"labels"`) {
		t.Error("the list carried labels or annotations")
	}
}

// The health fold, one outcome per row, with Kiali's thresholds: a fifth of
// requests failing is down, one in a thousand is degraded, and a workload
// nobody calls is idle rather than healthy.
func TestWorkloadHealthFold(t *testing.T) {
	rate := func(v float64) *float64 { return &v }
	cases := []struct {
		name   string
		wl     meshconfig.Workload
		row    meshWorkloadDTO
		status string
	}{
		{"no pods", meshconfig.Workload{}, meshWorkloadDTO{}, "idle"},
		{"none running", meshconfig.Workload{Pods: 2}, meshWorkloadDTO{}, "down"},
		{"errors past a fifth", meshconfig.Workload{Pods: 2, RunningPods: 2}, meshWorkloadDTO{HasTraffic: true, ErrorRate: rate(0.25)}, "down"},
		{"some errors", meshconfig.Workload{Pods: 2, RunningPods: 2}, meshWorkloadDTO{HasTraffic: true, ErrorRate: rate(0.01)}, "degraded"},
		{"a pod short", meshconfig.Workload{Pods: 3, RunningPods: 2}, meshWorkloadDTO{HasTraffic: true, ErrorRate: rate(0)}, "degraded"},
		{"running, silent", meshconfig.Workload{Pods: 1, RunningPods: 1}, meshWorkloadDTO{}, "idle"},
		{"all well", meshconfig.Workload{Pods: 1, RunningPods: 1}, meshWorkloadDTO{HasTraffic: true, ErrorRate: rate(0)}, "healthy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workloadHealth(tc.wl, tc.row)
			if got.Status != tc.status || got.Reason == "" {
				t.Errorf("health = %+v, want %s with a reason", got, tc.status)
			}
		})
	}
}
