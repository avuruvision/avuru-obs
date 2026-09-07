package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// workloadSnapshot is a cluster with three workloads: one captured, one
// injected, and one asked into the mesh that never got there.
func workloadSnapshot() meshconfig.Snapshot {
	strict := meshconfig.DeclaredMTLS{Mode: "STRICT", Source: meshconfig.SourceNamespace, Policy: "shop/default"}
	return meshconfig.Snapshot{
		State:    meshconfig.StateOK,
		SyncedAt: time.Now(),
		Kinds:    []meshconfig.KindSync{{Kind: meshconfig.KindPod, Count: 4, SyncedAt: time.Now()}},
		Namespaces: []meshconfig.Namespace{
			{Name: "quiet", DataplaneMode: "ambient", Workloads: 1},
			{Name: "shop", DataplaneMode: "ambient", MTLSMode: "STRICT", MTLSSource: meshconfig.SourceNamespace,
				MTLSPolicy: "shop/default", Workloads: 2, Enrolled: 2},
		},
		Objects: []meshconfig.Object{{
			Kind: meshconfig.KindPeerAuthentication, Namespace: "shop", Name: "default",
			Findings: []meshconfig.Finding{{
				Code: meshconfig.CodeMTLSConflict, Severity: meshconfig.SeverityError,
				Message: "DestinationRule shop/legacy disables TLS underneath this STRICT policy",
				Hint:    "drop the DestinationRule's tls.mode DISABLE, or relax the policy",
			}},
		}},
		Workloads: []meshconfig.Workload{
			{Namespace: "quiet", Name: "worker", Kind: meshconfig.KindDeployment, DeclaredMode: "ambient",
				Pods: 1, RunningPods: 1},
			{Namespace: "shop", Name: "checkout", Kind: meshconfig.KindDeployment, DeclaredMode: "ambient",
				DataplaneMode: "ambient", Captured: true, Pods: 2, RunningPods: 2, DeclaredMTLS: strict,
				Policies: []meshconfig.PolicyRef{{Kind: meshconfig.KindPeerAuthentication, Namespace: "shop",
					Name: "default", Scope: meshconfig.SourceNamespace}},
				Services: []string{"shop/checkout"},
				Findings: []meshconfig.Finding{{Code: meshconfig.CodeWaypointMissing, Severity: meshconfig.SeverityWarning}}},
			{Namespace: "shop", Name: "payments", Kind: meshconfig.KindDeployment, DeclaredMode: "sidecar",
				DataplaneMode: "sidecar", Injected: true, Pods: 1, RunningPods: 1, DeclaredMTLS: strict,
				Waypoint: "global-waypoint", WaypointNamespace: "istio-waypoint", WaypointSource: meshconfig.SourceNamespace},
		},
	}
}

func decodeWorkloads(t *testing.T, body []byte) map[string]meshWorkloadDTO {
	t.Helper()
	var resp meshWorkloadsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]meshWorkloadDTO{}
	for _, w := range resp.Workloads {
		out[w.Namespace+"/"+w.Name] = w
	}
	return out
}

// The route belongs to mesh-config: an install with the mesh screen and
// without the cluster permission must not appear to have the data.
func TestMeshWorkloadsRouteNeedsItsOwnModule(t *testing.T) {
	active := modules.Set{modules.Core: true, modules.Mesh: true}
	cfg := Config{Modules: active, MeshConfigReader: stubReader{}}
	for _, path := range []string{"/api/v1/mesh/workloads", "/api/v1/mesh/workloads/shop/checkout", "/api/v1/mesh/waypoints/shop/wp"} {
		if rec := meshGet(t, &storagetest.Fake{}, cfg, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 with mesh-config off", path, rec.Code)
		}
	}
}

