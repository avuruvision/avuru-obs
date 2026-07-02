//go:build integration

package clickhouse

import (
	"context"
	"strings"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// startClickHouse runs the pinned ClickHouse image and applies the schema via
// the hub-owned migrator (the same `Migrate` used in compose and k8s) —
// dogfooded here so schema drift between migrations and queries fails first.
func startClickHouse(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	req := tc.ContainerRequest{
		Image: "clickhouse/clickhouse-server:26.3",
		Env: map[string]string{
			"CLICKHOUSE_USER":     "avuru",
			"CLICKHOUSE_PASSWORD": "avuru",
			"CLICKHOUSE_DB":       "otel",
		},
		ExposedPorts: []string{"9000/tcp", "8123/tcp"},
		WaitingFor:   wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(2 * time.Minute),
	}
	ctr, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		t.Fatalf("starting clickhouse container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}

	store, err := New(ctx, Config{Addr: host + ":" + port.Port(), Database: "otel", Username: "avuru", Password: "avuru"})
	if err != nil {
		t.Fatalf("connecting store: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrating schema: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestMigrateIsIdempotent guards the migrator + retention: tables created, the
// ledger records exactly the applied versions, a second run is a no-op, and
// ApplyRetention writes a TTL clause.
func TestMigrateIsIdempotent(t *testing.T) {
	store := startClickHouse(t) // migrated once already
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var count uint64
	if err := store.conn.QueryRow(ctx, "SELECT count() FROM otel.schema_migrations").Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations: %v", err)
	}
	if count != 4 {
		t.Fatalf("schema_migrations has %d rows, want 4", count)
	}

	tables := append([]string{"otel_traces", "otel_logs", "otel_traces_trace_id_ts", "profiling_stacks", "profiling_samples"}, metricsTables...)
	for _, tbl := range tables {
		var n uint64
		if err := store.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database='otel' AND name=?", tbl).Scan(&n); err != nil {
			t.Fatalf("checking table %s: %v", tbl, err)
		}
		if n != 1 {
			t.Errorf("table %s missing after migrate", tbl)
		}
	}

	if err := store.ApplyRetention(ctx, Retention{TracesDays: 7, LogsDays: 3, MetricsDays: 5, ProfilesDays: 2}); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}
	var ddl string
	if err := store.conn.QueryRow(ctx, "SHOW CREATE TABLE otel.otel_logs").Scan(&ddl); err != nil {
		t.Fatalf("SHOW CREATE otel_logs: %v", err)
	}
	if !strings.Contains(ddl, "toIntervalDay(3)") {
		t.Errorf("logs TTL not applied; DDL:\n%s", ddl)
	}
	if err := store.conn.QueryRow(ctx, "SHOW CREATE TABLE otel.otel_metrics_gauge").Scan(&ddl); err != nil {
		t.Fatalf("SHOW CREATE otel_metrics_gauge: %v", err)
	}
	if !strings.Contains(ddl, "toIntervalDay(5)") {
		t.Errorf("metrics TTL not applied; DDL:\n%s", ddl)
	}
	if err := store.conn.QueryRow(ctx, "SHOW CREATE TABLE otel.profiling_samples").Scan(&ddl); err != nil {
		t.Fatalf("SHOW CREATE profiling_samples: %v", err)
	}
	if !strings.Contains(ddl, "toIntervalDay(2)") {
		t.Errorf("profiles TTL not applied; DDL:\n%s", ddl)
	}
}

// TestMetricsSchemaAcceptsExporterShape guards the frozen 0003 contract: an
// INSERT with the exporter's explicit column list (create_schema:false path)
// must succeed, and the Avuru Tenant DEFAULT must materialize.
func TestMetricsSchemaAcceptsExporterShape(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC()

	batch, err := store.conn.PrepareBatch(ctx, `INSERT INTO otel_metrics_gauge
		(ResourceAttributes, ResourceSchemaUrl, ScopeName, ScopeVersion, ScopeAttributes,
		 ScopeDroppedAttrCount, ScopeSchemaUrl, ServiceName, MetricName, MetricDescription,
		 MetricUnit, Attributes, StartTimeUnix, TimeUnix, Value, Flags)`)
	if err != nil {
		t.Fatalf("preparing gauge batch: %v", err)
	}
	err = batch.Append(
		map[string]string{"k8s.node.name": "node-a"}, "", "kubeletstats", "1", map[string]string{},
		uint32(0), "", "node-a", "k8s.node.cpu.usage", "", "1",
		map[string]string{}, now, now, 0.42, uint32(0),
	)
	if err != nil {
		t.Fatalf("appending gauge point: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending gauge batch: %v", err)
	}

	var tenant string
	var value float64
	if err := store.conn.QueryRow(ctx,
		"SELECT Tenant, Value FROM otel.otel_metrics_gauge WHERE MetricName = 'k8s.node.cpu.usage'",
	).Scan(&tenant, &value); err != nil {
		t.Fatalf("reading gauge row: %v", err)
	}
	if tenant != "default" || value != 0.42 {
		t.Errorf("gauge row wrong: tenant=%q value=%v", tenant, value)
	}
}

type testSpan struct {
	ts       time.Time
	traceID  string
	spanID   string
	parentID string
	name     string
	kind     string
	service  string
	duration time.Duration
	status   string
}

func insertSpans(t *testing.T, s *Store, spans []testSpan) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes, Duration, StatusCode, StatusMessage,
		 Events.Timestamp, Events.Name, Events.Attributes,
		 Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	for _, sp := range spans {
		err := batch.Append(
			sp.ts, sp.traceID, sp.spanID, sp.parentID, "", sp.name, sp.kind, sp.service,
			map[string]string{"service.name": sp.service}, "test", "1", map[string]string{"k": "v"},
			uint64(sp.duration.Nanoseconds()), sp.status, "",
			[]time.Time{}, []string{}, []map[string]string{},
			[]string{}, []string{}, []string{}, []map[string]string{},
		)
		if err != nil {
			t.Fatalf("appending span: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending batch: %v", err)
	}
}

type testLog struct {
	ts       time.Time
	traceID  string
	spanID   string
	severity string
	sevNum   uint8
	service  string
	body     string
}

func insertLogs(t *testing.T, s *Store, logs []testLog) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_logs
		(Timestamp, TraceId, SpanId, SeverityText, SeverityNumber, ServiceName, Body, LogAttributes)`)
	if err != nil {
		t.Fatalf("preparing logs batch: %v", err)
	}
	for _, l := range logs {
		if err := batch.Append(l.ts, l.traceID, l.spanID, l.severity, l.sevNum, l.service, l.body, map[string]string{"k": "v"}); err != nil {
			t.Fatalf("appending log: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending logs batch: %v", err)
	}
}

func TestLogsIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	logs := []testLog{
		{base.Add(1 * time.Minute), "trace-aaaa", "span-1", "ERROR", 17, "checkout", "order lookup failed: connection refused"},
		{base.Add(2 * time.Minute), "trace-aaaa", "span-2", "WARN", 13, "checkout", "degraded, served error page"},
		{base.Add(3 * time.Minute), "", "", "INFO", 9, "frontend", "request handled ok"},
	}
	insertLogs(t, store, logs)
	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}

	t.Run("SearchAllNewestFirst", func(t *testing.T) {
		page, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("SearchLogs: %v", err)
		}
		if len(page.Logs) != 3 {
			t.Fatalf("got %d logs, want 3", len(page.Logs))
		}
		if page.Logs[0].Service != "frontend" { // newest (base+3)
			t.Errorf("expected newest log first, got %+v", page.Logs[0])
		}
		if page.Logs[0].Attributes["k"] != "v" {
			t.Errorf("attributes not round-tripped: %+v", page.Logs[0].Attributes)
		}
	})

	t.Run("FilterService", func(t *testing.T) {
		page, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Range: tr, Service: "checkout"})
		if err != nil {
			t.Fatalf("SearchLogs service: %v", err)
		}
		if len(page.Logs) != 2 {
			t.Errorf("got %d checkout logs, want 2", len(page.Logs))
		}
	})

	t.Run("FilterSeverity", func(t *testing.T) {
		page, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Range: tr, MinSeverity: "ERROR"})
		if err != nil {
			t.Fatalf("SearchLogs severity: %v", err)
		}
		if len(page.Logs) != 1 || page.Logs[0].Severity != "ERROR" {
			t.Errorf("severity filter wrong: %+v", page.Logs)
		}
	})

	t.Run("FullText", func(t *testing.T) {
		page, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Range: tr, Query: "Connection Refused"})
		if err != nil {
			t.Fatalf("SearchLogs fulltext: %v", err)
		}
		if len(page.Logs) != 1 {
			t.Errorf("fulltext got %d, want 1", len(page.Logs))
		}
	})

	t.Run("Pagination", func(t *testing.T) {
		p1, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Range: tr, Limit: 2})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(p1.Logs) != 2 || p1.NextCursor == nil {
			t.Fatalf("page1 wrong: %d logs cursor=%v", len(p1.Logs), p1.NextCursor)
		}
		p2, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Range: tr, Limit: 2, Cursor: p1.NextCursor})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(p2.Logs) != 1 || p2.NextCursor != nil {
			t.Fatalf("page2 wrong: %d logs cursor=%v", len(p2.Logs), p2.NextCursor)
		}
	})

	t.Run("LogsForTrace", func(t *testing.T) {
		got, err := store.LogsForTrace(ctx, "default", "trace-aaaa")
		if err != nil {
			t.Fatalf("LogsForTrace: %v", err)
		}
		if len(got) != 2 || got[0].SpanID != "span-1" { // oldest first
			t.Fatalf("LogsForTrace wrong: %+v", got)
		}
	})
}

func TestStoreIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	spans := []testSpan{
		// trace A: frontend root (error) + downstream client span
		{base.Add(1 * time.Minute), "aaaa0001", "s1", "", "GET /dispatch", "Server", "frontend", 500 * time.Millisecond, "Error"},
		{base.Add(1*time.Minute + 10*time.Millisecond), "aaaa0001", "s2", "s1", "SQL SELECT", "Client", "frontend", 100 * time.Millisecond, "Unset"},
		// trace B: frontend root ok
		{base.Add(2 * time.Minute), "bbbb0002", "s3", "", "GET /dispatch", "Server", "frontend", 50 * time.Millisecond, "Unset"},
		// trace C: driver root ok
		{base.Add(3 * time.Minute), "cccc0003", "s4", "", "FindNearest", "Server", "driver", 20 * time.Millisecond, "Unset"},
	}
	insertSpans(t, store, spans)

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}

	t.Run("ListServices", func(t *testing.T) {
		got, err := store.ListServices(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("ListServices: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d services, want 2 (%+v)", len(got), got)
		}
		if got[0].Name != "frontend" || got[0].SpanCount != 2 || got[0].ErrorCount != 1 {
			t.Errorf("frontend stats wrong: %+v", got[0])
		}
	})

	t.Run("TraceOverview", func(t *testing.T) {
		got, err := store.TraceOverview(ctx, storage.OverviewQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("TraceOverview: %v", err)
		}
		if len(got) != 2 { // (frontend, GET /dispatch) ×2 roots and (driver, FindNearest)
			t.Fatalf("got %d operations, want 2 (%+v)", len(got), got)
		}
		if got[0].Service != "frontend" || got[0].Count != 2 || got[0].ErrorCount != 1 {
			t.Errorf("frontend op stats wrong: %+v", got[0])
		}
	})

	t.Run("SearchTracesAndPagination", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Limit: 2})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 2 || page.NextCursor == nil {
			t.Fatalf("page1: got %d traces, cursor %v", len(page.Traces), page.NextCursor)
		}
		if page.Traces[0].TraceID != "cccc0003" { // newest first
			t.Errorf("expected newest trace first, got %s", page.Traces[0].TraceID)
		}
		if page.Traces[1].TraceID != "bbbb0002" || page.Traces[1].SpanCount != 1 {
			t.Errorf("trace B summary wrong: %+v", page.Traces[1])
		}

		page2, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Limit: 2, Cursor: page.NextCursor})
		if err != nil {
			t.Fatalf("SearchTraces page2: %v", err)
		}
		if len(page2.Traces) != 1 || page2.Traces[0].TraceID != "aaaa0001" || page2.NextCursor != nil {
			t.Fatalf("page2 wrong: %+v cursor=%v", page2.Traces, page2.NextCursor)
		}
		if page2.Traces[0].SpanCount != 2 || page2.Traces[0].ErrorCount != 1 {
			t.Errorf("trace A aggregates wrong: %+v", page2.Traces[0])
		}
	})

	t.Run("SearchTracesErrorFilter", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Status: "error"})
		if err != nil {
			t.Fatalf("SearchTraces error filter: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "aaaa0001" {
			t.Fatalf("error filter wrong: %+v", page.Traces)
		}
	})

	t.Run("GetTrace", func(t *testing.T) {
		got, err := store.GetTrace(ctx, "default", "aaaa0001")
		if err != nil {
			t.Fatalf("GetTrace: %v", err)
		}
		if len(got.Spans) != 2 {
			t.Fatalf("got %d spans, want 2", len(got.Spans))
		}
		if got.Spans[0].SpanID != "s1" || got.Spans[1].ParentSpanID != "s1" {
			t.Errorf("span tree wrong: %+v", got.Spans)
		}
		if got.Spans[0].Attributes["k"] != "v" {
			t.Errorf("span attributes not round-tripped: %+v", got.Spans[0].Attributes)
		}
	})

	t.Run("GetTraceNotFound", func(t *testing.T) {
		_, err := store.GetTrace(ctx, "default", "doesnotexist")
		if err != storage.ErrNotFound {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("TraceHeatmap", func(t *testing.T) {
		hm, err := store.TraceHeatmap(ctx, storage.HeatmapQuery{Tenant: "default", Range: tr, TimeBuckets: 10, DurationBuckets: 12})
		if err != nil {
			t.Fatalf("TraceHeatmap: %v", err)
		}
		if len(hm.DurationBounds) != 12 {
			t.Fatalf("got %d duration bounds, want 12", len(hm.DurationBounds))
		}
		var total, errs uint64
		for _, c := range hm.Cells {
			total += c.Count
			errs += c.ErrorCount
			if c.TimeBucket < 0 || c.TimeBucket > 9 || c.DurationBucket < 0 || c.DurationBucket > 11 {
				t.Errorf("cell out of range: %+v", c)
			}
		}
		if total != 3 || errs != 1 { // 3 root spans, 1 error
			t.Errorf("heatmap totals wrong: total=%d errs=%d", total, errs)
		}
	})

	t.Run("TenantIsolation", func(t *testing.T) {
		got, err := store.ListServices(ctx, storage.ServiceQuery{Tenant: "other", Range: tr})
		if err != nil {
			t.Fatalf("ListServices other tenant: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("tenant isolation broken: %+v", got)
		}
	})

	t.Run("SystemStats", func(t *testing.T) {
		st, err := store.SystemStats(ctx)
		if err != nil {
			t.Fatalf("SystemStats: %v", err)
		}
		var traces *storage.SignalStats
		for i := range st.Signals {
			if st.Signals[i].Signal == "traces" {
				traces = &st.Signals[i]
			}
		}
		if traces == nil || traces.Rows < 4 || traces.Bytes == 0 || traces.Newest == nil {
			t.Fatalf("traces stats wrong: %+v", traces)
		}
		if len(st.Disks) == 0 || st.Disks[0].TotalBytes == 0 {
			t.Errorf("disks wrong: %+v", st.Disks)
		}
	})
}

// insertGauge writes one exporter-shaped gauge point.
func insertGauge(t *testing.T, s *Store, ts time.Time, metric string, res map[string]string, value float64) {
	t.Helper()
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_metrics_gauge
		(ResourceAttributes, ScopeName, ServiceName, MetricName, MetricUnit, Attributes, StartTimeUnix, TimeUnix, Value, Flags)`)
	if err != nil {
		t.Fatalf("preparing gauge insert: %v", err)
	}
	if err := batch.Append(res, "kubeletstats", "", metric, "1", map[string]string{}, ts, ts, value, uint32(0)); err != nil {
		t.Fatalf("appending gauge: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending gauge: %v", err)
	}
}

