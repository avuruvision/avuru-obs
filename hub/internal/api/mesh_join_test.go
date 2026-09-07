package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// joinFake is a store where the traces saw checkout and the data plane saw
// nine encrypted and three plaintext requests reach it under a STRICT policy.
func joinFake() *storagetest.Fake {
	return &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 60}},
		Labels:   []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
		Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK, Workloads: []storage.MeshWorkloadSecurity{{
			Namespace: "shop", Workload: "checkout", Reporter: "destination",
			Counts: storage.MeshSecurityCounts{MTLSRequests: 9, PlaintextRequests: 3},
		}}},
	}
}

// The Workloads tab starts from configuration and asks what the proxies saw;
// the answer is the same fold the Security tab runs, so the row carries the
// observed share, the verdict, and the verdict's finding in its counts.
func TestMeshWorkloadsCarryObservedAndPosture(t *testing.T) {
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: workloadSnapshot()}}
	rec := meshGet(t, joinFake(), cfg, "/api/v1/mesh/workloads")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	rows := decodeWorkloads(t, rec.Body.Bytes())

	checkout := rows["shop/checkout"]
	if checkout.ObservedMTLS == nil || checkout.ObservedMTLS.MTLS != 9 || checkout.ObservedMTLS.Plaintext != 3 ||
		checkout.ObservedMTLS.MTLSShare == nil || *checkout.ObservedMTLS.MTLSShare != 0.75 {
		t.Errorf("checkout observed = %+v", checkout.ObservedMTLS)
	}
	if checkout.Posture != postureStrictButPlaintext {
		t.Errorf("checkout posture = %q, want %q", checkout.Posture, postureStrictButPlaintext)
	}
	// One validator warning (the waypoint) plus the join's error.
	if checkout.Errors != 1 || checkout.Warnings != 1 {
		t.Errorf("checkout issues = %d errors / %d warnings, want 1 / 1", checkout.Errors, checkout.Warnings)
	}

	payments := rows["shop/payments"]
	if payments.ObservedMTLS != nil {
		t.Errorf("payments was never observed, got %+v", payments.ObservedMTLS)
	}
	if payments.Posture != postureUnknown {
		t.Errorf("payments posture = %q, want %q", payments.Posture, postureUnknown)
	}
}

// Without infra-metrics the data plane's tables do not exist: the observed
// half is absent — not zero — and no posture compares against it.
func TestMeshWorkloadsObservedAbsentWithoutInfraMetrics(t *testing.T) {
	active := modules.AllSet()
	delete(active, modules.InfraMetrics)
	cfg := Config{Modules: active, MeshConfigReader: stubReader{snap: workloadSnapshot()}}
	rows := decodeWorkloads(t, meshGet(t, joinFake(), cfg, "/api/v1/mesh/workloads").Body.Bytes())
	if rows["shop/checkout"].ObservedMTLS != nil {
		t.Errorf("observed = %+v, want absent without infra-metrics", rows["shop/checkout"].ObservedMTLS)
	}
	if rows["shop/checkout"].Errors != 0 {
		t.Errorf("errors = %d, want 0: nothing observed, nothing to enforce against", rows["shop/checkout"].Errors)
	}
}

// A workload's page lists the posture finding beside the validator's, with
// its code, so the reader sees "not enforced" where the row said so.
func TestMeshWorkloadDetailIncludesPostureFinding(t *testing.T) {
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: workloadSnapshot()}}
	rec := meshGet(t, joinFake(), cfg, "/api/v1/mesh/workloads/shop/checkout")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshWorkloadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	codes := map[string]bool{}
	for _, f := range resp.Findings {
		codes[f.Code] = true
	}
	if !codes[string(meshconfig.CodeMTLSNotEnforced)] || !codes[string(meshconfig.CodeWaypointMissing)] {
		t.Errorf("findings = %v, want both the posture's and the validator's", codes)
	}
	if resp.Workload == nil || resp.Workload.Posture != postureStrictButPlaintext {
		t.Errorf("workload = %+v", resp.Workload)
	}
}

// The namespace list counts what the join found beside what the validator
// found: shop had one configuration error and gains the posture's.
func TestMeshNamespacesCountPostureFindings(t *testing.T) {
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: workloadSnapshot()}}
	rec := meshGet(t, joinFake(), cfg, "/api/v1/mesh/namespaces")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshNamespacesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, ns := range resp.Namespaces {
		if ns.Name == "shop" && (ns.Errors != 2 || ns.Warnings != 1) {
			t.Errorf("shop issues = %d errors / %d warnings, want 2 / 1", ns.Errors, ns.Warnings)
		}
	}
}

// The Security tab now reads the policy the inventory resolved per workload:
// a selector-scoped PERMISSIVE inside a STRICT namespace is reported as the
// workload's own, not as the namespace's.
func TestMeshSecurityUsesTheWorkloadScopedPolicy(t *testing.T) {
	snap := workloadSnapshot()
	snap.Workloads[1].DeclaredMTLS = meshconfig.DeclaredMTLS{Mode: "PERMISSIVE", Source: meshconfig.SourceWorkload, Policy: "shop/checkout-legacy"}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}
	resp := securityGet(t, joinFake(), cfg)
	for _, row := range resp.Workloads {
		if row.Namespace != "shop" || row.Name != "checkout" {
			continue
		}
		if row.DeclaredMode != "PERMISSIVE" || row.DeclaredScope != meshconfig.SourceWorkload {
			t.Errorf("declared = %s/%s, want PERMISSIVE/%s", row.DeclaredMode, row.DeclaredScope, meshconfig.SourceWorkload)
		}
		if row.Posture != posturePermissivePlaintext {
			t.Errorf("posture = %q, want %q", row.Posture, posturePermissivePlaintext)
		}
		return
	}
	t.Fatal("no shop/checkout row")
}

// The validator's own sentence about skipped checks reaches the screen
// unchanged, ahead of the API's fallbacks.
func TestChecksSkippedPrefersTheValidatorsSentence(t *testing.T) {
	snap := workloadSnapshot()
	snap.PodsTruncated = true
	snap.ChecksSkipped = "MESH_POLICY_NO_MATCH was skipped: the pod list was cut at 20000"
	if got := checksSkipped(snap); got != snap.ChecksSkipped {
		t.Errorf("checksSkipped = %q, want the validator's sentence", got)
	}
}
