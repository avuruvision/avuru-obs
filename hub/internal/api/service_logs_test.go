package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

// serviceSnapshot is workloadSnapshot plus the Kubernetes Services in front of
// it, including the shape this endpoint exists for: a Service named for the
// application (checkout-service) selecting a Deployment named for the workload
// (checkout).
func serviceSnapshot() meshconfig.Snapshot {
	snap := overviewSnapshot()
	snap.Services = []meshconfig.Service{
		{Namespace: "shop", Name: "checkout-service", Workloads: []string{"shop/checkout"}},
		{Namespace: "shop", Name: "headless", Workloads: nil},
		{Namespace: "shop", Name: "twin", Workloads: []string{"shop/payments"}},
		{Namespace: "quiet", Name: "twin", Workloads: []string{"quiet/worker"}},
	}
	return snap
}

// The route is the logs module's, not the mesh's: an install with no mesh
// still has logs, and this is the screen that shows them.
func TestServiceLogsRouteNeedsOnlyTheLogsModule(t *testing.T) {
	fake := &storagetest.Fake{}
	if rec := meshGet(t, fake, Config{Modules: modules.Set{modules.Core: true, modules.Mesh: true}},
		"/api/v1/services/checkout/logs"); rec.Code != http.StatusNotFound {
		t.Errorf("logs off: status %d, want 404", rec.Code)
	}
	rec := meshGet(t, fake, Config{Modules: modules.Set{modules.Core: true, modules.Logs: true}},
		"/api/v1/services/checkout/logs")
	if rec.Code != http.StatusOK {
		t.Fatalf("logs on, mesh off: status %d: %s", rec.Code, rec.Body.String())
	}
	// No mesh, so no workload and no proxies — but the app's own lines, and
	// a sentence saying why the rest is missing.
	resp := decodeWorkloadLogs(t, rec.Body.Bytes())
	if resp.Sources.ProxiesUnavailable == "" || resp.Sources.Workload != "" {
		t.Errorf("sources = %+v, want app-only with a reason", resp.Sources)
	}
	if got := sourceServices(fake.LastLogQuery); strings.Join(got, ";") != "checkout" {
		t.Errorf("queried %v, want the service's own name alone", got)
	}
}

// The whole point: a service whose Deployment is named differently. The
// resolver finds the workload, and the app source carries BOTH names — the
// lines may be filed under either, and asking for only one is what made the
// tab look empty.
func TestServiceLogsResolvesTheWorkloadAndKeepsBothNames(t *testing.T) {
	cases := []struct {
		name string
		fake *storagetest.Fake
		cfg  Config
	}{
		{
			// Rung 1: the spans say which Deployment wrote them.
			name: "from resource attributes",
			fake: &storagetest.Fake{Workload: storage.ServiceWorkload{Namespace: "shop", Workload: "checkout"}},
			cfg:  Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}},
		},
		{
			// Rung 2: no attributes, but a Service of that name fronts it.
			name: "from the Service's selector",
			fake: &storagetest.Fake{},
			cfg:  Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := meshGet(t, tc.fake, tc.cfg, "/api/v1/services/checkout-service/logs?source=app,ztunnel")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			resp := decodeWorkloadLogs(t, rec.Body.Bytes())
			if resp.Sources.Workload != "checkout" || resp.Sources.Namespace != "shop" {
				t.Errorf("resolved %s/%s, want shop/checkout", resp.Sources.Namespace, resp.Sources.Workload)
			}
			if resp.Sources.ProxiesUnavailable != "" {
				t.Errorf("proxies withheld: %q", resp.Sources.ProxiesUnavailable)
			}
			app := tc.fake.LastLogQuery.Sources[0].Services
			if strings.Join(app, "|") != "checkout|checkout.shop|checkout-service" {
				t.Errorf("app services = %v, want the workload's two spellings and the service name", app)
			}
			if !resp.Sources.Precise {
				t.Errorf("sources = %+v, want the pods from the snapshot", resp.Sources)
			}
		})
	}
}

