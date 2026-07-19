package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pprofile"
	"go.opentelemetry.io/collector/pdata/pprofile/pprofileotlp"

	"github.com/avuru/avuru-obs/hub/internal/modules"
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
	fake := &storagetest.Fake{
		Traces: map[string]storage.Trace{
			"abc": {TraceID: "abc", Spans: []storage.Span{{SpanID: "s1", Service: "svc", StartTime: time.Now(), Duration: time.Millisecond}}},
		},
		SpanTraces: map[string]string{"s1": "abc"},
	}
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
		{"span lookup ok", http.MethodGet, "/api/v1/spans/s1", http.StatusOK},
		{"span lookup not found", http.MethodGet, "/api/v1/spans/nope", http.StatusNotFound},
		{"logs ok", http.MethodGet, "/api/v1/logs", http.StatusOK},
		{"logs for trace ok", http.MethodGet, "/api/v1/traces/abc/logs", http.StatusOK},
		{"infra nodes ok", http.MethodGet, "/api/v1/infra/nodes", http.StatusOK},
		{"red ok", http.MethodGet, "/api/v1/metrics/red", http.StatusOK},
		{"profiled services ok", http.MethodGet, "/api/v1/profiles/services", http.StatusOK},
		{"flamegraph needs service", http.MethodGet, "/api/v1/profiles/flamegraph", http.StatusBadRequest},
		{"flamegraph ok", http.MethodGet, "/api/v1/profiles/flamegraph?service=checkout", http.StatusOK},
		{"infra pods ok", http.MethodGet, "/api/v1/infra/pods", http.StatusOK},
		{"agents ok", http.MethodGet, "/api/v1/agents", http.StatusOK},
		{"agents bad window", http.MethodGet, "/api/v1/agents?windowSec=-5", http.StatusBadRequest},
		{"health groups ok", http.MethodGet, "/api/v1/health/groups", http.StatusOK},
		{"alerts ok", http.MethodGet, "/api/v1/alerts", http.StatusOK},
		{"alert rules ok", http.MethodGet, "/api/v1/alerts/rules", http.StatusOK},
		{"health groups bad start", http.MethodGet, "/api/v1/health/groups?start=garbage", http.StatusBadRequest},
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

	for _, path := range []string{"/api/v1/services", "/api/v1/traces", "/api/v1/traces/abc", "/api/v1/spans/s1", "/api/v1/logs", "/api/v1/traces/abc/logs", "/api/v1/infra/nodes", "/api/v1/infra/pods", "/api/v1/agents", "/api/v1/health/groups", "/api/v1/alerts"} {
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

func TestSpanLookup(t *testing.T) {
	fake := &storagetest.Fake{SpanTraces: map[string]string{"s1": "abc"}}
	mux := newMux(fake)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/spans/s1", nil)
	req.Header.Set("X-Avuru-Tenant", "staging")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// Tenant header is honored (the enterprise seam).
	if fake.LastSpanLookupTenant != "staging" {
		t.Errorf("tenant header not honored: %q", fake.LastSpanLookupTenant)
	}
	var resp spanLookupResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TraceID != "abc" || resp.SpanID != "s1" {
		t.Errorf("lookup response wrong: %+v", resp)
	}
}

func TestTraceOverviewParamParsing(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/traces/overview?service=frontend")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	q := fake.LastOverviewQuery
	if q.Service != "frontend" {
		t.Errorf("service = %q, want frontend", q.Service)
	}
	if q.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", q.Tenant)
	}
	if !q.ExcludeAux {
		t.Errorf("ExcludeAux should default to true")
	}

	rec2 := get(t, mux, "/api/v1/traces/overview?includeAux=true")
	if rec2.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec2.Code, rec2.Body.String())
	}
	if fake.LastOverviewQuery.ExcludeAux {
		t.Errorf("ExcludeAux should be false with includeAux=true")
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

// TestMergeEdges exercises the trace+flow edge merge directly (table-driven):
// an edge seen in both sources becomes "both" and gains bytes while keeping its
// call volume; trace-only stays "trace"; flow-only is appended as "flow".
func TestMergeEdges(t *testing.T) {
	type want struct {
		provenance string
		count      uint64
		bytesPos   bool
	}
	tests := []struct {
		name   string
		trace  []storage.ServiceEdge
		flow   []storage.ServiceEdge
		expect map[string]want
	}{
		{
			name:  "shared edge becomes both, flow-only appended",
			trace: []storage.ServiceEdge{{Source: "A", Target: "B", Count: 3, ErrorCount: 1}},
			flow: []storage.ServiceEdge{
				{Source: "A", Target: "B", Bytes: 1024},
				{Source: "C", Target: "D", Bytes: 2048},
			},
			expect: map[string]want{
				"A->B": {provenance: "both", count: 3, bytesPos: true},
				"C->D": {provenance: "flow", count: 0, bytesPos: true},
			},
		},
		{
			name:   "trace only (no infra-metrics)",
			trace:  []storage.ServiceEdge{{Source: "A", Target: "B", Count: 2}},
			flow:   nil,
			expect: map[string]want{"A->B": {provenance: "trace", count: 2, bytesPos: false}},
		},
		{
			name:   "flow only",
			trace:  nil,
			flow:   []storage.ServiceEdge{{Source: "C", Target: "D", Bytes: 7}},
			expect: map[string]want{"C->D": {provenance: "flow", count: 0, bytesPos: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := mergeEdges(tt.trace, tt.flow)
			if len(merged) != len(tt.expect) {
				t.Fatalf("got %d edges, want %d: %+v", len(merged), len(tt.expect), merged)
			}
			for _, e := range merged {
				k := e.Source + "->" + e.Target
				w, ok := tt.expect[k]
				if !ok {
					t.Fatalf("unexpected edge %s: %+v", k, e)
				}
				if e.Provenance != w.provenance {
					t.Errorf("%s provenance = %q, want %q", k, e.Provenance, w.provenance)
				}
				if e.Count != w.count {
					t.Errorf("%s count = %d, want %d", k, e.Count, w.count)
				}
				if (e.Bytes > 0) != w.bytesPos {
					t.Errorf("%s bytes = %d, want positive=%v", k, e.Bytes, w.bytesPos)
				}
			}
		})
	}
}

// TestServiceMapMergesNetworkEdges proves the handler wires NetworkEdges into
// the response when infra-metrics is active: the shared A→B edge is "both" with
// bytes, and the flow-only C→D edge shows up as "flow".
func TestServiceMapMergesNetworkEdges(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "A", SpanCount: 5}},
		Edges:    []storage.ServiceEdge{{Source: "A", Target: "B", Count: 3, ErrorCount: 1}},
		NetEdges: []storage.ServiceEdge{
			{Source: "A", Target: "B", Bytes: 1024, Provenance: "flow"},
			{Source: "C", Target: "D", Bytes: 2048, Provenance: "flow"},
		},
	}
	mux := newMux(fake) // zero-value Config → all modules on → infra-metrics active

	var resp struct {
		Edges []serviceEdgeDTO `json:"edges"`
	}
	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byPair := map[string]serviceEdgeDTO{}
	for _, e := range resp.Edges {
		byPair[e.Source+"->"+e.Target] = e
	}
	if len(resp.Edges) != 2 {
		t.Fatalf("got %d edges, want 2: %+v", len(resp.Edges), resp.Edges)
	}
	ab := byPair["A->B"]
	if ab.Provenance != "both" || ab.Bytes == 0 || ab.Calls != 3 {
		t.Errorf("A->B = %+v, want provenance=both, bytes>0, calls=3", ab)
	}
	cd := byPair["C->D"]
	if cd.Provenance != "flow" || cd.Bytes == 0 {
		t.Errorf("C->D = %+v, want provenance=flow, bytes>0", cd)
	}
}

