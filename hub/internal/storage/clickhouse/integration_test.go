//go:build integration

package clickhouse

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/migrations"
)

// startClickHouse runs the pinned ClickHouse image and applies the schema via
// the hub-owned migrator (the same `Migrate` used in compose and k8s) —
// dogfooded here so schema drift between migrations and queries fails first.
func startClickHouse(t *testing.T) *Store {
	t.Helper()
	store := startClickHouseContainer(t)
	if err := store.Migrate(context.Background(), modules.AllSet()); err != nil {
		t.Fatalf("migrating schema: %v", err)
	}
	return store
}

// startClickHouseContainer runs the pinned ClickHouse image and connects a
// Store WITHOUT migrating — for tests exercising the migrator itself.
func startClickHouseContainer(t *testing.T) *Store {
	t.Helper()
	store, _ := startClickHouseContainerAddr(t)
	return store
}

// startClickHouseContainerAddr also returns the address, so a test can open a
// second Store against a different database on the same server.
func startClickHouseContainerAddr(t *testing.T) (*Store, string) {
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

	addr := host + ":" + port.Port()
	store, err := New(ctx, Config{Addr: addr, Database: "otel", Username: "avuru", Password: "avuru"})
	if err != nil {
		t.Fatalf("connecting store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, addr
}

// TestMigrateIsIdempotent guards the migrator + retention: tables created, the
// ledger records exactly the applied versions, a second run is a no-op, and
// ApplyRetention writes a TTL clause.
func TestMigrateIsIdempotent(t *testing.T) {
	store := startClickHouse(t) // migrated once already
	ctx := context.Background()

	if err := store.Migrate(ctx, modules.AllSet()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}

	var count uint64
	if err := store.conn.QueryRow(ctx, "SELECT count() FROM otel.schema_migrations").Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations: %v", err)
	}
	if count != uint64(len(migrations.Ordered)) {
		t.Fatalf("schema_migrations has %d rows, want %d", count, len(migrations.Ordered))
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
		got, err := store.LogsForTrace(ctx, []string{"default"}, "trace-aaaa")
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
		// Entry spans only: (frontend, GET /dispatch) ×2 and (driver,
		// FindNearest); the Client SQL SELECT is not a request. Ordered by
		// service name for the UI's grouped rendering.
		if len(got) != 2 {
			t.Fatalf("got %d operations, want 2 (%+v)", len(got), got)
		}
		if got[0].Service != "driver" || got[0].Count != 1 {
			t.Errorf("driver op stats wrong: %+v", got[0])
		}
		if got[1].Service != "frontend" || got[1].Count != 2 || got[1].ErrorCount != 1 {
			t.Errorf("frontend op stats wrong: %+v", got[1])
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
		got, err := store.GetTrace(ctx, []string{"default"}, "aaaa0001")
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
		if got.Spans[0].ScopeName != "test" || got.Spans[0].ScopeVersion != "1" {
			t.Errorf("instrumentation scope not round-tripped: %q@%q", got.Spans[0].ScopeName, got.Spans[0].ScopeVersion)
		}
	})

	t.Run("GetTraceNotFound", func(t *testing.T) {
		_, err := store.GetTrace(ctx, []string{"default"}, "doesnotexist")
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

func TestFlamegraphIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// checkout: two stacks sharing the root frame "main" (leaf-first frames).
	samples := []storage.ProfileSample{
		{Tenant: "default", Timestamp: now, Service: "checkout", SampleType: "samples:count", Frames: []string{"handler", "main"}, Value: 5},
		{Tenant: "default", Timestamp: now, Service: "checkout", SampleType: "samples:count", Frames: []string{"encode", "handler", "main"}, Value: 3},
		{Tenant: "default", Timestamp: now, Service: "driver", SampleType: "samples:count", Frames: []string{"loop"}, Value: 2},
	}
	if err := store.WriteProfileSamples(ctx, samples); err != nil {
		t.Fatalf("WriteProfileSamples: %v", err)
	}
	tr := storage.TimeRange{Start: now.Add(-time.Minute), End: now.Add(time.Minute)}

	services, err := store.ListProfiledServices(ctx, storage.ProfileQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("ListProfiledServices: %v", err)
	}
	if len(services) != 2 || services[0].Name != "checkout" || services[0].Samples != 8 {
		t.Fatalf("profiled services wrong: %+v", services)
	}

	root, err := store.ProfileFlamegraph(ctx, storage.ProfileQuery{Tenant: "default", Range: tr, Service: "checkout"})
	if err != nil {
		t.Fatalf("ProfileFlamegraph: %v", err)
	}
	if root.Value != 8 || len(root.Children) != 1 {
		t.Fatalf("root wrong: %+v", root)
	}
	main := root.Children[0]
	if main.Name != "main" || main.Value != 8 || main.Self != 0 {
		t.Fatalf("main frame wrong: %+v", main)
	}
	if len(main.Children) != 1 || main.Children[0].Name != "handler" {
		t.Fatalf("handler frame missing: %+v", main.Children)
	}
	handler := main.Children[0]
	if handler.Value != 8 || handler.Self != 5 {
		t.Errorf("handler value/self wrong: %+v", handler)
	}
	if len(handler.Children) != 1 || handler.Children[0].Name != "encode" || handler.Children[0].Value != 3 || handler.Children[0].Self != 3 {
		t.Errorf("encode frame wrong: %+v", handler.Children)
	}

	// Tenant isolation.
	other, err := store.ProfileFlamegraph(ctx, storage.ProfileQuery{Tenant: "other", Range: tr, Service: "checkout"})
	if err != nil {
		t.Fatalf("other tenant: %v", err)
	}
	if other.Value != 0 || len(other.Children) != 0 {
		t.Errorf("tenant isolation broken: %+v", other)
	}
}

// TestAuxDBPingExclusion: parentless DB health pings (Lettuce INFO/PING,
// actuator SQL probes) must not list as one-span traces by default, but stay
// reachable via includeAux — and a DB span INSIDE a real trace is untouched.
func TestAuxDBPingExclusion(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	batch, err := store.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, SpanAttributes, Duration, StatusCode)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	rows := []struct {
		trace, span, parent, name, kind string
		attrs                           map[string]string
	}{
		// Orphan Redis health ping — aux noise.
		{"ping0001", "p1", "", "info", "Client", map[string]string{"db.system": "redis", "db.operation": "INFO"}},
		// Real request with a Redis INFO child — must stay fully visible.
		{"real0001", "r1", "", "GET /checkout", "Server", map[string]string{}},
		{"real0001", "r2", "r1", "info", "Client", map[string]string{"db.system": "redis", "db.operation": "INFO"}},
	}
	for i, r := range rows {
		if err := batch.Append(base.Add(time.Duration(i)*time.Second), r.trace, r.span, r.parent, r.name, r.kind, "checkout", r.attrs, uint64(time.Millisecond), "Unset"); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send: %v", err)
	}
	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}

	page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, ExcludeAux: true})
	if err != nil {
		t.Fatalf("SearchTraces: %v", err)
	}
	if len(page.Traces) != 1 || page.Traces[0].TraceID != "real0001" {
		t.Fatalf("default list wrong (want only real0001): %+v", page.Traces)
	}
	if page.Traces[0].SpanCount != 2 {
		t.Errorf("real trace should keep its DB child: %+v", page.Traces[0])
	}

	all, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("SearchTraces includeAux: %v", err)
	}
	if len(all.Traces) != 2 {
		t.Fatalf("includeAux should surface the ping too: %+v", all.Traces)
	}
}

