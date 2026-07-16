//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// insertSpanWithEvent inserts one span carrying a single named event with the
// given attributes — the shape span.recordException produces (event name
// "exception", exception.* attributes). tenantAttr, when set, is written to
// ResourceAttributes['avuru.tenant'] to exercise the MV's tenant derivation.
func insertSpanWithEvent(t *testing.T, s *Store, sp testSpan, eventName string, eventAttrs map[string]string, resAttrs map[string]string) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes, Duration, StatusCode, StatusMessage,
		 Events.Timestamp, Events.Name, Events.Attributes,
		 Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes)`)
	if err != nil {
		t.Fatalf("preparing span+event batch: %v", err)
	}
	res := map[string]string{"service.name": sp.service}
	for k, v := range resAttrs {
		res[k] = v
	}
	var (
		evtTimes []time.Time
		evtNames []string
		evtAttrs []map[string]string
	)
	if eventName != "" {
		evtTimes = []time.Time{sp.ts}
		evtNames = []string{eventName}
		evtAttrs = []map[string]string{eventAttrs}
	} else {
		evtTimes, evtNames, evtAttrs = []time.Time{}, []string{}, []map[string]string{}
	}
	if err := batch.Append(
		sp.ts, sp.traceID, sp.spanID, sp.parentID, "", sp.name, sp.kind, sp.service,
		res, "test", "1", map[string]string{},
		uint64(sp.duration.Nanoseconds()), sp.status, "",
		evtTimes, evtNames, evtAttrs,
		[]string{}, []string{}, []string{}, []map[string]string{},
	); err != nil {
		t.Fatalf("appending span+event: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending span+event batch: %v", err)
	}
}

// insertErrorSpan inserts a span with an HTTP status attribute and no
// exception event — the error-span derivation path (MV 2).
func insertErrorSpan(t *testing.T, s *Store, sp testSpan, httpStatus string) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes, Duration, StatusCode, StatusMessage,
		 Events.Timestamp, Events.Name, Events.Attributes,
		 Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes)`)
	if err != nil {
		t.Fatalf("preparing error-span batch: %v", err)
	}
	if err := batch.Append(
		sp.ts, sp.traceID, sp.spanID, sp.parentID, "", sp.name, sp.kind, sp.service,
		map[string]string{"service.name": sp.service}, "test", "1",
		map[string]string{"http.response.status_code": httpStatus},
		uint64(sp.duration.Nanoseconds()), sp.status, "",
		[]time.Time{}, []string{}, []map[string]string{},
		[]string{}, []string{}, []string{}, []map[string]string{},
	); err != nil {
		t.Fatalf("appending error-span: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending error-span batch: %v", err)
	}
}

