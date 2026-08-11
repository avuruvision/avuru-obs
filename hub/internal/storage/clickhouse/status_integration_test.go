//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestEffectiveStatusIntegration exercises errorSpanExpr end-to-end: spans
// whose OTel status is Unset but whose http status is 5xx (any kind) or 4xx
// (Client) must count as errors everywhere — trace filter, per-trace error
// counts, service stats, overview, heatmap, RED and service-map edges —
// while Ok-status, 3xx and server-side 4xx spans must not. One fixture per
// row of the rule table in status.go.
func TestEffectiveStatusIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	fixtures := []struct {
		traceID, spanID, parentID, name, kind, service, status string
		attrs                                                  map[string]string
		dur                                                     time.Duration // 0 means "default" (1ms)
	}{
		// Rule 1: explicit Error always wins.
		{"aaaa1111", "s1", "", "GET /x", "Server", "web", "Error", nil, 0},
		// Rule 2: explicit Ok is final, even alongside a 500 attribute.
		{"bbbb2222", "s2", "", "GET /x", "Server", "web", "Ok",
			map[string]string{"http.response.status_code": "500"}, 0},
		// Rule 3: Unset + 5xx (current semconv key) is an error.
		{"cccc3333", "s3", "", "GET /x", "Server", "web", "Unset",
			map[string]string{"http.response.status_code": "500"}, 0},
		// Rule 4: Unset + 4xx on a CLIENT child is an error (pre-1.21 key);
		// the root itself stays ok, so this trace has ErrorCount=1 but does
		// not match the root-span error filter.
		{"dddd4444", "s4", "", "GET /x", "Server", "web", "Unset", nil, 0},
		{"dddd4444", "c4", "s4", "call dep", "Client", "web", "Unset",
			map[string]string{"http.status_code": "404"}, 0},
		// Rule 5: Unset + 4xx on a SERVER span is NOT an error.
		{"eeee5555", "s5", "", "GET /x", "Server", "web", "Unset",
			map[string]string{"http.status_code": "404"}, 0},
		// Rule 6: 3xx is never an error.
		{"ffff6666", "s6", "", "GET /x", "Server", "web", "Unset",
			map[string]string{"http.response.status_code": "307"}, 0},
		// Cross-service pair for the "server."-prefixed ServiceEdges variant:
		// a client 4xx root (error, rule 4) calling a server that 500s
		// (error, rule 3). The client span is seeded at a distinct duration
		// (5ms vs. the default 1ms) so the ServiceEdges latency assertion
		// below can tell client.Duration and server.Duration apart.
		{"abcd7777", "cs", "", "CALL api", "Client", "web", "Unset",
			map[string]string{"http.status_code": "404"}, 5 * time.Millisecond},
		{"abcd7777", "ss", "cs", "GET /y", "Server", "api", "Unset",
			map[string]string{"http.response.status_code": "500"}, 0},
	}

	batch, err := store.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind, ServiceName, SpanAttributes, Duration, StatusCode)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	for i, sp := range fixtures {
		attrs := sp.attrs
		if attrs == nil {
			attrs = map[string]string{}
		}
		d := sp.dur
		if d == 0 {
			d = time.Millisecond
		}
		if err := batch.Append(base.Add(time.Minute+time.Duration(i)*time.Second),
			sp.traceID, sp.spanID, sp.parentID, sp.name, sp.kind, sp.service,
			attrs, uint64(d), sp.status); err != nil {
			t.Fatalf("appending span %s: %v", sp.spanID, err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending batch: %v", err)
	}

	tr := storage.TimeRange{Start: base, End: base.Add(6 * time.Minute)}

	t.Run("SearchTracesErrorFilter", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Status: "error"})
		if err != nil {
			t.Fatalf("SearchTraces error: %v", err)
		}
		want := map[string]bool{"aaaa1111": true, "cccc3333": true, "abcd7777": true}
		if len(page.Traces) != len(want) {
			t.Fatalf("error filter got %d traces, want %d: %+v", len(page.Traces), len(want), page.Traces)
		}
		for _, got := range page.Traces {
			if !want[got.TraceID] {
				t.Errorf("error filter returned unexpected trace %s", got.TraceID)
			}
		}
	})

	t.Run("SearchTracesOkFilter", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr, Status: "ok"})
		if err != nil {
			t.Fatalf("SearchTraces ok: %v", err)
		}
		want := map[string]bool{"bbbb2222": true, "dddd4444": true, "eeee5555": true, "ffff6666": true}
		if len(page.Traces) != len(want) {
			t.Fatalf("ok filter got %d traces, want %d: %+v", len(page.Traces), len(want), page.Traces)
		}
		for _, got := range page.Traces {
			if !want[got.TraceID] {
				t.Errorf("ok filter returned unexpected trace %s", got.TraceID)
			}
		}
	})

	t.Run("TraceErrorCounts", func(t *testing.T) {
		page, err := store.SearchTraces(ctx, storage.TraceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("SearchTraces: %v", err)
		}
		want := map[string]uint64{
			"aaaa1111": 1, "bbbb2222": 0, "cccc3333": 1,
			"dddd4444": 1, "eeee5555": 0, "ffff6666": 0, "abcd7777": 2,
		}
		if len(page.Traces) != len(want) {
			t.Fatalf("got %d traces, want %d", len(page.Traces), len(want))
		}
		for _, got := range page.Traces {
			if got.ErrorCount != want[got.TraceID] {
				t.Errorf("trace %s ErrorCount = %d, want %d", got.TraceID, got.ErrorCount, want[got.TraceID])
			}
		}
	})

	t.Run("ListServices", func(t *testing.T) {
		got, err := store.ListServices(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("ListServices: %v", err)
		}
		// Entry spans only: web has s1..s6 (errors: s1 explicit, s3 500);
		// api has ss (error via 500).
		if len(got) != 2 || got[0].Name != "web" {
			t.Fatalf("services wrong: %+v", got)
		}
		if got[0].SpanCount != 6 || got[0].ErrorCount != 2 {
			t.Errorf("web stats = %d spans / %d errors, want 6/2", got[0].SpanCount, got[0].ErrorCount)
		}
		if got[1].SpanCount != 1 || got[1].ErrorCount != 1 {
			t.Errorf("api stats = %d spans / %d errors, want 1/1", got[1].SpanCount, got[1].ErrorCount)
		}
	})

	t.Run("TraceOverview", func(t *testing.T) {
		got, err := store.TraceOverview(ctx, storage.OverviewQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("TraceOverview: %v", err)
		}
		byOp := map[string]storage.OperationStats{}
		for _, op := range got {
			byOp[op.Service+" "+op.Operation] = op
		}
		if op := byOp["web GET /x"]; op.Count != 6 || op.ErrorCount != 2 {
			t.Errorf("web GET /x = %d/%d, want 6/2", op.Count, op.ErrorCount)
		}
		if op := byOp["web CALL api"]; op.Count != 1 || op.ErrorCount != 1 {
			t.Errorf("web CALL api = %d/%d, want 1/1", op.Count, op.ErrorCount)
		}
	})

	t.Run("TraceHeatmap", func(t *testing.T) {
		hm, err := store.TraceHeatmap(ctx, storage.HeatmapQuery{Tenant: "default", Range: tr, TimeBuckets: 10, DurationBuckets: 12})
		if err != nil {
			t.Fatalf("TraceHeatmap: %v", err)
		}
		var total, errs uint64
		for _, c := range hm.Cells {
			total += c.Count
			errs += c.ErrorCount
		}
		// 7 roots; errors: s1 (explicit), s3 (500), cs (client 404).
		if total != 7 || errs != 3 {
			t.Errorf("heatmap totals = %d/%d, want 7/3", total, errs)
		}
	})

	t.Run("REDSeries", func(t *testing.T) {
		series, err := store.REDSeries(ctx, storage.REDQuery{Tenant: "default", Range: tr, Service: "web", Points: 6})
		if err != nil {
			t.Fatalf("REDSeries: %v", err)
		}
		if len(series) != 1 {
			t.Fatalf("got %d series, want 1: %+v", len(series), series)
		}
		var reqs, errs uint64
		for _, p := range series[0].Points {
			reqs += p.Count
			errs += p.ErrorCount
		}
		if reqs != 6 || errs != 2 {
			t.Errorf("web RED = %d reqs / %d errors, want 6/2", reqs, errs)
		}
	})

	t.Run("ServiceEdges", func(t *testing.T) {
		edges, err := store.ServiceEdges(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
		if err != nil {
			t.Fatalf("ServiceEdges: %v", err)
		}
		if len(edges) != 1 {
			t.Fatalf("got %d edges, want 1: %+v", len(edges), edges)
		}
		e := edges[0]
		if e.Source != "web" || e.Target != "api" || e.Count != 1 || e.ErrorCount != 1 {
			t.Errorf("edge wrong: %+v", e)
		}
		// Client-side latency for the call path. The client span ("CALL api")
		// is seeded at 5ms while the server span it calls ("GET /y") is 1ms, so
		// this assertion FAILS if the query is ever pointed at server.Duration
		// — which is the whole design decision this edge encodes.
		if e.P50 != 5*time.Millisecond || e.P95 != 5*time.Millisecond {
			t.Errorf("edge latency = p50 %v / p95 %v, want 5ms/5ms", e.P50, e.P95)
		}
	})
}