// TestServiceMapSkipsNetworkEdgesWithoutInfraMetrics proves the flow query is
// gated: with infra-metrics disabled, NetworkEdges is never consulted (the
// otel_metrics_* tables don't exist), so only trace edges appear — stamped
// "trace".
func TestServiceMapSkipsNetworkEdgesWithoutInfraMetrics(t *testing.T) {
	fake := &storagetest.Fake{
		Edges:    []storage.ServiceEdge{{Source: "A", Target: "B", Count: 3}},
		NetEdges: []storage.ServiceEdge{{Source: "C", Target: "D", Bytes: 2048}},
	}
	set, err := modules.Parse("core") // infra-metrics off
	if err != nil {
		t.Fatalf("parse modules: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{Modules: set})

	var resp struct {
		Edges []serviceEdgeDTO `json:"edges"`
	}
	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Edges) != 1 || resp.Edges[0].Source != "A" || resp.Edges[0].Provenance != "trace" {
		t.Errorf("edges = %+v, want only A->B with provenance=trace", resp.Edges)
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

func TestREDSeriesEndpoint(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	fake := &storagetest.Fake{
		RED: []storage.REDSeries{{
			Service: "checkout",
			Points: []storage.REDPoint{{
				Time: now, Count: 60, ErrorCount: 6,
				P50: 10 * time.Millisecond, P95: 40 * time.Millisecond, P99: 90 * time.Millisecond,
			}},
		}},
	}
	mux := newMux(fake)

	var resp redResponse
	rec := get(t, mux, "/api/v1/metrics/red?service=checkout&points=30&top=3")
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding red: %v", err)
	}
	q := fake.LastREDQuery
	if q.Service != "checkout" || q.Points != 30 || q.TopN != 3 || !q.ExcludeAux {
		t.Errorf("query not parsed: %+v", q)
	}
	if len(resp.Series) != 1 || len(resp.Series[0].Points) != 1 {
		t.Fatalf("payload wrong: %+v", resp)
	}
	p := resp.Series[0].Points[0]
	if p.ErrorRate != 0.1 || p.P95Ms != 40 {
		t.Errorf("point conversion wrong: %+v", p)
	}
	// 15m default window / 30 points = 30s buckets -> 60 reqs = 2/s.
	if resp.BucketSeconds != 30 || p.RatePerSec != 2 {
		t.Errorf("rate wrong: bucket=%v rate=%v", resp.BucketSeconds, p.RatePerSec)
	}
}

// buildProfilesBody marshals a minimal OTLP profiles request. Test-only
// pprofile usage: production code goes through storage/profilesadapter.
func buildProfilesBody(t *testing.T) []byte {
	t.Helper()
	profiles := pprofile.NewProfiles()
	dic := profiles.Dictionary()
	_, _ = pprofile.SetString(dic.StringTable(), "")
	nameIdx, _ := pprofile.SetString(dic.StringTable(), "work")
	fn := pprofile.NewFunction()
	fn.SetNameStrindex(nameIdx)
	fnIdx, _ := pprofile.SetFunction(dic.FunctionTable(), fn)
	loc := pprofile.NewLocation()
	loc.Lines().AppendEmpty().SetFunctionIndex(fnIdx)
	locIdx, _ := pprofile.SetLocation(dic.LocationTable(), loc)
	st := pprofile.NewStack()
	st.LocationIndices().Append(locIdx)
	stIdx, _ := pprofile.SetStack(dic.StackTable(), st)

	rp := profiles.ResourceProfiles().AppendEmpty()
	rp.Resource().Attributes().PutStr("service.name", "checkout")
	prof := rp.ScopeProfiles().AppendEmpty().Profiles().AppendEmpty()
	sample := prof.Samples().AppendEmpty()
	sample.SetStackIndex(stIdx)
	sample.Values().Append(3)

	body, err := pprofileotlp.NewExportRequestFromProfiles(profiles).MarshalProto()
	if err != nil {
		t.Fatalf("marshal profiles: %v", err)
	}
	return body
}

func TestProfilesIngest(t *testing.T) {
	fake := &storagetest.Fake{}
	mux := newMux(fake)

	post := func(body []byte, contentType string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1development/profiles", bytes.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := post(buildProfilesBody(t), "application/x-protobuf")
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest: got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(fake.Written) != 1 || fake.Written[0].Service != "checkout" || fake.Written[0].Value != 3 {
		t.Fatalf("samples not written: %+v", fake.Written)
	}
	if fake.Written[0].Frames[0] != "work" {
		t.Errorf("frames wrong: %v", fake.Written[0].Frames)
	}

	if rec := post([]byte("{}"), "application/json"); rec.Code != http.StatusBadRequest {
		t.Errorf("json content type: got %d, want 400", rec.Code)
	}
	if rec := post([]byte("garbage-not-proto!!!"), "application/x-protobuf"); rec.Code != http.StatusBadRequest {
		t.Errorf("garbage body: got %d, want 400", rec.Code)
	}
}

func TestFlamegraphEndpoint(t *testing.T) {
	fake := &storagetest.Fake{
		Flame: storage.FlameNode{
			Name: "root", Value: 8,
			Children: []*storage.FlameNode{
				{Name: "main", Value: 8, Children: []*storage.FlameNode{
					{Name: "handler", Value: 5, Self: 5},
					{Name: "loop", Value: 3, Self: 3},
				}},
			},
		},
	}
	mux := newMux(fake)

	var resp flamegraphResponse
	rec := get(t, mux, "/api/v1/profiles/flamegraph?service=checkout")
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding flamegraph: %v", err)
	}
	if fake.LastProfileQuery.Service != "checkout" {
		t.Errorf("service not parsed: %+v", fake.LastProfileQuery)
	}
	if resp.Root.Value != 8 || len(resp.Root.Children) != 1 {
		t.Fatalf("root wrong: %+v", resp.Root)
	}
	kids := resp.Root.Children[0].Children
	if len(kids) != 2 || kids[0].Name != "handler" || kids[0].Self != 5 {
		t.Errorf("children wrong: %+v", kids)
	}
}

func TestAgentsEndpoint(t *testing.T) {
	seen := time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC)
	older := seen.Add(-2 * time.Minute)
	fake := &storagetest.Fake{
		Agents: []storage.AgentNode{
			{Node: "worker-1", LastSeen: seen, Metrics: &seen, Logs: &older},
		},
	}
	mux := newMux(fake)

	var resp agentsResponse
	rec := get(t, mux, "/api/v1/agents?windowSec=300")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding agents: %v", err)
	}
	if fake.LastAgentQuery.Tenant != storage.DefaultTenant {
		t.Errorf("tenant = %q, want default", fake.LastAgentQuery.Tenant)
	}
	if fake.LastAgentQuery.Window != 5*time.Minute {
		t.Errorf("window = %v, want 5m", fake.LastAgentQuery.Window)
	}
	if resp.WindowSeconds != 300 || len(resp.Sensors) != 1 {
		t.Fatalf("response wrong: %+v", resp)
	}
	s := resp.Sensors[0]
	if s.Node != "worker-1" || s.LastSeen != "2026-07-05T10:00:00Z" {
		t.Errorf("sensor wrong: %+v", s)
	}
	if s.Signals.Metrics == nil || s.Signals.Logs == nil {
		t.Errorf("expected metrics+logs freshness, got %+v", s.Signals)
	}
	if s.Signals.Traces != nil || s.Signals.Profiles != nil {
		t.Errorf("expected nil traces/profiles freshness, got %+v", s.Signals)
	}

	// Tenant header is honored (the enterprise seam).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	req.Header.Set("X-Avuru-Tenant", "staging")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req)
	if fake.LastAgentQuery.Tenant != "staging" {
		t.Errorf("tenant header not honored: %q", fake.LastAgentQuery.Tenant)
	}
}

