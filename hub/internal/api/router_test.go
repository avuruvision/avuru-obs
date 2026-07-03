package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func newMux(fake *storagetest.Fake) *http.ServeMux {
	mux := http.NewServeMux()
	provider := func() storage.Store {
		if fake == nil {
			return nil
		}
		return fake
	}
	Register(mux, provider, Config{RetentionTracesDays: 7, RetentionLogsDays: 3})
	return mux
}

func get(t *testing.T, mux *http.ServeMux, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRoutes(t *testing.T) {
	fake := &storagetest.Fake{Traces: map[string]storage.Trace{
		"abc": {TraceID: "abc", Spans: []storage.Span{{SpanID: "s1", Service: "svc", StartTime: time.Now(), Duration: time.Millisecond}}},
	}}
	mux := newMux(fake)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"healthz ok", http.MethodGet, "/healthz", http.StatusOK},
		{"status ok", http.MethodGet, "/api/v1/status", http.StatusOK},
		{"system status ok", http.MethodGet, "/api/v1/system/status", http.StatusOK},
		{"services ok", http.MethodGet, "/api/v1/services", http.StatusOK},
		{"service map ok", http.MethodGet, "/api/v1/service-map", http.StatusOK},
		{"traces ok", http.MethodGet, "/api/v1/traces", http.StatusOK},
		{"overview ok", http.MethodGet, "/api/v1/traces/overview", http.StatusOK},
		{"heatmap ok", http.MethodGet, "/api/v1/traces/heatmap", http.StatusOK},
		{"trace by id ok", http.MethodGet, "/api/v1/traces/abc", http.StatusOK},
		{"logs ok", http.MethodGet, "/api/v1/logs", http.StatusOK},
		{"logs for trace ok", http.MethodGet, "/api/v1/traces/abc/logs", http.StatusOK},
		{"infra nodes ok", http.MethodGet, "/api/v1/infra/nodes", http.StatusOK},
		{"infra pods ok", http.MethodGet, "/api/v1/infra/pods", http.StatusOK},
		{"infra nodes bad start", http.MethodGet, "/api/v1/infra/nodes?start=garbage", http.StatusBadRequest},
		{"trace not found", http.MethodGet, "/api/v1/traces/nope", http.StatusNotFound},
		{"bad start", http.MethodGet, "/api/v1/traces?start=garbage", http.StatusBadRequest},
		{"bad status filter", http.MethodGet, "/api/v1/traces?status=weird", http.StatusBadRequest},
		{"bad order filter", http.MethodGet, "/api/v1/traces?order=sideways", http.StatusBadRequest},
		{"bad cursor", http.MethodGet, "/api/v1/traces?cursor=!!!", http.StatusBadRequest},
		{"healthz wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("got status %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestStoreUnavailable(t *testing.T) {
	mux := newMux(nil)

	for _, path := range []string{"/api/v1/services", "/api/v1/traces", "/api/v1/traces/abc", "/api/v1/logs", "/api/v1/traces/abc/logs", "/api/v1/infra/nodes", "/api/v1/infra/pods"} {
		if rec := get(t, mux, path); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: got %d, want 503", path, rec.Code)
		}
	}
	// healthz must stay green during a store outage.
	if rec := get(t, mux, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz during outage: got %d, want 200", rec.Code)
	}
	var status statusResponse
	rec := get(t, mux, "/api/v1/status")
	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decoding status: %v", err)
	}
	if status.ClickHouse != "unreachable" {
		t.Errorf("clickhouse status = %q, want unreachable", status.ClickHouse)
	}
}