// TestSearchTracesOrphanResilience: the trace list must surface traces whose
// true root span is absent (exported to another backend / dropped), keyed on
// their effective root (the earliest present span), while complete traces stay
// unchanged — one row, the root's own duration. The service filter matches
// any participating span (see TestSearchTracesParticipantFilter).
func TestSearchTracesOrphanResilience(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	spans := []testSpan{
		// complete: frontend root (500ms) + driver client child (100ms).
		{base.Add(1 * time.Minute), "cmpl0001", "c1", "", "GET /a", "Server", "frontend", 500 * time.Millisecond, "Unset"},
		{base.Add(1*time.Minute + 10*time.Millisecond), "cmpl0001", "c2", "c1", "SQL", "Client", "driver", 100 * time.Millisecond, "Unset"},
		// orphan1: a single child whose parent span is missing from the store.
		{base.Add(2 * time.Minute), "orph0001", "o1", "missingp", "SQL SELECT", "Client", "checkout", 30 * time.Millisecond, "Unset"},
		// orphan2: two children with NO root, tied on Timestamp — SpanId breaks
		// the tie so the representative is deterministically "a1".
		{base.Add(3 * time.Minute), "orph0002", "a1", "px", "op-a1", "Client", "svc", 70 * time.Millisecond, "Unset"},
		{base.Add(3 * time.Minute), "orph0002", "b2", "px", "op-b2", "Client", "svc", 40 * time.Millisecond, "Unset"},
	}
	insertSpans(t, store, spans)
	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}

	t.Run("OrphanAppearsWithChildAsRoot", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "checkout"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "orph0001" {
			t.Fatalf("want 1 orphan trace orph0001, got %+v", page.Traces)
		}
		got := page.Traces[0]
		if got.RootService != "checkout" || got.RootOperation != "SQL SELECT" ||
			got.Duration != 30*time.Millisecond || got.SpanCount != 1 {
			t.Errorf("orphan representative wrong: %+v", got)
		}
	})

	t.Run("MultiChildOrphanEarliestSpanIdWins", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "svc"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "orph0002" {
			t.Fatalf("want 1 trace orph0002, got %+v", page.Traces)
		}
		// a1 (< b2) is the representative: its op name and its 70ms duration,
		// both spans counted.
		got := page.Traces[0]
		if got.RootOperation != "op-a1" || got.Duration != 70*time.Millisecond || got.SpanCount != 2 {
			t.Errorf("tiebreak representative wrong: %+v", got)
		}
	})

	t.Run("CompleteTraceUnchanged", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "frontend"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "cmpl0001" {
			t.Fatalf("want 1 trace cmpl0001, got %+v", page.Traces)
		}
		// Root's own duration (500ms), NOT wall-clock; both spans counted once.
		got := page.Traces[0]
		if got.RootOperation != "GET /a" || got.Duration != 500*time.Millisecond || got.SpanCount != 2 {
			t.Errorf("complete trace summary wrong: %+v", got)
		}
	})

	t.Run("FilterMatchesAnyParticipatingService", func(t *testing.T) {
		// "driver" is only a child of cmpl0001 (root is frontend); the
		// participant filter must surface the trace, still displayed under
		// its effective root.
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "driver"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "cmpl0001" {
			t.Fatalf("driver participates in cmpl0001; want it listed, got %+v", page.Traces)
		}
		if page.Traces[0].RootService != "frontend" {
			t.Errorf("display root must stay the effective root: %+v", page.Traces[0])
		}
	})

	t.Run("PaginationNewestMixed", func(t *testing.T) {
		p1, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Limit: 2})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(p1.Traces) != 2 || p1.NextCursor == nil {
			t.Fatalf("page1 got %d traces cursor=%v", len(p1.Traces), p1.NextCursor)
		}
		// newest by StartTime: orph0002 (base+3m), orph0001 (base+2m), cmpl0001 (base+1m).
		if p1.Traces[0].TraceID != "orph0002" || p1.Traces[1].TraceID != "orph0001" {
			t.Errorf("newest order wrong: %+v", p1.Traces)
		}
		p2, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Limit: 2, Cursor: p1.NextCursor})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(p2.Traces) != 1 || p2.Traces[0].TraceID != "cmpl0001" || p2.NextCursor != nil {
			t.Fatalf("page2 wrong: %+v cursor=%v", p2.Traces, p2.NextCursor)
		}
		seen := map[string]int{}
		for _, s := range append(p1.Traces, p2.Traces...) {
			seen[s.TraceID]++
		}
		if len(seen) != 3 || seen["cmpl0001"] != 1 || seen["orph0001"] != 1 || seen["orph0002"] != 1 {
			t.Errorf("pagination lost/duplicated traces: %v", seen)
		}
	})

	t.Run("PaginationSlowestMixed", func(t *testing.T) {
		p1, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Order: "slowest", Limit: 2})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		// slowest by representative duration: cmpl0001 (500ms), orph0002 (70ms), orph0001 (30ms).
		if len(p1.Traces) != 2 || p1.Traces[0].TraceID != "cmpl0001" || p1.Traces[1].TraceID != "orph0002" {
			t.Fatalf("slowest page1 wrong: %+v", p1.Traces)
		}
		p2, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Order: "slowest", Limit: 2, Cursor: p1.NextCursor})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(p2.Traces) != 1 || p2.Traces[0].TraceID != "orph0001" {
			t.Fatalf("slowest page2 wrong: %+v", p2.Traces)
		}
	})
}

// TestSearchTracesParticipantFilter: with a service filter, a trace matches
// when it CONTAINS a qualifying span of that service — operation/duration
// must hold on the same span, status is judged on that service's spans, and
// keyset pagination stays exact. Display stays the effective root throughout.
func TestSearchTracesParticipantFilter(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	spans := []testSpan{
		// Three gateway-rooted traces all containing a "notify" child; the
		// child errors only in the second, and durations differ per trace.
		{base.Add(1 * time.Minute), "gwtrace1", "g1", "", "GET /api/x", "Server", "gateway", 500 * time.Millisecond, "Unset"},
		{base.Add(1*time.Minute + time.Millisecond), "gwtrace1", "n1", "g1", "GET /notify", "Server", "notify", 100 * time.Millisecond, "Unset"},
		{base.Add(2 * time.Minute), "gwtrace2", "g2", "", "GET /api/x", "Server", "gateway", 400 * time.Millisecond, "Unset"},
		{base.Add(2*time.Minute + time.Millisecond), "gwtrace2", "n2", "g2", "GET /notify", "Server", "notify", 200 * time.Millisecond, "Error"},
		{base.Add(3 * time.Minute), "gwtrace3", "g3", "", "GET /api/x", "Server", "gateway", 300 * time.Millisecond, "Unset"},
		{base.Add(3*time.Minute + time.Millisecond), "gwtrace3", "n3", "g3", "SQL", "Client", "notify", 300 * time.Millisecond, "Unset"},
		// Aux-rooted trace containing notify: hidden by ExcludeAux even when
		// filtering by notify (aux is judged on the representative).
		{base.Add(4 * time.Minute), "auxtrace", "h1", "", "GET /healthz", "Server", "gateway", 5 * time.Millisecond, "Unset"},
		{base.Add(4*time.Minute + time.Millisecond), "auxtrace", "n4", "h1", "GET /notify", "Server", "notify", 2 * time.Millisecond, "Unset"},
	}
	insertSpans(t, store, spans)
	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}

	t.Run("ServiceAndOperationMatchSameSpan", func(t *testing.T) {
		// notify + the ROOT's operation: no notify span is named "GET /api/x",
		// so nothing matches — the combo must not mix spans.
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "notify", Operation: "GET /api/x"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 0 {
			t.Fatalf("operation must match the filtered service's span: %+v", page.Traces)
		}
		// notify + notify's own operation matches gwtrace3 only.
		page, err = store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "notify", Operation: "SQL"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "gwtrace3" {
			t.Fatalf("want gwtrace3, got %+v", page.Traces)
		}
	})

	t.Run("DurationJudgedOnServiceSpans", func(t *testing.T) {
		// notify spans: 100ms, 200ms, 300ms. Bounds 150–250ms select only the
		// 200ms one (gwtrace2), even though every ROOT is >= 300ms.
		page, err := store.SearchTraces(ctx, storage.TraceQuery{
			Tenant: "default", Range: tr, Service: "notify",
			MinDuration: 150 * time.Millisecond, MaxDuration: 250 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "gwtrace2" {
			t.Fatalf("want gwtrace2 only, got %+v", page.Traces)
		}
	})

	t.Run("StatusJudgedOnServiceSpans", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "notify", Status: "error"})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 1 || page.Traces[0].TraceID != "gwtrace2" {
			t.Fatalf("notify errors only in gwtrace2, got %+v", page.Traces)
		}
		page, err = store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "notify", Status: "ok", ExcludeAux: true})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		if len(page.Traces) != 2 {
			t.Fatalf("want gwtrace1+gwtrace3 for status=ok, got %+v", page.Traces)
		}
	})

	t.Run("AuxRootedTraceStaysHidden", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Service: "notify", ExcludeAux: true})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		for _, s := range page.Traces {
			if s.TraceID == "auxtrace" {
				t.Fatalf("aux-rooted trace must stay hidden by default: %+v", page.Traces)
			}
		}
		if len(page.Traces) != 3 {
			t.Fatalf("want the 3 real gateway traces, got %+v", page.Traces)
		}
	})

	t.Run("PaginationWithParticipantFilter", func(t *testing.T) {
		for _, order := range []string{"newest", "slowest"} {
			seen := map[string]int{}
			var cursor *storage.TraceCursor
			for range 5 {
				page, err := store.SearchTraces(ctx, storage.TraceQuery{
					Tenant: "default", Range: tr, Service: "notify", ExcludeAux: true,
					Order: order, Limit: 1, Cursor: cursor,
				})
				if err != nil {
					t.Fatalf("%s page: %v", order, err)
				}
				for _, s := range page.Traces {
					seen[s.TraceID]++
				}
				if page.NextCursor == nil {
					break
				}
				cursor = page.NextCursor
			}
			if len(seen) != 3 || seen["gwtrace1"] != 1 || seen["gwtrace2"] != 1 || seen["gwtrace3"] != 1 {
				t.Errorf("%s pagination lost/duplicated traces: %v", order, seen)
			}
		}
	})
}