// The roster is the cluster's; traffic is decoration. A silent workload keeps
// its row, and traffic lands only on the row it belongs to — including when
// the sensor spelled the service as name.namespace, and NOT when a service of
// the same name lives in another namespace.
func TestMeshWorkloadsListsTheRosterAndDecoratesTraffic(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 60, ErrorCount: 6},
			{Name: "payments.shop", SpanCount: 30},
			{Name: "worker", SpanCount: 5},
		},
		Labels: []storage.ServiceLabel{
			{Service: "checkout", K8sNamespace: "shop"},
			{Service: "payments.shop", K8sNamespace: "shop"},
			{Service: "worker", K8sNamespace: "elsewhere"},
		},
	}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: workloadSnapshot()}}

	rec := meshGet(t, fake, cfg, "/api/v1/mesh/workloads")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	rows := decodeWorkloads(t, rec.Body.Bytes())
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	checkout := rows["shop/checkout"]
	if !checkout.HasTraffic || checkout.RatePerSec == nil || *checkout.RatePerSec <= 0 || checkout.ErrorRate == nil || *checkout.ErrorRate != 0.1 {
		t.Errorf("checkout traffic = %+v", checkout)
	}
	if !checkout.Captured || checkout.DataplaneMode != "ambient" || checkout.Errors != 0 || checkout.Warnings != 1 {
		t.Errorf("checkout row = %+v", checkout)
	}
	if checkout.DeclaredMTLS == nil || checkout.DeclaredMTLS.Source != "namespace" || checkout.DeclaredMTLS.Policy != "shop/default" {
		t.Errorf("checkout declared mTLS = %+v — a mode without its source reads as a contradiction", checkout.DeclaredMTLS)
	}
	if !rows["shop/payments"].HasTraffic {
		t.Error("payments.shop did not decorate shop/payments — the name.namespace form must join")
	}
	if got := rows["shop/payments"].WaypointNamespace; got != "istio-waypoint" {
		t.Errorf("payments waypoint namespace = %q", got)
	}
	worker, ok := rows["quiet/worker"]
	if !ok {
		t.Fatal("a workload with no traffic vanished — this is the whole point of reading pods")
	}
	if worker.HasTraffic || worker.RatePerSec != nil {
		t.Errorf("quiet/worker took the traffic of elsewhere/worker: %+v", worker)
	}
	if worker.DeclaredMTLS != nil {
		t.Error("a workload with no policy carried a declared mode — that is a guess")
	}
	if strings.Contains(rec.Body.String(), "observedMtls") {
		t.Error("observedMtls was sent; nothing measures it yet, and absent must mean not measured")
	}
}

// mode= filters on what the pods say, and declared-only is the gap between
// what was asked for and what happened.
func TestMeshWorkloadsFiltersByMode(t *testing.T) {
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: workloadSnapshot()}}
	cases := []struct {
		query string
		want  []string
		code  int
	}{
		{"", []string{"quiet/worker", "shop/checkout", "shop/payments"}, http.StatusOK},
		{"?mode=ambient", []string{"shop/checkout"}, http.StatusOK},
		{"?mode=sidecar", []string{"shop/payments"}, http.StatusOK},
		{"?mode=none", []string{"quiet/worker"}, http.StatusOK},
		{"?mode=declared-only", []string{"quiet/worker"}, http.StatusOK},
		{"?namespace=shop&mode=declared-only", []string{}, http.StatusOK},
		{"?mode=bogus", nil, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads"+tc.query)
			if rec.Code != tc.code {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.code, rec.Body.String())
			}
			if tc.code != http.StatusOK {
				return
			}
			rows := decodeWorkloads(t, rec.Body.Bytes())
			if len(rows) != len(tc.want) {
				t.Fatalf("rows = %v, want %v", rows, tc.want)
			}
			for _, key := range tc.want {
				if _, ok := rows[key]; !ok {
					t.Errorf("missing %s", key)
				}
			}
		})
	}
}

