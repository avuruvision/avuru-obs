//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// The proxy scrape's resource shape after groupbyattrs: the job as
// service.name, ip:port as the instance, and the pod's identity lifted to the
// resource. `tenant` rides avuru.tenant, which the Tenant column derives from.
func dataplaneRes(tenant, instance, ns, pod string) map[string]string {
	return map[string]string{
		"avuru.tenant": tenant, "service.name": "mesh-dataplane", "service.instance.id": instance,
		"k8s.namespace.name": ns, "k8s.pod.name": pod,
	}
}

func istioAttrs(reporter, src, dst, policy string) map[string]string {
	return map[string]string{
		"reporter":                       reporter,
		"source_workload_namespace":      "shop",
		"source_workload":                src,
		"destination_workload_namespace": "shop",
		"destination_workload":           dst,
		"destination_service":            dst + ".shop.svc.cluster.local",
		"connection_security_policy":     policy,
		"request_protocol":               "http",
		"response_code":                  "200",
		"response_flags":                 "-",
		"source_version":                 "v1",
		"destination_version":            "v1",
	}
}

func meshQuery(base time.Time, tenant string) storage.ServiceQuery {
	return storage.ServiceQuery{Tenant: tenant, Range: storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}}
}

// One edge reported by both sides, and a cumulative counter scraped twice: the
// destination's account wins, and the answer is the counter's rise, not the
// sum of its samples. Also the tenant seam: another tenant sees nothing.
func TestMeshSecurityIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	apiRes := dataplaneRes("default", "10.0.0.2:15020", "shop", "api-7d9f")
	webRes := dataplaneRes("default", "10.0.0.3:15020", "shop", "web-5c1a")

	// api's own sidecar: 100 -> 130 over two scrapes. 30, never 230.
	insertSum(t, store, base.Add(1*time.Minute), meshRequestsMetric, apiRes,
		istioAttrs("destination", "web", "api", "mutual_tls"), 100)
	insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, apiRes,
		istioAttrs("destination", "web", "api", "mutual_tls"), 130)
	// web's sidecar reporting the same edge from its side: dropped.
	insertSum(t, store, base.Add(1*time.Minute), meshRequestsMetric, webRes,
		istioAttrs("source", "web", "api", "mutual_tls"), 500)
	insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, webRes,
		istioAttrs("source", "web", "api", "mutual_tls"), 560)
	// A plaintext caller, seen by api's sidecar: 0 -> 7.
	insertSum(t, store, base.Add(1*time.Minute), meshRequestsMetric, apiRes,
		istioAttrs("destination", "legacy", "api", "none"), 0)
	insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, apiRes,
		istioAttrs("destination", "legacy", "api", "none"), 7)
	// Traffic leaving the mesh has no destination workload to fix: dropped.
	insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, webRes,
		istioAttrs("source", "web", "unknown", "none"), 9)
	// Scrape report: api's proxy answered, web's did not.
	insertGauge(t, store, base.Add(2*time.Minute), "up", apiRes, 1)
	insertGauge(t, store, base.Add(2*time.Minute), "up", webRes, 0)

	sec, err := store.MeshSecurity(ctx, meshQuery(base, "default"))
	if err != nil {
		t.Fatalf("MeshSecurity: %v", err)
	}
	if !sec.Available || sec.State != storage.MeshControlPlaneOK {
		t.Fatalf("available=%v state=%q with series present", sec.Available, sec.State)
	}
	if len(sec.Workloads) != 1 {
		t.Fatalf("workloads = %+v, want only api (unknown dropped)", sec.Workloads)
	}
	api := sec.Workloads[0]
	if api.Reporter != "destination" {
		t.Errorf("reporter = %q, want destination", api.Reporter)
	}
	if api.Counts.MTLSRequests != 30 {
		t.Errorf("mTLS requests = %d, want 30 (the delta, not 230 and not the source's 60)", api.Counts.MTLSRequests)
	}
	if api.Counts.PlaintextRequests != 7 {
		t.Errorf("plaintext requests = %d, want 7", api.Counts.PlaintextRequests)
	}
	if len(api.PlaintextCallers) != 1 || api.PlaintextCallers[0].Workload != "legacy" || api.PlaintextCallers[0].Units != 7 {
		t.Errorf("plaintext callers = %+v, want legacy/7", api.PlaintextCallers)
	}
	if api.LastSeen.IsZero() || sec.LastSeen.IsZero() {
		t.Error("lastSeen missing on an available data plane")
	}
	if len(sec.Edges) != 2 {
		t.Errorf("edges = %+v, want web->api and legacy->api", sec.Edges)
	}
	if sec.TargetsTotal != 2 || sec.TargetsUp != 1 {
		t.Errorf("targets up/total = %d/%d, want 1/2", sec.TargetsUp, sec.TargetsTotal)
	}
	if len(sec.TargetsDown) != 1 || sec.TargetsDown[0] != "shop/web-5c1a" {
		t.Errorf("targets down = %v, want [shop/web-5c1a]", sec.TargetsDown)
	}

	other, err := store.MeshSecurity(ctx, meshQuery(base, "other"))
	if err != nil {
		t.Fatalf("MeshSecurity other tenant: %v", err)
	}
	if other.Available || other.TargetsTotal != 0 {
		t.Errorf("tenant isolation broken: %+v", other)
	}
}

