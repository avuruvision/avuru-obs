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

func securityGet(t *testing.T, fake *storagetest.Fake, cfg Config) meshSecurityResponse {
	t.Helper()
	rec := meshGet(t, fake, cfg, "/api/v1/mesh/security")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp meshSecurityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func postureRow(t *testing.T, resp meshSecurityResponse, ns, name string) meshWorkloadPostureDTO {
	t.Helper()
	for _, w := range resp.Workloads {
		if w.Namespace == ns && w.Name == name {
			return w
		}
	}
	t.Fatalf("no row for %s/%s in %+v", ns, name, resp.Workloads)
	return meshWorkloadPostureDTO{}
}

func strictShop() stubReader {
	return stubReader{snap: meshconfig.Snapshot{
		State: meshconfig.StateOK, SyncedAt: time.Now(),
		Namespaces: []meshconfig.Namespace{
			{Name: "shop", DataplaneMode: "ambient", MTLSMode: "STRICT"},
			{Name: "legacy", DataplaneMode: "sidecar", MTLSMode: "PERMISSIVE"},
		},
	}}
}

func securedWorkload(ns, name string, mtlsReq, plainReq uint64, callers ...storage.MeshCaller) storage.MeshWorkloadSecurity {
	return storage.MeshWorkloadSecurity{
		Namespace: ns, Workload: name, Reporter: "destination",
		Counts:           storage.MeshSecurityCounts{MTLSRequests: mtlsReq, PlaintextRequests: plainReq},
		PlaintextCallers: callers,
	}
}

// The screen either exists whole or not at all, like the rest of the mesh.
func TestMeshSecurityRoutesAbsentWithoutModule(t *testing.T) {
	active := modules.AllSet()
	delete(active, modules.Mesh)
	for _, path := range []string{"/api/v1/mesh/security", "/api/v1/mesh/workloads/shop/checkout/requests"} {
		if rec := meshGet(t, &storagetest.Fake{}, Config{Modules: active}, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d with the mesh module off, want 404", path, rec.Code)
		}
	}
}

// Every silence names its fix. A data plane nobody scrapes must not read as
// a fully encrypted mesh.
func TestMeshSecuritySilenceNamesItsOwnFix(t *testing.T) {
	t.Run("no infra-metrics", func(t *testing.T) {
		active := modules.Set{modules.Core: true, modules.Mesh: true}
		resp := securityGet(t, &storagetest.Fake{}, Config{Modules: active})
		if resp.Available || !strings.Contains(resp.Reason, "infra-metrics") {
			t.Errorf("available=%v reason=%q", resp.Available, resp.Reason)
		}
	})
	for _, tc := range []struct {
		state storage.MeshScrapeState
		want  string
	}{
		{storage.MeshControlPlaneUnconfigured, "mesh.dataPlane.enabled"},
		{storage.MeshControlPlaneUnreachable, "port 15020"},
		{storage.MeshControlPlaneUnrecognised, "Istio-shaped"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			fake := &storagetest.Fake{Security: storage.MeshSecurity{
				State: tc.state, TargetsUp: 1, TargetsTotal: 3, TargetsDown: []string{"shop/checkout-abc", "shop/payments-def"},
			}}
			resp := securityGet(t, fake, Config{Modules: modules.AllSet()})
			if resp.Available {
				t.Fatal("a silent data plane reported available")
			}
			if resp.State != string(tc.state) || !strings.Contains(resp.Reason, tc.want) {
				t.Errorf("state=%q reason=%q, want %q", resp.State, resp.Reason, tc.want)
			}
			if tc.state == storage.MeshControlPlaneUnreachable && !strings.Contains(resp.Reason, "shop/checkout-abc") {
				t.Errorf("unreachable reason does not name the targets: %q", resp.Reason)
			}
			if resp.Targets == nil || resp.Targets.Total != 3 {
				t.Errorf("targets = %+v, want the scrape's own report", resp.Targets)
			}
			if resp.Workloads == nil || resp.Findings == nil {
				t.Error("lists must be empty, not null")
			}
		})
	}
}