// Pods refused but the rest readable is state ok with no workloads — and the
// response must say that the empty list is a permission, not a cluster running
// nothing.
func TestMeshWorkloadsSayWhyPodsAreMissing(t *testing.T) {
	snap := workloadSnapshot()
	snap.Workloads, snap.Pods = nil, nil
	snap.MissingKinds = []string{meshconfig.KindPod}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}

	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads")
	var resp meshWorkloadsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State != "ok" || len(resp.Workloads) != 0 || len(resp.MissingKinds) != 1 {
		t.Errorf("resp = %+v", resp)
	}
	if !strings.Contains(resp.ChecksSkipped, "grant pods get/list/watch") {
		t.Errorf("checksSkipped = %q — an absence must name its fix", resp.ChecksSkipped)
	}

	// The detail is not a 404 either: the workload may well exist.
	rec = meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads/shop/checkout")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "grant pods") {
		t.Errorf("detail with pods unreadable: %d %s", rec.Code, rec.Body.String())
	}

	// Truncated is the other half: rows, plus the sentence naming the cut.
	snap = workloadSnapshot()
	snap.PodsTruncated = true
	cfg = Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}
	rec = meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads")
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.PodsTruncated || len(resp.Workloads) != 3 || !strings.Contains(resp.ChecksSkipped, "of the cluster's 4 pods") {
		t.Errorf("truncated resp = %+v", resp)
	}
	if len(resp.Kinds) != 1 || resp.Kinds[0].Kind != "Pod" || resp.Kinds[0].SyncedAt == nil {
		t.Errorf("kinds = %+v", resp.Kinds)
	}
}

// One workload whole: its findings, its policies with THEIR findings, and a
// bounded pod list that says how many it did not show.
func TestMeshWorkloadDetail(t *testing.T) {
	snap := workloadSnapshot()
	for i := 0; i < 60; i++ {
		snap.Pods = append(snap.Pods, meshconfig.Pod{
			Namespace: "shop", Name: fmt.Sprintf("checkout-abc12-%02d", i), NodeName: "node-1", Phase: "Running",
			OwnerKind: meshconfig.KindReplicaSet, OwnerName: "checkout-abc12",
			Labels:      map[string]string{"pod-template-hash": "abc12"},
			Annotations: map[string]string{"ambient.istio.io/redirection": "enabled"},
		})
	}
	// Another Deployment's pod, and a checkout pod from an unconfirmed
	// ReplicaSet, must not count.
	snap.Pods = append(snap.Pods,
		meshconfig.Pod{Namespace: "shop", Name: "payments-x-1", OwnerKind: meshconfig.KindReplicaSet, OwnerName: "payments-x",
			Labels: map[string]string{"pod-template-hash": "x"}, Containers: []string{"app", "istio-proxy"}},
		meshconfig.Pod{Namespace: "shop", Name: "checkout-other-1", OwnerKind: meshconfig.KindReplicaSet, OwnerName: "checkout-other"},
	)
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}

	rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads/shop/checkout")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshWorkloadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Workload == nil || resp.Workload.Name != "checkout" {
		t.Fatalf("workload = %+v", resp.Workload)
	}
	if resp.PodsShown != 50 || resp.PodsTotal != 60 || len(resp.Pods) != 50 {
		t.Errorf("pods shown/total = %d/%d (%d rows), want 50/60", resp.PodsShown, resp.PodsTotal, len(resp.Pods))
	}
	if p := resp.Pods[0]; !p.Captured || p.Injected || p.Node != "node-1" {
		t.Errorf("pod row = %+v", p)
	}
	if len(resp.Findings) != 1 || resp.Findings[0].Code != string(meshconfig.CodeWaypointMissing) {
		t.Errorf("findings = %+v", resp.Findings)
	}
	pol := resp.Workload.Policies
	if len(pol) != 1 || len(pol[0].Findings) != 1 || pol[0].Findings[0].Hint == "" {
		t.Errorf("policies = %+v — a covering policy's own finding belongs beside it", pol)
	}

	rec = meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads/shop/nothing")
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "no workload shop/nothing in the cluster snapshot") {
		t.Errorf("missing workload: %d %s", rec.Code, rec.Body.String())
	}
}