// insertSum writes one exporter-shaped cumulative-sum point.
func insertSum(t *testing.T, s *Store, ts time.Time, metric string, res, attrs map[string]string, value float64) {
	t.Helper()
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_metrics_sum
		(ResourceAttributes, ScopeName, ServiceName, MetricName, MetricUnit, Attributes, StartTimeUnix, TimeUnix, Value, Flags, AggregationTemporality, IsMonotonic)`)
	if err != nil {
		t.Fatalf("preparing sum insert: %v", err)
	}
	if err := batch.Append(res, "kubeletstats", "", metric, "By", attrs, ts, ts, value, uint32(0), int32(2), true); err != nil {
		t.Fatalf("appending sum: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending sum: %v", err)
	}
}

func TestInfraMetricsIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	nodeRes := map[string]string{"k8s.node.name": "node-a"}
	podRes := map[string]string{
		"k8s.node.name": "node-a", "k8s.pod.name": "web-1",
		"k8s.namespace.name": "shop", "k8s.deployment.name": "web",
	}

	// Two samples so latest-wins and the series has >1 bucket.
	insertGauge(t, store, base.Add(1*time.Minute), "k8s.node.cpu.usage", nodeRes, 1.0)
	insertGauge(t, store, base.Add(5*time.Minute), "k8s.node.cpu.usage", nodeRes, 2.0)
	insertGauge(t, store, base.Add(5*time.Minute), "k8s.node.memory.usage", nodeRes, 2048)
	insertGauge(t, store, base.Add(5*time.Minute), "k8s.node.memory.available", nodeRes, 4096)
	// Cumulative network counter: 1000 -> 7000 over the window (receive).
	insertSum(t, store, base.Add(1*time.Minute), "k8s.node.network.io", nodeRes, map[string]string{"direction": "receive", "interface": "eth0"}, 1000)
	insertSum(t, store, base.Add(5*time.Minute), "k8s.node.network.io", nodeRes, map[string]string{"direction": "receive", "interface": "eth0"}, 7000)
	// Pod gauges.
	insertGauge(t, store, base.Add(5*time.Minute), "k8s.pod.cpu.usage", podRes, 0.25)
	insertGauge(t, store, base.Add(5*time.Minute), "k8s.pod.memory.usage", podRes, 512)

	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}

	nodes, err := store.ListNodeStats(ctx, storage.InfraQuery{Tenant: "default", Range: tr, Points: 6})
	if err != nil {
		t.Fatalf("ListNodeStats: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1 (%+v)", len(nodes), nodes)
	}
	n := nodes[0]
	if n.Name != "node-a" || n.CPUUsage != 2.0 || n.MemoryUsage != 2048 || n.MemoryAvailable != 4096 {
		t.Errorf("node latest wrong: %+v", n)
	}
	wantRate := 6000.0 / tr.End.Sub(tr.Start).Seconds()
	if n.NetworkRxRate < wantRate*0.99 || n.NetworkRxRate > wantRate*1.01 {
		t.Errorf("rx rate = %v, want ~%v", n.NetworkRxRate, wantRate)
	}
	if n.PodCount != 1 {
		t.Errorf("pod count = %d, want 1", n.PodCount)
	}
	if len(n.CPUSeries) < 2 {
		t.Errorf("cpu series too short: %+v", n.CPUSeries)
	}

	pods, err := store.ListPodStats(ctx, storage.InfraQuery{Tenant: "default", Range: tr, Node: "node-a"})
	if err != nil {
		t.Fatalf("ListPodStats: %v", err)
	}
	if len(pods) != 1 {
		t.Fatalf("got %d pods, want 1 (%+v)", len(pods), pods)
	}
	p := pods[0]
	if p.Name != "web-1" || p.Namespace != "shop" || p.Node != "node-a" || p.Workload != "web" || p.CPUUsage != 0.25 || p.MemoryUsage != 512 {
		t.Errorf("pod stats wrong: %+v", p)
	}

	// Node filter that matches nothing.
	none, err := store.ListPodStats(ctx, storage.InfraQuery{Tenant: "default", Range: tr, Node: "node-b"})
	if err != nil {
		t.Fatalf("ListPodStats node-b: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("node filter leaked: %+v", none)
	}
}

func TestREDSeriesIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	spans := []testSpan{
		{base.Add(1 * time.Minute), "aaaa0001", "s1", "", "GET /x", "Server", "frontend", 100 * time.Millisecond, "Unset"},
		{base.Add(1*time.Minute + time.Second), "aaaa0002", "s2", "", "GET /x", "Server", "frontend", 200 * time.Millisecond, "Error"},
		{base.Add(4 * time.Minute), "aaaa0003", "s3", "", "GET /x", "Server", "frontend", 50 * time.Millisecond, "Unset"},
		{base.Add(4 * time.Minute), "aaaa0004", "s4", "", "Find", "Server", "driver", 20 * time.Millisecond, "Unset"},
	}
	insertSpans(t, store, spans)
	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}

	// One service, 6 one-minute buckets.
	series, err := store.REDSeries(ctx, storage.REDQuery{Tenant: "default", Range: tr, Service: "frontend", Points: 6})
	if err != nil {
		t.Fatalf("REDSeries: %v", err)
	}
	if len(series) != 1 || series[0].Service != "frontend" {
		t.Fatalf("series wrong: %+v", series)
	}
	if len(series[0].Points) != 2 { // minutes 1 and 4 have data
		t.Fatalf("got %d points, want 2 (%+v)", len(series[0].Points), series[0].Points)
	}
	first := series[0].Points[0]
	if first.Count != 2 || first.ErrorCount != 1 {
		t.Errorf("bucket 1 wrong: %+v", first)
	}

	// Top-N across services: both services present.
	all, err := store.REDSeries(ctx, storage.REDQuery{Tenant: "default", Range: tr, Points: 6, TopN: 5})
	if err != nil {
		t.Fatalf("REDSeries topN: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("topN got %d series, want 2 (%+v)", len(all), all)
	}
	// TopN = 1 keeps only the busiest (frontend, 3 spans).
	top1, err := store.REDSeries(ctx, storage.REDQuery{Tenant: "default", Range: tr, Points: 6, TopN: 1})
	if err != nil {
		t.Fatalf("REDSeries top1: %v", err)
	}
	if len(top1) != 1 || top1[0].Service != "frontend" {
		t.Fatalf("top1 wrong: %+v", top1)
	}
}

func TestProfilesWriteIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	frames := []string{"handler", "main"}
	samples := []storage.ProfileSample{
		{Tenant: "default", Timestamp: now, Service: "checkout", SampleType: "samples:count", Frames: frames, Value: 5, Node: "node-a", Pod: "web-1", Container: "app"},
		{Tenant: "default", Timestamp: now.Add(time.Second), Service: "checkout", SampleType: "samples:count", Frames: frames, Value: 3, Node: "node-a", Pod: "web-1", Container: "app"},
		{Tenant: "default", Timestamp: now, Service: "driver", SampleType: "samples:count", Frames: []string{"loop", "main"}, Value: 1},
	}
	if err := store.WriteProfileSamples(ctx, samples); err != nil {
		t.Fatalf("WriteProfileSamples: %v", err)
	}

	var stackCount uint64
	if err := store.conn.QueryRow(ctx, "SELECT uniqExact(StackHash) FROM otel.profiling_stacks").Scan(&stackCount); err != nil {
		t.Fatalf("counting stacks: %v", err)
	}
	if stackCount != 2 { // two distinct stacks across three samples
		t.Errorf("got %d unique stacks, want 2", stackCount)
	}

	var total uint64
	if err := store.conn.QueryRow(ctx,
		"SELECT sum(Value) FROM otel.profiling_samples WHERE ServiceName = 'checkout'",
	).Scan(&total); err != nil {
		t.Fatalf("summing samples: %v", err)
	}
	if total != 8 {
		t.Errorf("checkout sample sum = %d, want 8", total)
	}

	// Samples join back to their frames via the hash.
	var frameCheck []string
	if err := store.conn.QueryRow(ctx, `
SELECT st.Frames FROM otel.profiling_samples AS sm
INNER JOIN otel.profiling_stacks AS st ON sm.StackHash = st.StackHash AND sm.Tenant = st.Tenant
WHERE sm.ServiceName = 'driver' LIMIT 1`).Scan(&frameCheck); err != nil {
		t.Fatalf("joining stack: %v", err)
	}
	if len(frameCheck) != 2 || frameCheck[0] != "loop" {
		t.Errorf("frames round-trip wrong: %v", frameCheck)
	}

	if err := store.WriteProfileSamples(ctx, nil); err != nil {
		t.Errorf("empty write must be a no-op: %v", err)
	}
}