// TestTraceOverviewEntrySpans: the overview counts entry spans (Server/
// Consumer) per (service, operation) — downstream services' operations show
// up, Client/Internal spans don't, and orphaned entry spans still count.
func TestTraceOverviewEntrySpans(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	spans := []testSpan{
		// One trace crossing gateway -> notification, plus internal/client noise.
		{base.Add(1 * time.Minute), "ovtrace1", "v1", "", "GET /api/pay", "Server", "gateway", 300 * time.Millisecond, "Unset"},
		{base.Add(1*time.Minute + time.Millisecond), "ovtrace1", "v2", "v1", "call payments", "Client", "gateway", 200 * time.Millisecond, "Unset"},
		{base.Add(1*time.Minute + 2*time.Millisecond), "ovtrace1", "v3", "v2", "POST /pay", "Server", "payments", 150 * time.Millisecond, "Error"},
		{base.Add(1*time.Minute + 3*time.Millisecond), "ovtrace1", "v4", "v3", "compute", "Internal", "payments", 50 * time.Millisecond, "Unset"},
		// Orphaned entry span (parent exported elsewhere) still counts.
		{base.Add(2 * time.Minute), "ovtrace2", "v5", "gone", "POST /pay", "Server", "payments", 100 * time.Millisecond, "Unset"},
	}
	insertSpans(t, store, spans)
	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}

	t.Run("ServiceFilterReturnsItsEntryOps", func(t *testing.T) {
		got, err := store.TraceOverview(ctx, storage.OverviewQuery{Tenant: "default", Range: tr, Service: "payments"})
		if err != nil {
			t.Fatalf("TraceOverview: %v", err)
		}
		if len(got) != 1 || got[0].Operation != "POST /pay" || got[0].Count != 2 || got[0].ErrorCount != 1 {
			t.Fatalf("payments ops wrong: %+v", got)
		}
	})

	t.Run("UnfilteredGroupsByService", func(t *testing.T) {
		got, err := store.TraceOverview(ctx, storage.OverviewQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("TraceOverview: %v", err)
		}
		// Client + Internal spans are not requests: exactly one op per service,
		// ordered by service name.
		if len(got) != 2 {
			t.Fatalf("got %d operations, want 2 (%+v)", len(got), got)
		}
		if got[0].Service != "gateway" || got[0].Operation != "GET /api/pay" {
			t.Errorf("gateway op wrong: %+v", got[0])
		}
		if got[1].Service != "payments" || got[1].Count != 2 {
			t.Errorf("payments op wrong: %+v", got[1])
		}
	})

	t.Run("HeatmapServiceFilterUsesEntrySpans", func(t *testing.T) {
		// payments never roots a trace; its heatmap must still fill.
		hm, err := store.TraceHeatmap(ctx, storage.HeatmapQuery{Tenant: "default", Range: tr, Service: "payments", TimeBuckets: 10, DurationBuckets: 12})
		if err != nil {
			t.Fatalf("TraceHeatmap: %v", err)
		}
		var total, errs uint64
		for _, c := range hm.Cells {
			total += c.Count
			errs += c.ErrorCount
		}
		if total != 2 || errs != 1 { // the two POST /pay entry spans, one error
			t.Errorf("filtered heatmap totals wrong: total=%d errs=%d", total, errs)
		}
		// Unfiltered stays root-based: only the gateway root counts.
		hm, err = store.TraceHeatmap(ctx, storage.HeatmapQuery{Tenant: "default", Range: tr, TimeBuckets: 10, DurationBuckets: 12})
		if err != nil {
			t.Fatalf("TraceHeatmap unfiltered: %v", err)
		}
		total = 0
		for _, c := range hm.Cells {
			total += c.Count
		}
		if total != 1 {
			t.Errorf("unfiltered heatmap should count roots only: total=%d", total)
		}
	})
}

