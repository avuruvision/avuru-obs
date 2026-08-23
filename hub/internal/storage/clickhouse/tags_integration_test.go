//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// insertTaggedSpan writes one span carrying resource attributes of its own,
// which the shared insertSpans helper does not allow — business tags live in
// exactly that map, so the tests below need to set it.
func insertTaggedSpan(t *testing.T, s *Store, tenant string, sp testSpan, res map[string]string) {
	t.Helper()
	batch, err := s.conn.PrepareBatch(context.Background(), `INSERT INTO otel_traces
		(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
		 ResourceAttributes, ScopeName, ScopeVersion, SpanAttributes, Duration, StatusCode, StatusMessage,
		 Tenant,
		 Events.Timestamp, Events.Name, Events.Attributes,
		 Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes)`)
	if err != nil {
		t.Fatalf("preparing batch: %v", err)
	}
	attrs := map[string]string{"service.name": sp.service}
	for k, v := range res {
		attrs[k] = v
	}
	if err := batch.Append(
		sp.ts, sp.traceID, sp.spanID, sp.parentID, "", sp.name, sp.kind, sp.service,
		attrs, "test", "1", map[string]string{"k": "v"},
		uint64(sp.duration.Nanoseconds()), sp.status, "",
		tenant,
		[]time.Time{}, []string{}, []map[string]string{},
		[]string{}, []string{}, []string{}, []map[string]string{},
	); err != nil {
		t.Fatalf("appending span: %v", err)
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("sending batch: %v", err)
	}
}

// TestTagKeysIntegration covers what only real ClickHouse can answer: that the
// map is unrolled into (key, value) pairs, that the prefix filter prunes in
// SQL, that the value sample scans into a []string, and that one project
// cannot discover another's tags.
func TestTagKeysIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	tr := storage.TimeRange{Start: base, End: base.Add(10 * time.Minute)}
	span := func(id, svc string) testSpan {
		return testSpan{ts: base.Add(time.Minute), traceID: id, spanID: id + "s", name: "GET /", kind: "Server", service: svc, duration: time.Millisecond, status: "Ok"}
	}

	insertTaggedSpan(t, store, "default", span("t1", "checkout"), map[string]string{
		"avuru.tag.team": "payments", "avuru.tag.tier": "critical",
		// Not a business tag: must not be discovered, or the filter control
		// fills with every semconv attribute a workload happens to carry.
		"k8s.namespace.name": "shop",
	})
	insertTaggedSpan(t, store, "default", span("t2", "cart"), map[string]string{
		"avuru.tag.team": "storefront",
	})
	// Same key, same value, a second time — the sample is DISTINCT values.
	insertTaggedSpan(t, store, "default", span("t3", "cart2"), map[string]string{
		"avuru.tag.team": "storefront",
	})
	insertTaggedSpan(t, store, "other", span("t4", "elsewhere"), map[string]string{
		"avuru.tag.team": "someone-elses",
	})

	tags, err := store.TagKeys(ctx, storage.ServiceQuery{Tenant: "default", Range: tr})
	if err != nil {
		t.Fatalf("TagKeys: %v", err)
	}
	got := map[string][]string{}
	for _, tk := range tags {
		got[tk.Key] = tk.Values
	}
	if len(got) != 2 {
		t.Fatalf("discovered %d keys %v, want exactly the two avuru.tag.* keys", len(got), got)
	}
	if _, ok := got["k8s.namespace.name"]; ok {
		t.Errorf("a non-business attribute leaked into discovery: %v", got)
	}
	teams := got["avuru.tag.team"]
	if len(teams) != 2 {
		t.Errorf("avuru.tag.team values = %v, want the two distinct teams", teams)
	}
	if !containsAll(teams, "payments", "storefront") {
		t.Errorf("avuru.tag.team values = %v, want payments and storefront", teams)
	}

	// Keys are ordered, so a type-ahead does not reshuffle between refreshes.
	if len(tags) == 2 && tags[0].Key > tags[1].Key {
		t.Errorf("keys not ordered: %v", tags)
	}

	// Tenant isolation: discovery is a read like any other.
	other, err := store.TagKeys(ctx, storage.ServiceQuery{Tenant: "other", Range: tr})
	if err != nil {
		t.Fatalf("TagKeys(other): %v", err)
	}
	if len(other) != 1 || other[0].Values[0] != "someone-elses" {
		t.Fatalf("tenant other discovered %v, want only its own tag", other)
	}
}

// TestSearchTracesByBusinessTagIntegration is the assertion the whole feature
// rests on: a tag lives on the RESOURCE, not the span, and a trace matches when
// any participating service carries it — not only when the root does.
func TestSearchTracesByBusinessTagIntegration(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	tr := storage.TimeRange{Start: base, End: base.Add(10 * time.Minute)}
	span := func(trace, id, parent, svc string) testSpan {
		return testSpan{ts: base.Add(time.Minute), traceID: trace, spanID: id, parentID: parent, name: "GET /" + svc, kind: "Server", service: svc, duration: time.Millisecond, status: "Ok"}
	}

	// Trace A: an untagged frontend calls a payments-owned service. The tag is
	// on the CHILD, which is the case a root-only rule would miss.
	insertTaggedSpan(t, store, "default", span("ta", "a1", "", "frontend"), nil)
	insertTaggedSpan(t, store, "default", span("ta", "a2", "a1", "billing"), map[string]string{"avuru.tag.team": "payments"})
	// Trace B: nothing payments-owned anywhere.
	insertTaggedSpan(t, store, "default", span("tb", "b1", "", "frontend"), nil)
	insertTaggedSpan(t, store, "default", span("tb", "b2", "b1", "search"), map[string]string{"avuru.tag.team": "storefront"})

	page, err := store.SearchTraces(ctx, storage.TraceQuery{
		Tenant: "default", Range: tr,
		Tags: map[string]string{"avuru.tag.team": "payments"},
	})
	if err != nil {
		t.Fatalf("SearchTraces: %v", err)
	}
	if len(page.Traces) != 1 || page.Traces[0].TraceID != "ta" {
		t.Fatalf("got %v, want only trace ta — a tag on a downstream service still selects the trace", page.Traces)
	}

	// And the negative: a value nothing carries returns nothing, rather than
	// silently ignoring the filter.
	page, err = store.SearchTraces(ctx, storage.TraceQuery{
		Tenant: "default", Range: tr,
		Tags: map[string]string{"avuru.tag.team": "nobody"},
	})
	if err != nil {
		t.Fatalf("SearchTraces(nobody): %v", err)
	}
	if len(page.Traces) != 0 {
		t.Fatalf("got %v, want nothing", page.Traces)
	}
}

func containsAll(have []string, want ...string) bool {
	set := map[string]bool{}
	for _, h := range have {
		set[h] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