// The three silences, told apart by the scrape-report series under the
// data-plane job — three job names on one server, each in a state of its own.
func TestMeshSecurityStates(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	insertGauge(t, store, base.Add(1*time.Minute), "up",
		map[string]string{"service.name": "dp-unreachable", "service.instance.id": "10.0.0.2:15020"}, 0)
	insertGauge(t, store, base.Add(1*time.Minute), "up",
		map[string]string{"service.name": "dp-unrecognised", "service.instance.id": "10.0.0.3:15020"}, 1)

	for job, want := range map[string]storage.MeshScrapeState{
		"dp-unconfigured": storage.MeshControlPlaneUnconfigured,
		"dp-unreachable":  storage.MeshControlPlaneUnreachable,
		"dp-unrecognised": storage.MeshControlPlaneUnrecognised,
	} {
		q := meshQuery(base, "default")
		q.MeshDataplaneJob = job
		sec, err := store.MeshSecurity(ctx, q)
		if err != nil {
			t.Fatalf("MeshSecurity(%s): %v", job, err)
		}
		if sec.Available {
			t.Errorf("%s: available with no istio series", job)
		}
		if sec.State != want {
			t.Errorf("%s: state = %q, want %q", job, sec.State, want)
		}
	}
}

// One destination's requests by outcome: flags and versions counted, and each
// caller's 5xx share — from the destination's account only.
func TestMeshRequestBreakdownIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	apiRes := dataplaneRes("default", "10.0.0.2:15020", "shop", "api-7d9f")
	webRes := dataplaneRes("default", "10.0.0.3:15020", "shop", "web-5c1a")

	with := func(src, code, flags, version string) map[string]string {
		a := istioAttrs("destination", src, "api", "mutual_tls")
		a["response_code"], a["response_flags"], a["destination_version"] = code, flags, version
		return a
	}
	series := []struct {
		attrs      map[string]string
		from, till float64
	}{
		{with("web", "200", "-", "v1"), 10, 100},
		{with("web", "503", "UO", "v1"), 0, 5},
		{with("mobile", "500", "-", "v2"), 0, 3},
		{with("mobile", "200", "-", "v2"), 0, 1},
	}
	for _, s := range series {
		insertSum(t, store, base.Add(1*time.Minute), meshRequestsMetric, apiRes, s.attrs, s.from)
		insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, apiRes, s.attrs, s.till)
	}
	// The caller's account of the same requests, and another destination's:
	// neither belongs in api's breakdown.
	src := istioAttrs("source", "web", "api", "mutual_tls")
	insertSum(t, store, base.Add(1*time.Minute), meshRequestsMetric, webRes, src, 0)
	insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, webRes, src, 1000)
	other := istioAttrs("destination", "web", "cart", "mutual_tls")
	insertSum(t, store, base.Add(1*time.Minute), meshRequestsMetric, apiRes, other, 0)
	insertSum(t, store, base.Add(2*time.Minute), meshRequestsMetric, apiRes, other, 50)

	bd, err := store.MeshRequestBreakdown(ctx, meshQuery(base, "default"), "shop", "api")
	if err != nil {
		t.Fatalf("MeshRequestBreakdown: %v", err)
	}
	if !bd.Measured || bd.Reporter != "destination" {
		t.Fatalf("measured=%v reporter=%q", bd.Measured, bd.Reporter)
	}
	if bd.ResponseFlags["-"] != 94 || bd.ResponseFlags["UO"] != 5 {
		t.Errorf("flags = %v, want -:94 UO:5", bd.ResponseFlags)
	}
	if bd.DestinationVersions["v1"] != 95 || bd.DestinationVersions["v2"] != 4 {
		t.Errorf("versions = %v, want v1:95 v2:4", bd.DestinationVersions)
	}
	if len(bd.Callers) != 2 {
		t.Fatalf("callers = %+v, want web and mobile", bd.Callers)
	}
	if web := bd.Callers[0]; web.Workload != "web" || web.Requests != 95 || web.Errors5xx != 5 {
		t.Errorf("web = %+v, want 95 requests / 5 errors", web)
	}
	if mobile := bd.Callers[1]; mobile.Workload != "mobile" || mobile.Requests != 4 || mobile.Errors5xx != 3 {
		t.Errorf("mobile = %+v, want 4 requests / 3 errors", mobile)
	}

	none, err := store.MeshRequestBreakdown(ctx, meshQuery(base, "default"), "shop", "nobody")
	if err != nil {
		t.Fatalf("MeshRequestBreakdown(nobody): %v", err)
	}
	if none.Measured {
		t.Errorf("an unknown workload came back measured: %+v", none)
	}
}