func TestFindSpanTrace(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	insertSpans(t, store, []testSpan{
		{base.Add(1 * time.Minute), "spantrace01", "abcd1234abcd1234", "", "GET /x", "Server", "svc", time.Millisecond, "Unset"},
	})

	t.Run("Found", func(t *testing.T) {
		traceID, err := store.FindSpanTrace(ctx, []string{"default"}, "abcd1234abcd1234")
		if err != nil {
			t.Fatalf("FindSpanTrace: %v", err)
		}
		if traceID != "spantrace01" {
			t.Errorf("traceID = %q, want spantrace01", traceID)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		if _, err := store.FindSpanTrace(ctx, []string{"default"}, "0000000000000000"); err != storage.ErrNotFound {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("TenantIsolation", func(t *testing.T) {
		if _, err := store.FindSpanTrace(ctx, []string{"other"}, "abcd1234abcd1234"); err != storage.ErrNotFound {
			t.Fatalf("want ErrNotFound for other tenant, got %v", err)
		}
	})
}

// TestMigrateModuleGating guards the module framework's schema layer: an
// inactive module's migrations are skipped, SystemStats tolerates the absent
// tables, and enabling the module later is a plain re-run.
func TestMigrateModuleGating(t *testing.T) {
	store := startClickHouseContainer(t)
	ctx := context.Background()

	subset, err := modules.Parse("core,logs")
	if err != nil {
		t.Fatalf("parsing modules: %v", err)
	}
	if err := store.Migrate(ctx, subset); err != nil {
		t.Fatalf("Migrate(core,logs): %v", err)
	}

	tableCount := func(name string) uint64 {
		t.Helper()
		var n uint64
		if err := store.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database='otel' AND name=?", name).Scan(&n); err != nil {
			t.Fatalf("checking table %s: %v", name, err)
		}
		return n
	}

	for _, tbl := range []string{"otel_traces", "otel_traces_trace_id_ts", "otel_logs"} {
		if tableCount(tbl) != 1 {
			t.Errorf("table %s missing with core+logs active", tbl)
		}
	}
	for _, tbl := range append([]string{"profiling_stacks", "profiling_samples"}, metricsTables...) {
		if tableCount(tbl) != 0 {
			t.Errorf("table %s exists although its module is inactive", tbl)
		}
	}

	// The status view must not error on the absent tables and must not
	// report the disabled signals.
	stats, err := store.SystemStats(ctx)
	if err != nil {
		t.Fatalf("SystemStats with disabled modules: %v", err)
	}
	for _, sig := range stats.Signals {
		if sig.Signal == "profiles" || sig.Signal == "metrics" {
			t.Errorf("SystemStats reports disabled signal %q", sig.Signal)
		}
	}

	// Enabling the remaining modules later is a plain idempotent re-run.
	if err := store.Migrate(ctx, modules.AllSet()); err != nil {
		t.Fatalf("Migrate(all) after subset: %v", err)
	}
	for _, tbl := range append([]string{"profiling_stacks", "profiling_samples"}, metricsTables...) {
		if tableCount(tbl) != 1 {
			t.Errorf("table %s missing after enabling its module", tbl)
		}
	}
	var count uint64
	if err := store.conn.QueryRow(ctx, "SELECT count() FROM otel.schema_migrations").Scan(&count); err != nil {
		t.Fatalf("counting schema_migrations: %v", err)
	}
	if count != uint64(len(migrations.Ordered)) {
		t.Fatalf("schema_migrations has %d rows, want %d", count, len(migrations.Ordered))
	}
}

// insertHistogram writes one exporter-shaped explicit-bucket histogram point.
func insertHistogram(t *testing.T, s *Store, ts time.Time, metric string, res, attrs map[string]string, buckets []uint64, bounds []float64) {
	t.Helper()
	var count uint64
	for _, b := range buckets {
		count += b
	}
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_metrics_histogram
		(ResourceAttributes, ScopeName, ServiceName, MetricName, MetricUnit, Attributes, StartTimeUnix, TimeUnix, Count, Sum, BucketCounts, ExplicitBounds, Flags, Min, Max, AggregationTemporality)`)
	if err != nil {
		t.Fatalf("preparing histogram insert: %v", err)
	}
	if err := batch.Append(res, "obi", "", metric, "s", attrs, ts, ts, count, 0.0, buckets, bounds, uint32(0), 0.0, 0.0, int32(2)); err != nil {
		t.Fatalf("appending histogram: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending histogram: %v", err)
	}
}

// TestNetworkEdgeHealthIntegration exercises the RTT-p95-from-histogram SQL and
// the failed-connection sum against real ClickHouse.
func TestNetworkEdgeHealthIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)

	edge := map[string]string{"k8s.src.owner.name": "cart", "k8s.dst.owner.name": "payments"}
	bounds := []float64{0.01, 0.05, 0.1, 0.5} // seconds; 5 buckets (last is +Inf)

	// Two rows, same bounds, merged element-wise to [10,20,40,25,5], total 100.
	// cumulative [10,30,70,95,100]; p95 (>=95) first hits bucket 4 -> bound 0.5s.
	insertHistogram(t, store, base.Add(1*time.Minute), "obi.stat.tcp.rtt", nil, edge, []uint64{10, 20, 30, 5, 0}, bounds)
	insertHistogram(t, store, base.Add(2*time.Minute), "obi.stat.tcp.rtt", nil, edge, []uint64{0, 0, 10, 20, 5}, bounds)
	// A self-edge must be excluded.
	insertHistogram(t, store, base.Add(1*time.Minute), "obi.stat.tcp.rtt", nil,
		map[string]string{"k8s.src.owner.name": "cart", "k8s.dst.owner.name": "cart"}, []uint64{1, 0, 0, 0, 0}, bounds)
	// Failed connections: cumulative counter 3 + 4 = 7.
	insertSum(t, store, base.Add(1*time.Minute), "obi.stat.tcp.failed.connections", nil, edge, 3)
	insertSum(t, store, base.Add(2*time.Minute), "obi.stat.tcp.failed.connections", nil, edge, 4)

	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}
	health, err := store.NetworkEdgeHealth(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("NetworkEdgeHealth: %v", err)
	}
	if len(health) != 1 {
		t.Fatalf("want 1 edge (self-edge excluded), got %+v", health)
	}
	h := health[0]
	if h.Source != "cart" || h.Target != "payments" {
		t.Errorf("edge endpoints wrong: %+v", h)
	}
	if h.RTTMs != 500 {
		t.Errorf("RTT p95 = %v ms, want 500 (bound 0.5s)", h.RTTMs)
	}
	if h.FailedConnections != 7 {
		t.Errorf("failed connections = %d, want 7", h.FailedConnections)
	}
}

// TestSchemaStatusOnEmptyDatabase is the incident, reproduced: a database that
// exists but was never migrated. The check must report "nothing applied" and
// return NO error — treating a missing ledger as a backend fault is what would
// send callers back to retrying blindly instead of repairing.
func TestSchemaStatusOnEmptyDatabase(t *testing.T) {
	store := startClickHouseContainer(t) // deliberately NOT migrated
	ctx := context.Background()

	st, err := store.SchemaStatus(ctx, modules.AllSet())
	if err != nil {
		t.Fatalf("SchemaStatus on an unmigrated database: %v", err)
	}
	if st.Ready {
		t.Error("Ready = true on an unmigrated database")
	}
	if len(st.Applied) != 0 {
		t.Errorf("Applied = %v, want none", st.Applied)
	}
	if len(st.Missing) != len(migrations.Ordered) {
		t.Errorf("Missing has %d entries, want all %d", len(st.Missing), len(migrations.Ordered))
	}
	if st.Database != "otel" {
		t.Errorf("Database = %q, want otel", st.Database)
	}
}

func TestSchemaStatusAfterMigrate(t *testing.T) {
	store := startClickHouse(t)

	st, err := store.SchemaStatus(context.Background(), modules.AllSet())
	if err != nil {
		t.Fatalf("SchemaStatus: %v", err)
	}
	if !st.Ready || len(st.Missing) != 0 {
		t.Errorf("Ready=%v Missing=%v, want ready with nothing missing", st.Ready, st.Missing)
	}
	if len(st.Applied) != len(migrations.Ordered) {
		t.Errorf("Applied has %d entries, want %d", len(st.Applied), len(migrations.Ordered))
	}
}

// TestSchemaStatusModuleRestricted: an install running a subset of modules is
// COMPLETE for that subset. Comparing against every migration instead would
// leave the schema gate looping forever on a perfectly healthy install.
func TestSchemaStatusModuleRestricted(t *testing.T) {
	store := startClickHouseContainer(t)
	ctx := context.Background()

	subset, err := modules.Parse("core,error-tracking")
	if err != nil {
		t.Fatalf("parsing modules: %v", err)
	}
	if err := store.Migrate(ctx, subset); err != nil {
		t.Fatalf("Migrate(subset): %v", err)
	}

	st, err := store.SchemaStatus(ctx, subset)
	if err != nil {
		t.Fatalf("SchemaStatus(subset): %v", err)
	}
	if !st.Ready {
		t.Errorf("subset install not ready for its own module set: missing %v", st.Missing)
	}

	// The same store IS incomplete when the logs module is switched on — that's
	// how enabling a module later gets its schema.
	full, err := store.SchemaStatus(ctx, modules.AllSet())
	if err != nil {
		t.Fatalf("SchemaStatus(all): %v", err)
	}
	if full.Ready {
		t.Error("Ready = true against the full module set after a subset migrate")
	}
	for _, want := range []string{"0002_logs.sql", "0007_errors_from_logs.sql"} {
		if !slices.Contains(full.Missing, want) {
			t.Errorf("Missing = %v, want it to include %s", full.Missing, want)
		}
	}
}

// TestConcurrentMigrateIsSafe backs the no-lock claim in Migrate's doc: several
// hub replicas (or a replica racing the chart's migrate Job) must converge
// without erroring. Asserts distinct versions, since concurrent writers may
// each append a ledger row for the same version — harmless, because the ledger
// is read as a set.
func TestConcurrentMigrateIsSafe(t *testing.T) {
	store := startClickHouseContainer(t)
	ctx := context.Background()

	const racers = 4
	errCh := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- store.Migrate(ctx, modules.AllSet())
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent Migrate: %v", err)
		}
	}

	var distinct uint64
	if err := store.conn.QueryRow(ctx, "SELECT countDistinct(version) FROM otel.schema_migrations").Scan(&distinct); err != nil {
		t.Fatalf("counting distinct versions: %v", err)
	}
	if distinct != uint64(len(migrations.Ordered)) {
		t.Errorf("ledger has %d distinct versions, want %d", distinct, len(migrations.Ordered))
	}

	st, err := store.SchemaStatus(ctx, modules.AllSet())
	if err != nil || !st.Ready {
		t.Errorf("SchemaStatus after concurrent migrate: ready=%v err=%v", st.Ready, err)
	}
}

// TestMigrateHonorsConfiguredDatabase: the .sql files must follow the
// configured database. They used to hardcode `otel.`, so any other name
// produced a schema in `otel` and an empty database for the hub to query —
// an install that answers "table does not exist" to everything.
func TestMigrateHonorsConfiguredDatabase(t *testing.T) {
	seed, addr := startClickHouseContainerAddr(t)
	ctx := context.Background()

	// The connection handshake names a database, so it must exist before we can
	// dial it — the same reason an external cluster pre-provisions one.
	if err := seed.conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS telemetry"); err != nil {
		t.Fatalf("creating database: %v", err)
	}
	store, err := New(ctx, Config{Addr: addr, Database: "telemetry", Username: "avuru", Password: "avuru"})
	if err != nil {
		t.Fatalf("connecting to telemetry: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Migrate(ctx, modules.AllSet()); err != nil {
		t.Fatalf("Migrate against a non-default database: %v", err)
	}

	st, err := store.SchemaStatus(ctx, modules.AllSet())
	if err != nil || !st.Ready {
		t.Fatalf("SchemaStatus: ready=%v err=%v missing=%v", st.Ready, err, st.Missing)
	}
	for _, tbl := range []string{"otel_traces", "auth_user", "alert_channel"} {
		var n uint64
		if err := store.conn.QueryRow(ctx,
			"SELECT count() FROM system.tables WHERE database='telemetry' AND name=?", tbl).Scan(&n); err != nil {
			t.Fatalf("checking table %s: %v", tbl, err)
		}
		if n != 1 {
			t.Errorf("table %s was not created in the configured database", tbl)
		}
	}

	// Retention targets the same database — the ALTER used to fail here because
	// it looked in the configured database for tables the DDL had put in otel.
	if err := store.ApplyRetention(ctx, Retention{TracesDays: 7}); err != nil {
		t.Errorf("ApplyRetention against a non-default database: %v", err)
	}
}

// insertSpansTenant seeds spans into an explicit tenant via the avuru.tenant
// resource attribute — the Tenant column's DEFAULT expression, i.e. the same
// path production data takes through the gateway. Each service also carries a
// per-tenant k8s.namespace.name so ServiceLabels has something to resolve.
func insertSpansTenant(t *testing.T, s *Store, tenant string, spans []testSpan) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, SpanAttributes, Duration, StatusCode)`)
	if err != nil {
		t.Fatalf("preparing tenant batch: %v", err)
	}
	res := map[string]string{"avuru.tenant": tenant, "k8s.namespace.name": tenant + "-ns"}
	for _, sp := range spans {
		err := batch.Append(
			sp.ts, sp.traceID, sp.spanID, sp.parentID, sp.name, sp.kind, sp.service,
			res, map[string]string{}, uint64(sp.duration.Nanoseconds()), sp.status,
		)
		if err != nil {
			t.Fatalf("appending tenant span: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending tenant batch: %v", err)
	}
}

// insertLogsTenant is insertLogs with an explicit tenant (same avuru.tenant
// resource-attribute path as insertSpansTenant).
func insertLogsTenant(t *testing.T, s *Store, tenant string, logs []testLog) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_logs
		(Timestamp, TraceId, SpanId, SeverityText, SeverityNumber, ServiceName, Body, ResourceAttributes)`)
	if err != nil {
		t.Fatalf("preparing tenant logs batch: %v", err)
	}
	res := map[string]string{"avuru.tenant": tenant}
	for _, l := range logs {
		if err := batch.Append(l.ts, l.traceID, l.spanID, l.severity, l.sevNum, l.service, l.body, res); err != nil {
			t.Fatalf("appending tenant log: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending tenant logs batch: %v", err)
	}
}

// TestMultiTenantTraceReads proves the Tenant IN (?) fan-out for the trace-
// signal reads: a single-tenant set returns exactly what the pre-conversion
// single-tenant query returned, and a two-tenant set returns the union /
// merged aggregate. It is also the binding proof that clickhouse-go renders a
// []string bound to IN (?) as an array literal ClickHouse accepts.
func TestMultiTenantTraceReads(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	insertSpansTenant(t, store, "default", []testSpan{
		// trace A: frontend root (error) -> client -> backend server.
		{base.Add(1 * time.Minute), "aaaa0001", "s1", "", "GET /x", "Server", "frontend", 500 * time.Millisecond, "Error"},
		{base.Add(1*time.Minute + time.Millisecond), "aaaa0001", "s2", "s1", "call backend", "Client", "frontend", 100 * time.Millisecond, "Unset"},
		{base.Add(1*time.Minute + 2*time.Millisecond), "aaaa0001", "s3", "s2", "POST /b", "Server", "backend", 80 * time.Millisecond, "Unset"},
		// trace B: frontend root ok.
		{base.Add(2 * time.Minute), "bbbb0002", "s4", "", "GET /x", "Server", "frontend", 50 * time.Millisecond, "Unset"},
	})
	insertSpansTenant(t, store, "staging", []testSpan{
		// trace C: checkout root -> client -> payments server.
		{base.Add(3 * time.Minute), "cccc0003", "t1", "", "GET /y", "Server", "checkout", 200 * time.Millisecond, "Unset"},
		{base.Add(3*time.Minute + time.Millisecond), "cccc0003", "t2", "t1", "call payments", "Client", "checkout", 150 * time.Millisecond, "Unset"},
		{base.Add(3*time.Minute + 2*time.Millisecond), "cccc0003", "t3", "t2", "POST /p", "Server", "payments", 90 * time.Millisecond, "Unset"},
	})
	// The same trace id carries logs in both tenants (a trace crossing two
	// member projects): the union is the merged-project read.
	insertLogsTenant(t, store, "default", []testLog{
		{base.Add(1 * time.Minute), "aaaa0001", "s1", "ERROR", 17, "frontend", "boom"},
	})
	insertLogsTenant(t, store, "staging", []testLog{
		{base.Add(1*time.Minute + time.Second), "aaaa0001", "t9", "INFO", 9, "payments", "downstream view"},
	})

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}
	both := []string{"default", "staging"}

	t.Run("ListServices", func(t *testing.T) {
		one, err := store.ListServices(ctx, storage.ServiceQuery{Tenant: "default", Tenants: []string{"default"}, Range: tr})
		if err != nil {
			t.Fatalf("ListServices one: %v", err)
		}
		if len(one) != 2 || one[0].Name != "frontend" || one[0].SpanCount != 2 || one[0].ErrorCount != 1 {
			t.Fatalf("single-tenant services wrong: %+v", one)
		}
		merged, err := store.ListServices(ctx, storage.ServiceQuery{Tenant: "default", Tenants: both, Range: tr})
		if err != nil {
			t.Fatalf("ListServices merged: %v", err)
		}
		if len(merged) != 4 {
			t.Fatalf("merged services = %+v, want 4 (frontend backend checkout payments)", merged)
		}
	})

	t.Run("SearchTraces", func(t *testing.T) {
		one, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("SearchTraces one: %v", err)
		}
		if len(one.Traces) != 2 || one.Traces[0].TraceID != "bbbb0002" || one.Traces[1].SpanCount != 3 {
			t.Fatalf("single-tenant traces wrong: %+v", one.Traces)
		}
		merged, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Tenants: both, Range: tr})
		if err != nil {
			t.Fatalf("SearchTraces merged: %v", err)
		}
		if len(merged.Traces) != 3 || merged.Traces[0].TraceID != "cccc0003" {
			t.Fatalf("merged traces wrong (want newest-first across tenants): %+v", merged.Traces)
		}
	})

	t.Run("TraceOverview", func(t *testing.T) {
		one, err := store.TraceOverview(ctx, storage.OverviewQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("TraceOverview one: %v", err)
		}
		if len(one) != 2 || one[0].Service != "backend" || one[1].Count != 2 || one[1].ErrorCount != 1 {
			t.Fatalf("single-tenant overview wrong: %+v", one)
		}
		merged, err := store.TraceOverview(ctx, storage.OverviewQuery{Tenant: "default", Tenants: both, Range: tr})
		if err != nil {
			t.Fatalf("TraceOverview merged: %v", err)
		}
		if len(merged) != 4 {
			t.Fatalf("merged overview = %+v, want 4 rows", merged)
		}
	})

	t.Run("ServiceEdges", func(t *testing.T) {
		one, err := store.ServiceEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("ServiceEdges one: %v", err)
		}
		if len(one) != 1 || one[0].Source != "frontend" || one[0].Target != "backend" {
			t.Fatalf("single-tenant edges wrong: %+v", one)
		}
		merged, err := store.ServiceEdges(ctx, storage.ServiceQuery{Tenant: "default", Tenants: both, Range: tr})
		if err != nil {
			t.Fatalf("ServiceEdges merged: %v", err)
		}
		if len(merged) != 2 {
			t.Fatalf("merged edges = %+v, want frontend->backend and checkout->payments", merged)
		}
	})

	t.Run("TraceHeatmap", func(t *testing.T) {
		sum := func(hm storage.Heatmap) (total, errs uint64) {
			for _, c := range hm.Cells {
				total += c.Count
				errs += c.ErrorCount
			}
			return
		}
		one, err := store.TraceHeatmap(ctx, storage.HeatmapQuery{Tenant: "default", Range: tr, TimeBuckets: 10, DurationBuckets: 12})
		if err != nil {
			t.Fatalf("TraceHeatmap one: %v", err)
		}
		if total, errs := sum(one); total != 2 || errs != 1 {
			t.Fatalf("single-tenant heatmap totals: total=%d errs=%d, want 2/1", total, errs)
		}
		merged, err := store.TraceHeatmap(ctx, storage.HeatmapQuery{Tenant: "default", Tenants: both, Range: tr, TimeBuckets: 10, DurationBuckets: 12})
		if err != nil {
			t.Fatalf("TraceHeatmap merged: %v", err)
		}
		if total, errs := sum(merged); total != 3 || errs != 1 {
			t.Fatalf("merged heatmap totals: total=%d errs=%d, want 3/1", total, errs)
		}
	})

	t.Run("REDSeries", func(t *testing.T) {
		one, err := store.REDSeries(ctx, storage.REDQuery{Tenant: "default", Range: tr, Points: 6, TopN: 6})
		if err != nil {
			t.Fatalf("REDSeries one: %v", err)
		}
		if len(one) != 2 {
			t.Fatalf("single-tenant series = %+v, want 2", one)
		}
		merged, err := store.REDSeries(ctx, storage.REDQuery{Tenant: "default", Tenants: both, Range: tr, Points: 6, TopN: 6})
		if err != nil {
			t.Fatalf("REDSeries merged: %v", err)
		}
		if len(merged) != 4 {
			t.Fatalf("merged series = %+v, want 4", merged)
		}
	})

	t.Run("ServiceLabels", func(t *testing.T) {
		one, err := store.ServiceLabels(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("ServiceLabels one: %v", err)
		}
		if len(one) != 2 {
			t.Fatalf("single-tenant labels = %+v, want 2", one)
		}
		merged, err := store.ServiceLabels(ctx, storage.ServiceQuery{Tenant: "default", Tenants: both, Range: tr})
		if err != nil {
			t.Fatalf("ServiceLabels merged: %v", err)
		}
		byService := map[string]storage.ServiceLabel{}
		for _, l := range merged {
			byService[l.Service] = l
		}
		if len(byService) != 4 {
			t.Fatalf("merged labels = %+v, want 4 services", merged)
		}
		if byService["frontend"].K8sNamespace != "default-ns" || byService["checkout"].K8sNamespace != "staging-ns" {
			t.Errorf("merged label namespaces wrong: %+v", byService)
		}
	})

	t.Run("GetTrace", func(t *testing.T) {
		if _, err := store.GetTrace(ctx, []string{"default"}, "cccc0003"); err != storage.ErrNotFound {
			t.Fatalf("staging trace visible to [default]: %v", err)
		}
		got, err := store.GetTrace(ctx, both, "cccc0003")
		if err != nil {
			t.Fatalf("GetTrace merged: %v", err)
		}
		if len(got.Spans) != 3 {
			t.Fatalf("merged GetTrace spans = %d, want 3", len(got.Spans))
		}
		if _, err := store.GetTrace(ctx, nil, "cccc0003"); err == nil {
			t.Fatal("empty tenant set must error")
		}
	})

	t.Run("FindSpanTrace", func(t *testing.T) {
		if _, err := store.FindSpanTrace(ctx, []string{"default"}, "t1"); err != storage.ErrNotFound {
			t.Fatalf("staging span visible to [default]: %v", err)
		}
		traceID, err := store.FindSpanTrace(ctx, both, "t1")
		if err != nil || traceID != "cccc0003" {
			t.Fatalf("merged FindSpanTrace = %q, %v", traceID, err)
		}
	})

	t.Run("LogsForTrace", func(t *testing.T) {
		one, err := store.LogsForTrace(ctx, []string{"default"}, "aaaa0001")
		if err != nil {
			t.Fatalf("LogsForTrace one: %v", err)
		}
		if len(one) != 1 || one[0].Service != "frontend" {
			t.Fatalf("single-tenant logs wrong: %+v", one)
		}
		merged, err := store.LogsForTrace(ctx, both, "aaaa0001")
		if err != nil {
			t.Fatalf("LogsForTrace merged: %v", err)
		}
		if len(merged) != 2 || merged[1].Service != "payments" { // oldest first
			t.Fatalf("merged logs wrong: %+v", merged)
		}
	})
}

// TestMultiTenantLogSearch proves the Tenant IN (?) fan-out for SearchLogs:
// a single-tenant set returns exactly what the pre-conversion single-tenant
// query returned; a two-tenant set returns the union newest-first, with
// filters and keyset pagination applied across the merged stream.
func TestMultiTenantLogSearch(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	insertLogsTenant(t, store, "default", []testLog{
		{base.Add(1 * time.Minute), "trace-d1", "d1", "ERROR", 17, "checkout", "payment declined"},
		{base.Add(3 * time.Minute), "trace-d2", "d2", "INFO", 9, "frontend", "request handled ok"},
	})
	insertLogsTenant(t, store, "staging", []testLog{
		{base.Add(2 * time.Minute), "trace-s1", "s1", "WARN", 13, "checkout", "retrying payment"},
		{base.Add(4 * time.Minute), "trace-s2", "s2", "ERROR", 17, "payments", "gateway timeout"},
	})

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(9 * time.Minute)}
	both := []string{"default", "staging"}

	t.Run("SingleTenantParity", func(t *testing.T) {
		one, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Tenants: []string{"default"}, Range: tr})
		if err != nil {
			t.Fatalf("SearchLogs one: %v", err)
		}
		if len(one.Logs) != 2 || one.Logs[0].Service != "frontend" || one.Logs[1].Service != "checkout" {
			t.Fatalf("single-tenant logs wrong: %+v", one.Logs)
		}
	})

	t.Run("UnionNewestFirst", func(t *testing.T) {
		merged, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Tenants: both, Range: tr})
		if err != nil {
			t.Fatalf("SearchLogs merged: %v", err)
		}
		if len(merged.Logs) != 4 {
			t.Fatalf("merged logs = %d, want 4 (%+v)", len(merged.Logs), merged.Logs)
		}
		want := []string{"payments", "frontend", "checkout", "checkout"} // base+4,3,2,1
		for i, svc := range want {
			if merged.Logs[i].Service != svc {
				t.Fatalf("merged order wrong at %d: got %q want %q (%+v)", i, merged.Logs[i].Service, svc, merged.Logs)
			}
		}
	})

	t.Run("FiltersOverUnion", func(t *testing.T) {
		errs, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Tenants: both, Range: tr, MinSeverity: "ERROR"})
		if err != nil {
			t.Fatalf("SearchLogs severity: %v", err)
		}
		if len(errs.Logs) != 2 { // one ERROR per tenant
			t.Fatalf("merged severity filter wrong: %+v", errs.Logs)
		}
		svc, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Tenants: both, Range: tr, Service: "checkout"})
		if err != nil {
			t.Fatalf("SearchLogs service: %v", err)
		}
		if len(svc.Logs) != 2 { // checkout logs from both tenants
			t.Fatalf("merged service filter wrong: %+v", svc.Logs)
		}
	})

	t.Run("PaginationOverUnion", func(t *testing.T) {
		seen := map[string]int{}
		var cursor *storage.LogCursor
		pages := 0
		for range 5 {
			page, err := store.SearchLogs(ctx, storage.LogQuery{Tenant: "default", Tenants: both, Range: tr, Limit: 3, Cursor: cursor})
			if err != nil {
				t.Fatalf("page %d: %v", pages, err)
			}
			pages++
			for _, l := range page.Logs {
				seen[l.SpanID]++
			}
			if page.NextCursor == nil {
				break
			}
			cursor = page.NextCursor
		}
		if pages != 2 || len(seen) != 4 {
			t.Fatalf("pagination pages=%d seen=%v, want 2 pages / 4 distinct logs", pages, seen)
		}
		for id, n := range seen {
			if n != 1 {
				t.Errorf("log %s seen %d times across pages", id, n)
			}
		}
	})
}

// insertNodeSignals seeds one row in each of the four tables ListAgentNodes
// probes (gauge / logs / traces / profiles), all carrying the same tenant and
// k8s.node.name, so every probe has something to find for that tenant.
func insertNodeSignals(t *testing.T, s *Store, tenant, node string, ts time.Time) {
	t.Helper()
	ctx := context.Background()
	res := map[string]string{"avuru.tenant": tenant, "k8s.node.name": node}

	insertGauge(t, s, ts, metricNodeCPU, res, 1.0)

	logBatch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_logs
		(Timestamp, TraceId, SpanId, SeverityText, SeverityNumber, ServiceName, Body, ResourceAttributes)`)
	if err != nil {
		t.Fatalf("preparing node log insert: %v", err)
	}
	if err := logBatch.Append(ts, "", "", "INFO", int32(9), "agent", "hello", res); err != nil {
		t.Fatalf("appending node log: %v", err)
	}
	if err := logBatch.Send(); err != nil {
		t.Fatalf("sending node log: %v", err)
	}

	spanBatch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, SpanAttributes, Duration, StatusCode)`)
	if err != nil {
		t.Fatalf("preparing node span insert: %v", err)
	}
	if err := spanBatch.Append(ts, "node"+tenant, "sp1", "", "GET /x", "Server", "agent",
		res, map[string]string{}, uint64(time.Millisecond), "Unset"); err != nil {
		t.Fatalf("appending node span: %v", err)
	}
	if err := spanBatch.Send(); err != nil {
		t.Fatalf("sending node span: %v", err)
	}

	if err := s.WriteProfileSamples(ctx, []storage.ProfileSample{{
		Tenant: tenant, Timestamp: ts, Service: "agent", SampleType: "samples:count",
		Frames: []string{"main"}, Value: 1, Node: node,
	}}); err != nil {
		t.Fatalf("writing node profile sample: %v", err)
	}
}

// TestMultiTenantInfraReads proves the Tenant IN (?) fan-out for the
// metrics-backed reads converted in this slice: node/pod stats (InfraQuery),
// the agent inventory probes (AgentQuery), and network edge health
// (ServiceQuery). A single-tenant set returns exactly the pre-conversion
// result; a two-tenant set returns the union, and merges rows that share a
// grouping key — the merged-project semantics ServiceEdges already established.
func TestMultiTenantInfraReads(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	both := []string{"default", "staging"}

	nodeA := map[string]string{"avuru.tenant": "default", "k8s.node.name": "node-a"}
	nodeB := map[string]string{"avuru.tenant": "staging", "k8s.node.name": "node-b"}
	podA := map[string]string{
		"avuru.tenant": "default", "k8s.node.name": "node-a", "k8s.pod.name": "web-1",
		"k8s.namespace.name": "shop", "k8s.deployment.name": "web",
	}
	podB := map[string]string{
		"avuru.tenant": "staging", "k8s.node.name": "node-b", "k8s.pod.name": "api-1",
		"k8s.namespace.name": "shop", "k8s.deployment.name": "api",
	}

	for _, n := range []struct {
		res       map[string]string
		cpu, mem  float64
		available float64
		rxFrom    float64
		rxTo      float64
	}{
		{nodeA, 2.0, 2048, 4096, 1000, 7000},
		{nodeB, 3.0, 1024, 8192, 500, 2500},
	} {
		insertGauge(t, store, base.Add(1*time.Minute), metricNodeCPU, n.res, n.cpu/2)
		insertGauge(t, store, base.Add(5*time.Minute), metricNodeCPU, n.res, n.cpu)
		insertGauge(t, store, base.Add(5*time.Minute), metricNodeMem, n.res, n.mem)
		insertGauge(t, store, base.Add(5*time.Minute), metricNodeMemAvail, n.res, n.available)
		rx := map[string]string{"direction": "receive", "interface": "eth0"}
		insertSum(t, store, base.Add(1*time.Minute), metricNodeNet, n.res, rx, n.rxFrom)
		insertSum(t, store, base.Add(5*time.Minute), metricNodeNet, n.res, rx, n.rxTo)
	}
	insertGauge(t, store, base.Add(5*time.Minute), metricPodCPU, podA, 0.25)
	insertGauge(t, store, base.Add(5*time.Minute), metricPodMem, podA, 512)
	insertGauge(t, store, base.Add(5*time.Minute), metricPodCPU, podB, 0.75)
	insertGauge(t, store, base.Add(5*time.Minute), metricPodMem, podB, 256)

	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}

	t.Run("NodeStats", func(t *testing.T) {
		one, err := store.ListNodeStats(ctx, storage.InfraQuery{
			Tenant: "default", Tenants: []string{"default"}, Range: tr, Points: 6,
		})
		if err != nil {
			t.Fatalf("ListNodeStats one: %v", err)
		}
		if len(one) != 1 || one[0].Name != "node-a" || one[0].CPUUsage != 2.0 || one[0].PodCount != 1 {
			t.Fatalf("single-tenant node stats wrong: %+v", one)
		}
		if len(one[0].CPUSeries) < 2 {
			t.Errorf("cpu series too short: %+v", one[0].CPUSeries)
		}
		wantRate := 6000.0 / tr.End.Sub(tr.Start).Seconds()
		if one[0].NetworkRxRate < wantRate*0.99 || one[0].NetworkRxRate > wantRate*1.01 {
			t.Errorf("rx rate = %v, want ~%v", one[0].NetworkRxRate, wantRate)
		}

		merged, err := store.ListNodeStats(ctx, storage.InfraQuery{
			Tenant: "default", Tenants: both, Range: tr, Points: 6,
		})
		if err != nil {
			t.Fatalf("ListNodeStats merged: %v", err)
		}
		if len(merged) != 2 || merged[0].Name != "node-a" || merged[1].Name != "node-b" {
			t.Fatalf("merged node stats wrong: %+v", merged)
		}
		if merged[1].CPUUsage != 3.0 || merged[1].MemoryUsage != 1024 || merged[1].PodCount != 1 {
			t.Errorf("merged node-b wrong: %+v", merged[1])
		}
	})

	t.Run("PodStats", func(t *testing.T) {
		one, err := store.ListPodStats(ctx, storage.InfraQuery{
			Tenant: "default", Tenants: []string{"default"}, Range: tr,
		})
		if err != nil {
			t.Fatalf("ListPodStats one: %v", err)
		}
		if len(one) != 1 || one[0].Name != "web-1" || one[0].Workload != "web" {
			t.Fatalf("single-tenant pod stats wrong: %+v", one)
		}

		merged, err := store.ListPodStats(ctx, storage.InfraQuery{
			Tenant: "default", Tenants: both, Range: tr,
		})
		if err != nil {
			t.Fatalf("ListPodStats merged: %v", err)
		}
		if len(merged) != 2 || merged[0].Name != "api-1" { // busiest first
			t.Fatalf("merged pod stats wrong: %+v", merged)
		}

		// The node filter still applies over the merged set.
		onNodeB, err := store.ListPodStats(ctx, storage.InfraQuery{
			Tenant: "default", Tenants: both, Range: tr, Node: "node-b",
		})
		if err != nil {
			t.Fatalf("ListPodStats node-b: %v", err)
		}
		if len(onNodeB) != 1 || onNodeB[0].Name != "api-1" {
			t.Fatalf("node filter over union wrong: %+v", onNodeB)
		}
	})

	t.Run("AgentInventory", func(t *testing.T) {
		ts := time.Now().UTC().Add(-2 * time.Minute)
		insertNodeSignals(t, store, "default", "agent-a", ts)
		insertNodeSignals(t, store, "staging", "agent-b", ts)

		names := func(nodes []storage.AgentNode) []string {
			out := make([]string, 0, len(nodes))
			for _, n := range nodes {
				out = append(out, n.Node)
			}
			return out
		}

		one, err := store.ListAgentNodes(ctx, storage.AgentQuery{
			Tenant: "default", Tenants: []string{"default"}, Window: time.Hour,
		})
		if err != nil {
			t.Fatalf("ListAgentNodes one: %v", err)
		}
		if !slices.Contains(names(one), "agent-a") || slices.Contains(names(one), "agent-b") {
			t.Fatalf("single-tenant agent inventory wrong: %v", names(one))
		}
		var a storage.AgentNode
		for _, n := range one {
			if n.Node == "agent-a" {
				a = n
			}
		}
		if a.Metrics == nil || a.Logs == nil || a.Traces == nil || a.Profiles == nil {
			t.Errorf("agent-a probes incomplete: %+v", a)
		}

		merged, err := store.ListAgentNodes(ctx, storage.AgentQuery{
			Tenant: "default", Tenants: both, Window: time.Hour,
		})
		if err != nil {
			t.Fatalf("ListAgentNodes merged: %v", err)
		}
		if !slices.Contains(names(merged), "agent-a") || !slices.Contains(names(merged), "agent-b") {
			t.Fatalf("merged agent inventory wrong: %v", names(merged))
		}
	})

	t.Run("NetworkEdgeHealth", func(t *testing.T) {
		bounds := []float64{0.01, 0.05, 0.1, 0.5} // 5 buckets, last is +Inf
		edge := map[string]string{"k8s.src.owner.name": "cart", "k8s.dst.owner.name": "payments"}
		other := map[string]string{"k8s.src.owner.name": "cart", "k8s.dst.owner.name": "shipping"}
		def := map[string]string{"avuru.tenant": "default"}
		stg := map[string]string{"avuru.tenant": "staging"}

		// default: p95 lands in bucket 2 (0.05s). staging: everything in the
		// +Inf bucket, so p95 caps at the last finite bound (0.5s). Merged, the
		// staging tail dominates — a different answer from either tenant alone.
		insertHistogram(t, store, base.Add(1*time.Minute), networkRTTMetric, def, edge, []uint64{90, 10, 0, 0, 0}, bounds)
		insertHistogram(t, store, base.Add(1*time.Minute), networkRTTMetric, stg, edge, []uint64{0, 0, 0, 0, 100}, bounds)
		insertSum(t, store, base.Add(1*time.Minute), networkFailedMetric, def, edge, 3)
		insertSum(t, store, base.Add(1*time.Minute), networkFailedMetric, stg, edge, 4)
		// An edge only the staging tenant sees.
		insertSum(t, store, base.Add(1*time.Minute), networkFailedMetric, stg, other, 5)

		one, err := store.NetworkEdgeHealth(ctx, storage.ServiceQuery{
			Tenant: "default", Tenants: []string{"default"}, Range: tr,
		})
		if err != nil {
			t.Fatalf("NetworkEdgeHealth one: %v", err)
		}
		if len(one) != 1 || one[0].Target != "payments" || one[0].RTTMs != 50 || one[0].FailedConnections != 3 {
			t.Fatalf("single-tenant edge health wrong: %+v", one)
		}

		merged, err := store.NetworkEdgeHealth(ctx, storage.ServiceQuery{
			Tenant: "default", Tenants: both, Range: tr,
		})
		if err != nil {
			t.Fatalf("NetworkEdgeHealth merged: %v", err)
		}
		if len(merged) != 2 {
			t.Fatalf("merged edges = %d, want 2 (%+v)", len(merged), merged)
		}
		byTarget := map[string]storage.NetworkEdgeHealth{}
		for _, e := range merged {
			byTarget[e.Target] = e
		}
		if p := byTarget["payments"]; p.RTTMs != 500 || p.FailedConnections != 7 {
			t.Errorf("merged cart->payments wrong: %+v", p)
		}
		if s := byTarget["shipping"]; s.FailedConnections != 5 {
			t.Errorf("merged cart->shipping wrong: %+v", s)
		}
	})
}

// TestMultiTenantProfileReads proves the Tenant IN (?) fan-out for the
// profiling reads. The stacks dictionary stays joined per tenant, but the
// flame graph groups by StackHash, so an identical stack sampled in two
// member tenants merges into one node with the summed value.
func TestMultiTenantProfileReads(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	both := []string{"m1", "m2"}

	handler := []string{"handler", "main"}
	if err := store.WriteProfileSamples(ctx, []storage.ProfileSample{
		{Tenant: "m1", Timestamp: now, Service: "checkout", SampleType: "samples:count", Frames: handler, Value: 5},
		{Tenant: "m2", Timestamp: now, Service: "checkout", SampleType: "samples:count", Frames: handler, Value: 3},
		{Tenant: "m2", Timestamp: now, Service: "checkout", SampleType: "samples:count", Frames: []string{"encode", "handler", "main"}, Value: 2},
		{Tenant: "m2", Timestamp: now, Service: "api", SampleType: "samples:count", Frames: []string{"serve", "main"}, Value: 7},
	}); err != nil {
		t.Fatalf("WriteProfileSamples: %v", err)
	}

	tr := storage.TimeRange{Start: now.Add(-time.Minute), End: now.Add(time.Minute)}

	t.Run("ProfiledServices", func(t *testing.T) {
		one, err := store.ListProfiledServices(ctx, storage.ProfileQuery{
			Tenant: "m1", Tenants: []string{"m1"}, Range: tr,
		})
		if err != nil {
			t.Fatalf("ListProfiledServices one: %v", err)
		}
		if len(one) != 1 || one[0].Name != "checkout" || one[0].Samples != 5 {
			t.Fatalf("single-tenant profiled services wrong: %+v", one)
		}

		merged, err := store.ListProfiledServices(ctx, storage.ProfileQuery{
			Tenant: "m1", Tenants: both, Range: tr,
		})
		if err != nil {
			t.Fatalf("ListProfiledServices merged: %v", err)
		}
		if len(merged) != 2 || merged[0].Name != "checkout" || merged[0].Samples != 10 {
			t.Fatalf("merged profiled services wrong (busiest first): %+v", merged)
		}
		if merged[1].Name != "api" || merged[1].Samples != 7 {
			t.Errorf("merged second service wrong: %+v", merged[1])
		}
	})

	t.Run("Flamegraph", func(t *testing.T) {
		child := func(n storage.FlameNode, name string) *storage.FlameNode {
			for _, c := range n.Children {
				if c.Name == name {
					return c
				}
			}
			t.Fatalf("no child %q under %q: %+v", name, n.Name, n.Children)
			return nil
		}

		one, err := store.ProfileFlamegraph(ctx, storage.ProfileQuery{
			Tenant: "m1", Tenants: []string{"m1"}, Range: tr, Service: "checkout",
		})
		if err != nil {
			t.Fatalf("ProfileFlamegraph one: %v", err)
		}
		if one.Value != 5 || len(one.Children) != 1 {
			t.Fatalf("single-tenant flamegraph wrong: %+v", one)
		}

		merged, err := store.ProfileFlamegraph(ctx, storage.ProfileQuery{
			Tenant: "m1", Tenants: both, Range: tr, Service: "checkout",
		})
		if err != nil {
			t.Fatalf("ProfileFlamegraph merged: %v", err)
		}
		if merged.Value != 10 {
			t.Fatalf("merged flamegraph total = %d, want 10 (%+v)", merged.Value, merged)
		}
		main := child(merged, "main")
		h := child(*main, "handler")
		if main.Value != 10 || h.Value != 10 {
			t.Errorf("shared stack did not merge across tenants: main=%d handler=%d", main.Value, h.Value)
		}
		if e := child(*h, "encode"); e.Value != 2 {
			t.Errorf("m2-only leaf wrong: %+v", e)
		}
		if h.Self != 8 {
			t.Errorf("merged handler self = %d, want 8 (5+3)", h.Self)
		}
	})
}
