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

func decodeWorkloadLogs(t *testing.T, body []byte) meshWorkloadLogsResponse {
	t.Helper()
	var resp meshWorkloadLogsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func sourceServices(q storage.LogQuery) []string {
	var out []string
	for _, s := range q.Sources {
		out = append(out, strings.Join(s.Services, "|"))
	}
	return out
}

// The route needs both halves: the mesh module for the workload and the logs
// module for the lines. Missing either, it is not there.
func TestMeshWorkloadLogsRouteNeedsMeshAndLogs(t *testing.T) {
	for _, active := range []modules.Set{
		{modules.Core: true, modules.Mesh: true, modules.MeshConfig: true},
		{modules.Core: true, modules.Logs: true, modules.MeshConfig: true},
	} {
		cfg := Config{Modules: active, MeshConfigReader: stubReader{snap: overviewSnapshot()}}
		if rec := meshGet(t, &storagetest.Fake{}, cfg, "/api/v1/mesh/workloads/shop/checkout/logs"); rec.Code != http.StatusNotFound {
			t.Errorf("with %v: status %d, want 404", active, rec.Code)
		}
	}
}

// With the snapshot at hand the sources are precise: the workload's two
// spellings, ztunnel narrowed to its pods and its Service, and the waypoint
// it is bound to — or no waypoint source when none is.
func TestMeshWorkloadLogsComposeThreeSourcesFromTheSnapshot(t *testing.T) {
	snap := overviewSnapshot()
	for i := range snap.Workloads {
		if snap.Workloads[i].Name == "checkout" {
			snap.Workloads[i].Waypoint, snap.Workloads[i].WaypointNamespace = "global-waypoint", "istio-waypoint"
		}
	}
	fake := &storagetest.Fake{LogPage: storage.LogPage{
		Logs:       []storage.LogRecord{{Timestamp: time.Now(), Service: "ztunnel", Body: "connection complete"}},
		NextCursor: &storage.LogCursor{Timestamp: time.Unix(0, 42), TraceID: "t", SpanID: "s"},
	}}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}
	rec := meshGet(t, fake, cfg, "/api/v1/mesh/workloads/shop/checkout/logs?q=complete&severity=WARN")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeWorkloadLogs(t, rec.Body.Bytes())
	if len(resp.Logs) != 1 || resp.NextCursor == "" {
		t.Errorf("page = %+v", resp)
	}
	if !resp.Sources.Precise || resp.Sources.Fallback != "" {
		t.Errorf("sources = %+v, want precise", resp.Sources)
	}
	if got := sourceServices(fake.LastLogQuery); strings.Join(got, ";") != "checkout|checkout.shop;ztunnel;global-waypoint|global-waypoint.istio-waypoint" {
		t.Errorf("sources queried = %v", got)
	}
	needles := fake.LastLogQuery.Sources[1].BodyAll
	if len(needles) != 1 || strings.Join(needles[0], ",") != "checkout.shop.svc,checkout-abc12-x1,checkout-abc12-x2" {
		t.Errorf("ztunnel needles = %v", needles)
	}
	if fake.LastLogQuery.Sources[0].BodyAll != nil {
		t.Error("the workload's own lines were narrowed by body")
	}
	if fake.LastLogQuery.Query != "complete" || fake.LastLogQuery.MinSeverity != "WARN" {
		t.Errorf("q/severity did not reach the store: %+v", fake.LastLogQuery)
	}

	// No waypoint bound: two sources, and the descriptor says so.
	rec = meshGet(t, fake, Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: overviewSnapshot()}},
		"/api/v1/mesh/workloads/shop/checkout/logs")
	resp = decodeWorkloadLogs(t, rec.Body.Bytes())
	if len(fake.LastLogQuery.Sources) != 2 || len(resp.Sources.Waypoint) != 0 {
		t.Errorf("without a waypoint: %d sources, descriptor %+v", len(fake.LastLogQuery.Sources), resp.Sources)
	}
}

// source= narrows to the sources asked for, and an unknown one is refused.
func TestMeshWorkloadLogsSourceParam(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: overviewSnapshot()}}
	rec := meshGet(t, fake, cfg, "/api/v1/mesh/workloads/shop/checkout/logs?source=ztunnel")
	if rec.Code != http.StatusOK || len(fake.LastLogQuery.Sources) != 1 || fake.LastLogQuery.Sources[0].Services[0] != "ztunnel" {
		t.Errorf("source=ztunnel: %d %v", rec.Code, sourceServices(fake.LastLogQuery))
	}
	if rec := meshGet(t, fake, cfg, "/api/v1/mesh/workloads/shop/checkout/logs?source=bogus"); rec.Code != http.StatusBadRequest {
		t.Errorf("source=bogus: status %d, want 400", rec.Code)
	}
}

// Without the pods the proxies' lines are matched by name and namespace
// together, so shop/checkout and shop-staging/checkout stay apart — and the
// response says why it could not do better.
func TestMeshWorkloadLogsFallBackWhenPodsAreUnknown(t *testing.T) {
	fake := &storagetest.Fake{}
	cases := []struct {
		name string
		cfg  Config
		why  string
	}{
		{"mesh-config off", Config{Modules: modules.Set{modules.Core: true, modules.Mesh: true, modules.Logs: true}}, "mesh-config"},
		{"pods cut", func() Config {
			snap := overviewSnapshot()
			snap.PodsTruncated = true
			return Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: snap}}
		}(), "cut"},
		{"not in snapshot", Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: workloadSnapshot()}}, "no pod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := meshGet(t, fake, tc.cfg, "/api/v1/mesh/workloads/shop/checkout/logs?waypoint=istio-waypoint/global-waypoint")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			resp := decodeWorkloadLogs(t, rec.Body.Bytes())
			if resp.Sources.Precise || !strings.Contains(resp.Sources.Fallback, tc.why) {
				t.Errorf("sources = %+v, want a fallback mentioning %q", resp.Sources, tc.why)
			}
			zt := fake.LastLogQuery.Sources[1]
			if len(zt.BodyAll) != 2 || strings.Join(zt.BodyAll[0], ",") != `checkout.shop.svc,workload="checkout-` ||
				strings.Join(zt.BodyAll[1], ",") != `namespace="shop",checkout.shop.svc` {
				t.Errorf("fallback needles = %v", zt.BodyAll)
			}
			if wp := fake.LastLogQuery.Sources[2]; strings.Join(wp.Services, "|") != "global-waypoint|global-waypoint.istio-waypoint" {
				t.Errorf("waypoint from the param = %v", wp.Services)
			}
		})
	}
}

// The cursor round-trips through the same wire format as the logs screen's.
func TestMeshWorkloadLogsCursorRoundTrip(t *testing.T) {
	fake := &storagetest.Fake{}
	cfg := Config{Modules: modules.AllSet(), MeshConfigReader: stubReader{snap: overviewSnapshot()}}
	cursor := encodeLogCursor(&storage.LogCursor{Timestamp: time.Unix(0, 42).UTC(), TraceID: "t", SpanID: "s"})
	rec := meshGet(t, fake, cfg, "/api/v1/mesh/workloads/shop/checkout/logs?cursor="+cursor+"&limit=7")
	if rec.Code != http.StatusOK || fake.LastLogQuery.Cursor == nil || fake.LastLogQuery.Cursor.TraceID != "t" || fake.LastLogQuery.Limit != 7 {
		t.Errorf("status %d, query %+v", rec.Code, fake.LastLogQuery)
	}
	_ = meshconfig.KindPod
}