// insertErrorLog inserts one ERROR-severity log. logAttrs feed the derivation
// (exception.*, sentry.*, avuru.error.source); resAttrs carry resource-scope
// values like avuru.tenant, which the gateway stamps into ResourceAttributes.
func insertErrorLog(t *testing.T, s *Store, ts time.Time, service, body string, logAttrs, resAttrs map[string]string) {
	t.Helper()
	ctx := context.Background()
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO otel_logs
		(Timestamp, TraceId, SpanId, SeverityText, SeverityNumber, ServiceName, Body, ResourceAttributes, LogAttributes)`)
	if err != nil {
		t.Fatalf("preparing error-log batch: %v", err)
	}
	res := map[string]string{"service.name": service}
	for k, v := range resAttrs {
		res[k] = v
	}
	if err := batch.Append(ts, "", "", "ERROR", uint8(17), service, body, res, logAttrs); err != nil {
		t.Fatalf("appending error-log: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending error-log batch: %v", err)
	}
}

type errorRow struct {
	tenant      string
	service     string
	fingerprint uint64
	source      string
	excType     string
	excMessage  string
	traceID     string
	environment string
	sdkName     string
}

func readErrorEvents(t *testing.T, s *Store, where string) []errorRow {
	t.Helper()
	ctx := context.Background()
	q := "SELECT Tenant, ServiceName, Fingerprint, toString(Source), ExceptionType, ExceptionMessage, TraceId, Environment, SdkName FROM otel.error_events"
	if where != "" {
		q += " WHERE " + where
	}
	q += " ORDER BY Timestamp"
	rows, err := s.conn.Query(ctx, q)
	if err != nil {
		t.Fatalf("querying error_events: %v", err)
	}
	defer rows.Close()
	var out []errorRow
	for rows.Next() {
		var r errorRow
		if err := rows.Scan(&r.tenant, &r.service, &r.fingerprint, &r.source, &r.excType, &r.excMessage, &r.traceID, &r.environment, &r.sdkName); err != nil {
			t.Fatalf("scanning error_events: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating error_events: %v", err)
	}
	return out
}

// TestErrorDerivationFromSpanEvents: span exception events become issues with
// the right type/message/trace, and the fingerprint is stable across changing
// line numbers (grouping), distinct across exception types.
func TestErrorDerivationFromSpanEvents(t *testing.T) {
	store := startClickHouse(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)

	npe := func(line string) map[string]string {
		return map[string]string{
			"exception.type":       "java.lang.NullPointerException",
			"exception.message":    "cannot invoke getName()",
			"exception.stacktrace": "at com.svc.Handler.handle(Handler.java:" + line + ")\nat com.svc.Router.route(Router.java:" + line + ")",
		}
	}
	insertSpanWithEvent(t, store, testSpan{base, "t1", "s0000000000000001", "", "GET /a", "Server", "web", time.Millisecond, "Error"}, "exception", npe("42"), nil)
	insertSpanWithEvent(t, store, testSpan{base.Add(time.Minute), "t2", "s0000000000000002", "", "GET /a", "Server", "web", time.Millisecond, "Error"}, "exception", npe("57"), nil)
	// A different exception type on the same service is a different issue.
	insertSpanWithEvent(t, store, testSpan{base.Add(2 * time.Minute), "t3", "s0000000000000003", "", "GET /b", "Server", "web", time.Millisecond, "Error"}, "exception",
		map[string]string{"exception.type": "java.lang.IllegalStateException", "exception.message": "closed", "exception.stacktrace": "at com.svc.Pool.take(Pool.java:9)"}, nil)

	rows := readErrorEvents(t, store, "Source = 'span'")
	if len(rows) != 3 {
		t.Fatalf("got %d span-sourced rows, want 3: %+v", len(rows), rows)
	}
	if rows[0].fingerprint != rows[1].fingerprint {
		t.Errorf("same exception with different line numbers should share a fingerprint: %d vs %d", rows[0].fingerprint, rows[1].fingerprint)
	}
	if rows[0].fingerprint == rows[2].fingerprint {
		t.Errorf("different exception types should not share a fingerprint")
	}
	if rows[0].excType != "java.lang.NullPointerException" || rows[0].traceID != "t1" {
		t.Errorf("row 0 wrong: %+v", rows[0])
	}
	if rows[0].source != "span" {
		t.Errorf("source = %q, want span", rows[0].source)
	}
}

// TestErrorDerivationFromErrorSpans: a 500 span with no exception event yields
// an "HTTP 500" issue, and it is NOT double-counted when an exception event is
// also present.
func TestErrorDerivationFromErrorSpans(t *testing.T) {
	store := startClickHouse(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-5 * time.Minute)

	insertErrorSpan(t, store, testSpan{base, "e1", "s0000000000000010", "", "POST /pay", "Server", "checkout", time.Millisecond, "Unset"}, "500")
	rows := readErrorEvents(t, store, "ServiceName = 'checkout'")
	if len(rows) != 1 {
		t.Fatalf("got %d rows for checkout, want 1: %+v", len(rows), rows)
	}
	if rows[0].excType != "HTTP 500" || rows[0].source != "span" {
		t.Errorf("error-span row wrong: %+v", rows[0])
	}

	// A span with BOTH a 500 and an exception event must come only from MV 1
	// (exception), not also MV 2 — the error-span MV excludes exception spans.
	insertSpanWithEvent(t, store, testSpan{base.Add(time.Minute), "e2", "s0000000000000011", "", "POST /pay", "Server", "billing", time.Millisecond, "Error"}, "exception",
		map[string]string{"exception.type": "TimeoutError", "exception.message": "upstream", "exception.stacktrace": "at pay(pay.go:1)"}, nil)
	billing := readErrorEvents(t, store, "ServiceName = 'billing'")
	if len(billing) != 1 {
		t.Fatalf("billing double-counted: got %d rows, want 1: %+v", len(billing), billing)
	}
	if billing[0].excType != "TimeoutError" {
		t.Errorf("billing issue should come from the exception event: %+v", billing[0])
	}
}

// TestErrorDerivationFromLogs: ERROR logs become issues; a Sentry-tagged log is
// sourced "sentry" with its SDK captured; tenant derives from the resource attr.
func TestErrorDerivationFromLogs(t *testing.T) {
	store := startClickHouse(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)

	insertErrorLog(t, store, base, "worker", "job failed", map[string]string{
		"exception.type":    "RuntimeError",
		"exception.message": "boom",
	}, nil)
	insertErrorLog(t, store, base.Add(time.Minute), "frontend", "TypeError: undefined is not a function", map[string]string{
		"exception.type":       "TypeError",
		"exception.message":    "undefined is not a function",
		"exception.stacktrace": "at App.render (app.js:12:3)",
		"avuru.error.source":   "sentry",
		"sentry.sdk.name":      "sentry.javascript.browser",
		"sentry.sdk.version":   "7.100.0",
	}, map[string]string{"avuru.tenant": "prod-eu"})

	logRow := readErrorEvents(t, store, "ServiceName = 'worker'")
	if len(logRow) != 1 || logRow[0].source != "log" || logRow[0].excType != "RuntimeError" {
		t.Fatalf("log-sourced row wrong: %+v", logRow)
	}

	sentry := readErrorEvents(t, store, "ServiceName = 'frontend'")
	if len(sentry) != 1 {
		t.Fatalf("got %d frontend rows, want 1: %+v", len(sentry), sentry)
	}
	if sentry[0].source != "sentry" {
		t.Errorf("source = %q, want sentry", sentry[0].source)
	}
	if sentry[0].sdkName != "sentry.javascript.browser" {
		t.Errorf("sdk = %q, want the browser SDK", sentry[0].sdkName)
	}
	if sentry[0].tenant != "prod-eu" {
		t.Errorf("tenant = %q, want prod-eu (derived from resource attr)", sentry[0].tenant)
	}
}

// TestErrorTrackingModuleGating: with error-tracking off, the schema is absent
// and SystemStats doesn't report an errors signal.
func TestErrorTrackingModuleGating(t *testing.T) {
	store := startClickHouseContainer(t)
	ctx := context.Background()

	subset, err := modules.Parse("core,logs")
	if err != nil {
		t.Fatalf("parsing modules: %v", err)
	}
	if err := store.Migrate(ctx, subset); err != nil {
		t.Fatalf("Migrate without error-tracking: %v", err)
	}
	var n uint64
	if err := store.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database='otel' AND name='error_events'").Scan(&n); err != nil {
		t.Fatalf("checking error_events: %v", err)
	}
	if n != 0 {
		t.Errorf("error_events exists although error-tracking is off")
	}
	stats, err := store.SystemStats(ctx)
	if err != nil {
		t.Fatalf("SystemStats: %v", err)
	}
	for _, sig := range stats.Signals {
		if sig.Signal == "errors" {
			t.Errorf("SystemStats reports errors although module is off")
		}
	}
}