// ztunnel: gauges summed across pods at their LATEST value, the counter's rise
// summed per series, and the pod count a node fleet checks itself against.
func TestMeshZtunnelHealthIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	a := dataplaneRes("default", "10.0.1.1:15020", "istio-system", "ztunnel-a")
	b := dataplaneRes("default", "10.0.1.2:15020", "istio-system", "ztunnel-b")

	// Pod a: 8 then 9 (latest wins). Pod b: 12. Fleet: 21, not 29.
	insertGauge(t, store, base.Add(1*time.Minute), meshZtunnelActiveMetric, a, 8)
	insertGauge(t, store, base.Add(2*time.Minute), meshZtunnelActiveMetric, a, 9)
	insertGauge(t, store, base.Add(2*time.Minute), meshZtunnelActiveMetric, b, 12)
	insertGauge(t, store, base.Add(2*time.Minute), meshZtunnelPendingMetric, b, 1)
	// Terminations: a 4 -> 6, b 0 -> 1. Rise: 3.
	insertSum(t, store, base.Add(1*time.Minute), meshZtunnelTerminationsMetric, a, map[string]string{}, 4)
	insertSum(t, store, base.Add(2*time.Minute), meshZtunnelTerminationsMetric, a, map[string]string{}, 6)
	insertSum(t, store, base.Add(1*time.Minute), meshZtunnelTerminationsMetric, b, map[string]string{}, 0)
	insertSum(t, store, base.Add(2*time.Minute), meshZtunnelTerminationsMetric, b, map[string]string{}, 1)

	zt, err := store.MeshZtunnelHealth(ctx, meshQuery(base, "default"))
	if err != nil {
		t.Fatalf("MeshZtunnelHealth: %v", err)
	}
	if !zt.Measured {
		t.Fatal("ztunnel reported unmeasured with series present")
	}
	if zt.ActiveWorkloads != 21 || zt.PendingWorkloads != 1 || zt.Pods != 2 {
		t.Errorf("active/pending/pods = %d/%d/%d, want 21/1/2", zt.ActiveWorkloads, zt.PendingWorkloads, zt.Pods)
	}
	if zt.XDSConnectionTerminations != 3 {
		t.Errorf("terminations = %d, want 3 (the rise, not 11)", zt.XDSConnectionTerminations)
	}

	empty, err := store.MeshZtunnelHealth(ctx, meshQuery(base, "other"))
	if err != nil {
		t.Fatalf("MeshZtunnelHealth other tenant: %v", err)
	}
	if empty.Measured {
		t.Errorf("another tenant's ztunnel came back measured: %+v", empty)
	}
}