func TestSearchTracesParamParsing(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := newMux(fake)

	cursor := encodeCursor(&storage.TraceCursor{Timestamp: time.Unix(0, 1718000000123456789).UTC(), TraceID: "tid"})
	rec := get(t, mux, "/api/v1/traces?service=frontend&operation=GET+/x&status=error&minDurationMs=10.5&maxDurationMs=2000&limit=7&cursor="+cursor)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	q := fake.LastTraceQuery
	if q.Service != "frontend" || q.Operation != "GET /x" || q.Status != "error" || q.Limit != 7 {
		t.Errorf("filters not parsed: %+v", q)
	}
	if q.MinDuration != 10500*time.Microsecond || q.MaxDuration != 2*time.Second {
		t.Errorf("durations not parsed: min=%v max=%v", q.MinDuration, q.MaxDuration)
	}
	if q.Cursor == nil || q.Cursor.TraceID != "tid" || q.Cursor.Timestamp.UnixNano() != 1718000000123456789 {
		t.Errorf("cursor not round-tripped: %+v", q.Cursor)
	}
	if q.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", q.Tenant)
	}
	// Auxiliary traffic is excluded by default (no includeAux param).
	if !q.ExcludeAux {
		t.Errorf("ExcludeAux should default to true")
	}

	// Order, tags and includeAux opt-in.
	rec2 := get(t, mux, "/api/v1/traces?order=slowest&tags=http.status_code=500,http.method=GET&includeAux=true")
	if rec2.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec2.Code, rec2.Body.String())
	}
	q2 := fake.LastTraceQuery
	if q2.Order != "slowest" {
		t.Errorf("order = %q, want slowest", q2.Order)
	}
	if q2.ExcludeAux {
		t.Errorf("ExcludeAux should be false with includeAux=true")
	}
	if q2.Tags["http.status_code"] != "500" || q2.Tags["http.method"] != "GET" {
		t.Errorf("tags not parsed: %+v", q2.Tags)
	}
}