// The verdict this surface exists for: STRICT declared, plaintext observed.
func TestMeshSecurityDeclaredStrictObservedPlaintext(t *testing.T) {
	fake := &storagetest.Fake{
		Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK, Workloads: []storage.MeshWorkloadSecurity{
			securedWorkload("shop", "checkout", 100, 12, storage.MeshCaller{Namespace: "shop", Workload: "legacy-cron", Units: 12}),
			securedWorkload("shop", "payments", 40, 0),
		}},
		Services: []storage.ServiceStats{{Name: "checkout", SpanCount: 5}},
		Labels:   []storage.ServiceLabel{{Service: "checkout", K8sNamespace: "shop"}},
	}
	resp := securityGet(t, fake, Config{Modules: modules.AllSet(), MeshConfigReader: strictShop()})
	if !resp.Available || !resp.Declared {
		t.Fatalf("available=%v declared=%v", resp.Available, resp.Declared)
	}
	checkout := postureRow(t, resp, "shop", "checkout")
	if checkout.Posture != postureStrictButPlaintext {
		t.Errorf("checkout posture = %q", checkout.Posture)
	}
	if checkout.DeclaredMode != "STRICT" || checkout.DeclaredScope != "namespace" {
		t.Errorf("declared = %s/%s", checkout.DeclaredMode, checkout.DeclaredScope)
	}
	if len(checkout.Findings) != 1 || checkout.Findings[0].Code != string(meshconfig.CodeMTLSNotEnforced) {
		t.Errorf("findings = %+v", checkout.Findings)
	}
	if len(checkout.PlaintextCallers) != 1 || checkout.PlaintextCallers[0].Name != "legacy-cron" {
		t.Errorf("plaintext callers = %+v", checkout.PlaintextCallers)
	}
	if checkout.Observed == nil || checkout.Observed.MTLSShare == nil || *checkout.Observed.MTLSShare < 0.89 {
		t.Errorf("observed = %+v", checkout.Observed)
	}
	if checkout.Service != "checkout" {
		t.Errorf("service link = %q", checkout.Service)
	}
	if payments := postureRow(t, resp, "shop", "payments"); payments.Posture != postureStrictAndMTLS || len(payments.Findings) != 0 {
		t.Errorf("payments = %+v, want strict-and-mtls with no finding", payments)
	}
	// The flat list carries exactly the rows' findings.
	if len(resp.Findings) != 1 || resp.Findings[0].Ref != "shop/checkout" {
		t.Errorf("findings = %+v", resp.Findings)
	}
}

// PERMISSIVE with every caller on mutual TLS is the good news, filed as info;
// with plaintext callers it is a warning that names them.
func TestMeshSecurityPermissivePostures(t *testing.T) {
	fake := &storagetest.Fake{Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK,
		Workloads: []storage.MeshWorkloadSecurity{
			securedWorkload("legacy", "ledger", 300, 0),
			securedWorkload("legacy", "reports", 30, 9,
				storage.MeshCaller{Namespace: "ops", Workload: "probe", Units: 9}),
		}}}
	resp := securityGet(t, fake, Config{Modules: modules.AllSet(), MeshConfigReader: strictShop()})
	ledger := postureRow(t, resp, "legacy", "ledger")
	if ledger.Posture != posturePermissiveAllMTLS || len(ledger.Findings) != 1 || ledger.Findings[0].Severity != "info" {
		t.Errorf("ledger = %+v", ledger)
	}
	reports := postureRow(t, resp, "legacy", "reports")
	if reports.Posture != posturePermissivePlaintext || len(reports.Findings) != 1 {
		t.Fatalf("reports = %+v", reports)
	}
	if f := reports.Findings[0]; f.Code != string(meshconfig.CodePlaintextCallers) || !strings.Contains(f.Message, "ops/probe") {
		t.Errorf("finding = %+v, want the callers named", f)
	}
}

// With no configuration read there is no policy to compare against: the
// verdicts are observed-only and no declared field is invented.
func TestMeshSecurityObservedOnlyWithoutConfig(t *testing.T) {
	fake := &storagetest.Fake{Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK,
		Workloads: []storage.MeshWorkloadSecurity{
			securedWorkload("shop", "checkout", 100, 0),
			securedWorkload("shop", "reports", 10, 3),
		}}}
	rec := meshGet(t, fake, Config{Modules: modules.AllSet(), MeshConfigReader: meshconfig.NoopReader{}}, "/api/v1/mesh/security")
	var resp meshSecurityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Declared {
		t.Fatal("declared with a NoopReader")
	}
	if postureRow(t, resp, "shop", "checkout").Posture != postureObservedOnlyMTLS {
		t.Error("checkout is not observed-only-mtls")
	}
	if postureRow(t, resp, "shop", "reports").Posture != postureObservedOnlyPlain {
		t.Error("reports is not observed-only-plaintext")
	}
	if strings.Contains(rec.Body.String(), `"declaredMode"`) {
		t.Error("a declared mode was serialized when nothing was declared")
	}
	if len(resp.Findings) != 0 {
		t.Errorf("findings against a policy nobody read: %+v", resp.Findings)
	}
}

