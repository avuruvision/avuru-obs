//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// labelSpan is a minimal entry span carrying explicit resource attributes.
// ServiceLabels is the only query driven entirely by ResourceAttributes, so it
// needs a fixture shape the shared testSpan does not provide. Kept local rather
// than widening testSpan, whose 30-odd positional literals would all have to
// change for a field only this file uses.
type labelSpan struct {
	service  string
	spanID   string
	resAttrs map[string]string
}

// insertLabelSpans writes entry spans (SpanKind=Server, so ServiceLabels' own
// filter accepts them) with the given resource attributes.
func insertLabelSpans(t *testing.T, s *Store, ts time.Time, spans []labelSpan) {
	t.Helper()
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes, Duration, StatusCode, StatusMessage,
		 Events.Timestamp, Events.Name, Events.Attributes,
		 Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	for _, sp := range spans {
		res := map[string]string{"service.name": sp.service}
		for k, v := range sp.resAttrs {
			res[k] = v
		}
		if err := batch.Append(
			ts, "trace-"+sp.spanID, sp.spanID, "", "", "GET /", "Server", sp.service,
			res, "test", "1", map[string]string{"k": "v"},
			uint64(time.Millisecond.Nanoseconds()), "Unset", "",
			[]time.Time{}, []string{}, []map[string]string{},
			[]string{}, []string{}, []string{}, []map[string]string{},
		); err != nil {
			t.Fatalf("appending span: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending batch: %v", err)
	}
}

// TestServiceLabelsDeclaredMetadata: ServiceLabels carries the declared
// environment and tier alongside the namespaces, each resolved to its dominant
// value by span count. The environment prefers the current semconv key and
// falls back to the deprecated one.
func TestServiceLabelsDeclaredMetadata(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)

	insertLabelSpans(t, s, now, []labelSpan{
		// Current semconv key wins when both are present.
		{service: "web", spanID: "s0000000000000001", resAttrs: map[string]string{
			"service.namespace":           "storefront",
			"deployment.environment.name": "prod",
			"deployment.environment":      "legacy-ignored",
			"avuru.tier":                  "T0",
		}},
		// Deprecated key is the fallback.
		{service: "cart", spanID: "s0000000000000002", resAttrs: map[string]string{
			"service.namespace":      "storefront",
			"deployment.environment": "staging",
			"avuru.tier":             "T2",
		}},
		// Dominant value: two staging spans beat one prod span.
		{service: "batch", spanID: "s0000000000000003", resAttrs: map[string]string{
			"deployment.environment.name": "staging",
		}},
		{service: "batch", spanID: "s0000000000000004", resAttrs: map[string]string{
			"deployment.environment.name": "staging",
		}},
		{service: "batch", spanID: "s0000000000000005", resAttrs: map[string]string{
			"deployment.environment.name": "prod",
		}},
	})

	got, err := s.ServiceLabels(context.Background(), storage.ServiceQuery{
		Tenant: "default",
		Range:  storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("ServiceLabels: %v", err)
	}

	byService := map[string]storage.ServiceLabel{}
	for _, l := range got {
		byService[l.Service] = l
	}

	want := map[string]storage.ServiceLabel{
		"web":   {Service: "web", ServiceNamespace: "storefront", Environment: "prod", DeclaredTier: "T0"},
		"cart":  {Service: "cart", ServiceNamespace: "storefront", Environment: "staging", DeclaredTier: "T2"},
		"batch": {Service: "batch", Environment: "staging"},
	}
	for svc, w := range want {
		if byService[svc] != w {
			t.Errorf("ServiceLabels[%q] = %+v, want %+v", svc, byService[svc], w)
		}
	}
}