func TestProjectsEndpoint(t *testing.T) {
	fake := &storagetest.Fake{Tenants: []string{"default", "prod-eu"}}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{Projects: []string{"staging", "default"}})

	var resp projectsResponse
	rec := get(t, mux, "/api/v1/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding projects: %v", err)
	}
	// Sorted union: default (always), prod-eu (data), staging (config).
	want := []projectDTO{
		{ID: "default", Source: "default"},
		{ID: "prod-eu", Source: "data"},
		{ID: "staging", Source: "config"},
	}
	if len(resp.Projects) != len(want) {
		t.Fatalf("projects = %+v, want %+v", resp.Projects, want)
	}
	for i, p := range want {
		if resp.Projects[i] != p {
			t.Errorf("projects[%d] = %+v, want %+v", i, resp.Projects[i], p)
		}
	}
}

func TestProjectsEndpointStoreDown(t *testing.T) {
	// The switcher must always render: store down -> config list, 200.
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return nil }, Config{Projects: []string{"staging"}})

	var resp projectsResponse
	rec := get(t, mux, "/api/v1/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 during store outage", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding projects: %v", err)
	}
	if len(resp.Projects) != 2 || resp.Projects[0].ID != "default" || resp.Projects[1].ID != "staging" {
		t.Errorf("projects during outage = %+v", resp.Projects)
	}
}

func TestProjectsEndpointListTenantsError(t *testing.T) {
	// A flaky store degrades to config-only, never 5xx.
	fake := &storagetest.Fake{TenantsErr: errors.New("boom")}
	mux := http.NewServeMux()
	Register(mux, func() storage.Store { return fake }, Config{})

	rec := get(t, mux, "/api/v1/projects")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 when ListTenants fails", rec.Code)
	}
}