// Rung 1 wins over the snapshot when the two disagree: the spans are the
// service's own statement about where it ran.
func TestServiceLogsPrefersResourceAttributesOverTheSnapshot(t *testing.T) {
	fake := &storagetest.Fake{Workload: storage.ServiceWorkload{Namespace: "quiet", Workload: "worker"}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	rec := meshGet(t, fake, cfg, "/api/v1/services/checkout-service/logs")
	resp := decodeWorkloadLogs(t, rec.Body.Bytes())
	if resp.Sources.Workload != "worker" || resp.Sources.Namespace != "quiet" {
		t.Errorf("resolved %s/%s, want quiet/worker", resp.Sources.Namespace, resp.Sources.Workload)
	}
	if fake.LastWorkloadService != "checkout-service" {
		t.Errorf("asked the store about %q", fake.LastWorkloadService)
	}
}

// Every way the resolution can come up empty ends the same way: the app's own
// lines, no proxy sources, and one sentence naming what to change.
func TestServiceLogsDegradesWhenNoWorkloadIsFound(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		service string
		why     string
	}{
		{"mesh-config off", Config{Modules: modules.Set{modules.Core: true, modules.Logs: true, modules.Mesh: true}}, "checkout-service", "mesh-config is off"},
		{"cluster unread", Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: meshconfig.Snapshot{
			State: meshconfig.StateForbidden, Reason: "the hub may not list pods cluster-wide"}}}, "checkout-service", "may not list pods"},
		{"no such name", Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}, "ghost", "named ghost"},
		{"a Service with nothing behind it", Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}, "headless", "named headless"},
		{"the name is a namesake in two namespaces", Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}, "twin", "named twin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &storagetest.Fake{}
			// Ask for all three: an unresolved service must not produce a
			// proxy branch with no service names, which would match nothing
			// and read as "no logs" rather than "never asked".
			rec := meshGet(t, fake, tc.cfg, "/api/v1/services/"+tc.service+"/logs?source=app,ztunnel,waypoint")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			resp := decodeWorkloadLogs(t, rec.Body.Bytes())
			if !strings.Contains(resp.Sources.ProxiesUnavailable, tc.why) {
				t.Errorf("reason = %q, want it to mention %q", resp.Sources.ProxiesUnavailable, tc.why)
			}
			if n := len(fake.LastLogQuery.Sources); n != 1 {
				t.Errorf("%d source branches, want only the app's", n)
			}
		})
	}
}

// A namesake IS resolvable once the spans have named the namespace, even
// though they never named the workload.
func TestServiceLogsNamesakeResolvesWithinTheReportedNamespace(t *testing.T) {
	fake := &storagetest.Fake{Workload: storage.ServiceWorkload{Namespace: "quiet"}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	rec := meshGet(t, fake, cfg, "/api/v1/services/twin/logs")
	resp := decodeWorkloadLogs(t, rec.Body.Bytes())
	if resp.Sources.Workload != "worker" || resp.Sources.Namespace != "quiet" {
		t.Errorf("resolved %s/%s, want quiet/worker", resp.Sources.Namespace, resp.Sources.Workload)
	}
}

// Later pages echo the workload back, and the resolver does not run again:
// re-resolving mid-scroll could change the source set under a live cursor.
func TestServiceLogsEchoedWorkloadSkipsTheResolver(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	cursor := encodeLogCursor(&storage.LogCursor{Timestamp: time.Unix(0, 42).UTC(), TraceID: "t", SpanID: "s"})
	rec := meshGet(t, fake, cfg,
		"/api/v1/services/checkout-service/logs?namespace=shop&workload=checkout&cursor="+cursor+"&limit=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.WorkloadCalls != 0 {
		t.Errorf("resolver ran %d times on an echoed page", fake.WorkloadCalls)
	}
	if fake.LastLogQuery.Cursor == nil || fake.LastLogQuery.Cursor.TraceID != "t" || fake.LastLogQuery.Limit != 7 {
		t.Errorf("cursor/limit did not reach the store: %+v", fake.LastLogQuery)
	}
	resp := decodeWorkloadLogs(t, rec.Body.Bytes())
	if resp.Sources.Workload != "checkout" {
		t.Errorf("sources = %+v", resp.Sources)
	}
}

// The filters the toolbar owns reach the store, and an unknown source is
// refused rather than silently dropped.
func TestServiceLogsFiltersAndBadSource(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: serviceSnapshot()}}
	rec := meshGet(t, fake, cfg, "/api/v1/services/checkout-service/logs?q=timeout&severity=ERROR")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if fake.LastLogQuery.Query != "timeout" || fake.LastLogQuery.MinSeverity != "ERROR" {
		t.Errorf("q/severity did not reach the store: %+v", fake.LastLogQuery)
	}
	if rec := meshGet(t, fake, cfg, "/api/v1/services/checkout-service/logs?source=bogus"); rec.Code != http.StatusBadRequest {
		t.Errorf("source=bogus: status %d, want 400", rec.Code)
	}
}