// A workload the traces saw, in a namespace labelled for ambient, that no
// proxy reported: a row is synthesised for it, because silence here is the
// failure.
func TestMeshSecuritySynthesisesUncarriedRows(t *testing.T) {
	fake := &storagetest.Fake{
		Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK,
			Workloads: []storage.MeshWorkloadSecurity{securedWorkload("shop", "checkout", 100, 0)}},
		Services: []storage.ServiceStats{
			{Name: "checkout", SpanCount: 5},
			{Name: "cart", SpanCount: 7},
			// Sidecar namespace: silence there is not this verdict.
			{Name: "ledger", SpanCount: 2},
			// The waypoint's own spans are not traffic a proxy carries to it.
			{Name: "global-waypoint.shop", SpanCount: 9},
		},
		Labels: []storage.ServiceLabel{
			{Service: "checkout", K8sNamespace: "shop"},
			{Service: "cart", K8sNamespace: "shop"},
			{Service: "ledger", K8sNamespace: "legacy"},
			{Service: "global-waypoint.shop", K8sNamespace: "shop"},
		},
	}
	resp := securityGet(t, fake, Config{Modules: modules.AllSet(), MeshConfigReader: strictShop()})
	cart := postureRow(t, resp, "shop", "cart")
	if cart.Posture != postureUncarried || cart.Observed != nil || cart.Service != "cart" {
		t.Errorf("cart = %+v", cart)
	}
	if len(cart.Findings) != 1 || cart.Findings[0].Code != string(meshconfig.CodeTrafficUncarried) {
		t.Errorf("cart findings = %+v", cart.Findings)
	}
	for _, w := range resp.Workloads {
		if w.Name == "ledger" || strings.HasPrefix(w.Name, "global-waypoint") {
			t.Errorf("a row was synthesised for %s/%s", w.Namespace, w.Name)
		}
	}
	// Sorted namespace/name, uncarried rows included.
	if resp.Workloads[0].Name != "cart" || resp.Workloads[1].Name != "checkout" {
		t.Errorf("rows out of order: %+v", resp.Workloads)
	}
}

// The service link resolves the sensor's dotted name back to the mesh's
// workload, and the edges carry a share only where something was classified.
func TestMeshSecurityJoinsDottedServiceNamesAndEdges(t *testing.T) {
	fake := &storagetest.Fake{
		Security: storage.MeshSecurity{Available: true, State: storage.MeshControlPlaneOK,
			Workloads: []storage.MeshWorkloadSecurity{securedWorkload("istio-waypoint", "global-waypoint", 10, 0)},
			Edges: []storage.MeshEdgeSecurity{
				{SourceNamespace: "shop", Source: "checkout", TargetNamespace: "istio-waypoint", Target: "global-waypoint",
					Reporter: "destination", Counts: storage.MeshSecurityCounts{MTLSRequests: 10}},
				{SourceNamespace: "shop", Source: "cart", TargetNamespace: "shop", Target: "checkout",
					Reporter: "destination", Counts: storage.MeshSecurityCounts{UnknownConnections: 4}},
			}},
		Services: []storage.ServiceStats{{Name: "global-waypoint.istio-waypoint", SpanCount: 5}},
		Labels:   []storage.ServiceLabel{{Service: "global-waypoint.istio-waypoint", K8sNamespace: "istio-waypoint"}},
	}
	resp := securityGet(t, fake, Config{Modules: modules.AllSet()})
	if got := postureRow(t, resp, "istio-waypoint", "global-waypoint").Service; got != "global-waypoint.istio-waypoint" {
		t.Errorf("service link = %q", got)
	}
	if len(resp.Edges) != 2 {
		t.Fatalf("edges = %+v", resp.Edges)
	}
	if resp.Edges[0].MTLSShare == nil || *resp.Edges[0].MTLSShare != 1 {
		t.Errorf("mtls edge share = %v, want 1", resp.Edges[0].MTLSShare)
	}
	if resp.Edges[1].MTLSShare != nil {
		t.Errorf("an edge with only unknown traffic got a share: %v", *resp.Edges[1].MTLSShare)
	}
}

// The job name travels: storage must be asked with the configured one.
func TestMeshSecurityPassesDataplaneJob(t *testing.T) {
	fake := &storagetest.Fake{}
	securityGet(t, fake, Config{Modules: modules.AllSet(), MeshDataplaneJob: "proxies-eu"})
	if fake.LastServiceQuery.MeshDataplaneJob != "proxies-eu" {
		t.Errorf("job = %q, want proxies-eu", fake.LastServiceQuery.MeshDataplaneJob)
	}
}