func TestServiceMapDefaults(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "frontend", SpanCount: 3}},
		Edges:    []storage.ServiceEdge{{Source: "frontend", Target: "api", Count: 2}},
	}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !fake.LastServiceQuery.ExcludeAux {
		t.Errorf("service-map ExcludeAux should default to true")
	}
	var resp struct {
		Services []serviceDTO     `json:"services"`
		Edges    []serviceEdgeDTO `json:"edges"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Services) != 1 || len(resp.Edges) != 1 || resp.Edges[0].Source != "frontend" {
		t.Errorf("service-map response wrong: %+v", resp)
	}
}

func TestSearchLogsParamParsing(t *testing.T) {
	fake := &storagetest.Fake{
		LogPage: storage.LogPage{
			Logs:       []storage.LogRecord{{Service: "checkout", Body: "boom", Severity: "ERROR"}},
			NextCursor: &storage.LogCursor{Timestamp: time.Unix(0, 42).UTC(), TraceID: "tid", SpanID: "sid"},
		},
	}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/logs?service=checkout&q=boom&severity=ERROR&limit=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	q := fake.LastLogQuery
	if q.Service != "checkout" || q.Query != "boom" || q.MinSeverity != "ERROR" || q.Limit != 7 {
		t.Errorf("log filters not parsed: %+v", q)
	}
	if q.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", q.Tenant)
	}

	var resp logsResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding logs response: %v", err)
	}
	if len(resp.Logs) != 1 || resp.Logs[0].Service != "checkout" || resp.NextCursor == "" {
		t.Errorf("logs response wrong: %+v", resp)
	}

	// The returned cursor must round-trip back into a LogQuery.
	rec2 := get(t, mux, "/api/v1/logs?cursor="+resp.NextCursor)
	if rec2.Code != http.StatusOK {
		t.Fatalf("cursor round-trip status %d: %s", rec2.Code, rec2.Body.String())
	}
	if c := fake.LastLogQuery.Cursor; c == nil || c.TraceID != "tid" || c.SpanID != "sid" {
		t.Errorf("log cursor not round-tripped: %+v", fake.LastLogQuery.Cursor)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	in := &storage.TraceCursor{Timestamp: time.Unix(0, 42).UTC(), TraceID: "abc,def"} // comma in id must survive
	req := httptest.NewRequest(http.MethodGet, "/?cursor="+encodeCursor(in), nil)
	out, err := parseCursor(req)
	if err != nil {
		t.Fatalf("parseCursor: %v", err)
	}
	if out.TraceID != in.TraceID || !out.Timestamp.Equal(in.Timestamp) {
		t.Errorf("round trip mismatch: %+v vs %+v", out, in)
	}
}

func TestSystemStatus(t *testing.T) {
	now := time.Now().UTC()
	fake := &storagetest.Fake{Stats: storage.SystemStats{
		Signals: []storage.SignalStats{
			{Signal: "traces", Rows: 100, Bytes: 1000, CompressedBytes: 200, Oldest: &now, Newest: &now},
			{Signal: "logs"},
		},
		Disks: []storage.DiskStats{{Name: "default", FreeBytes: 50, TotalBytes: 100}},
	}}

	rec := get(t, newMux(fake), "/api/v1/system/status")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp systemStatusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Overall != "healthy" {
		t.Errorf("overall = %q, want healthy", resp.Overall)
	}
	if !hasComponent(resp.Components, "ClickHouse", "healthy") || !hasComponent(resp.Components, "Ingestion", "healthy") {
		t.Errorf("components: %+v", resp.Components)
	}
	var traces *signalStatsDTO
	for i := range resp.Signals {
		if resp.Signals[i].Signal == "traces" {
			traces = &resp.Signals[i]
		}
	}
	if traces == nil || traces.RetentionDays != 7 || traces.Compression != 5 {
		t.Errorf("traces signal wrong: %+v", traces)
	}

	// Store down: still 200, but overall down and ClickHouse down.
	down := get(t, newMux(nil), "/api/v1/system/status")
	if down.Code != http.StatusOK {
		t.Fatalf("down code %d, want 200", down.Code)
	}
	var dresp systemStatusResponse
	if err := json.NewDecoder(down.Body).Decode(&dresp); err != nil {
		t.Fatalf("decode down: %v", err)
	}
	if dresp.Overall != "down" || !hasComponent(dresp.Components, "ClickHouse", "down") {
		t.Errorf("down response wrong: %+v", dresp)
	}
}

func hasComponent(cs []componentHealth, name, status string) bool {
	for _, c := range cs {
		if c.Name == name && c.Status == status {
			return true
		}
	}
	return false
}

func TestInfraEndpoints(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fake := &storagetest.Fake{
		Nodes: []storage.NodeStat{{
			Name: "node-a", CPUUsage: 1.5, MemoryUsage: 2048, MemoryAvailable: 4096,
			NetworkRxRate: 100, NetworkTxRate: 50, PodCount: 7,
			CPUSeries: []storage.MetricPoint{{Time: now, Value: 1.5}},
		}},
		Pods: []storage.PodStat{{
			Name: "web-1", Namespace: "shop", Node: "node-a", Workload: "web", CPUUsage: 0.2, MemoryUsage: 512,
		}},
	}
	mux := newMux(fake)

	var nodes nodesResponse
	rec := get(t, mux, "/api/v1/infra/nodes?points=12")
	if err := json.NewDecoder(rec.Body).Decode(&nodes); err != nil {
		t.Fatalf("decoding nodes: %v", err)
	}
	if fake.LastInfraQuery.Points != 12 {
		t.Errorf("points not parsed: %+v", fake.LastInfraQuery)
	}
	if len(nodes.Nodes) != 1 || nodes.Nodes[0].PodCount != 7 || len(nodes.Nodes[0].CPUSeries) != 1 {
		t.Fatalf("nodes payload wrong: %+v", nodes)
	}

	var pods podsResponse
	rec = get(t, mux, "/api/v1/infra/pods?node=node-a&limit=5")
	if err := json.NewDecoder(rec.Body).Decode(&pods); err != nil {
		t.Fatalf("decoding pods: %v", err)
	}
	if fake.LastInfraQuery.Node != "node-a" || fake.LastInfraQuery.Limit != 5 {
		t.Errorf("pod filters not parsed: %+v", fake.LastInfraQuery)
	}
	if len(pods.Pods) != 1 || pods.Pods[0].Workload != "web" {
		t.Fatalf("pods payload wrong: %+v", pods)
	}
}
