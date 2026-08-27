//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// insertAttrSpans seeds spans with per-span attribute maps, which the shared
// insertSpans helper cannot express (it writes one fixed map for every row).
func insertAttrSpans(t *testing.T, s *Store, spans []attrSpan) {
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
		attrs := sp.attrs
		if attrs == nil {
			attrs = map[string]string{}
		}
		if err := batch.Append(
			sp.ts, sp.traceID, sp.spanID, sp.parentID, "", sp.name, sp.kind, sp.service,
			res, "test", "1", attrs,
			uint64(sp.duration.Nanoseconds()), sp.status, "",
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

type attrSpan struct {
	ts       time.Time
	traceID  string
	spanID   string
	parentID string
	name     string
	kind     string
	service  string
	duration time.Duration
	status   string
	attrs    map[string]string
	resAttrs map[string]string
}

func breakdownWindow() storage.TimeRange {
	now := time.Now().UTC()
	return storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}
}

func groupByKey(bd storage.Breakdown) map[string]storage.BreakdownGroup {
	out := make(map[string]storage.BreakdownGroup, len(bd.Groups))
	for _, g := range bd.Groups {
		out[g.Key] = g
	}
	return out
}

// The whole point of WITH TOTALS: a limited breakdown still has to know the
// size of the tail it is not returning. Without that, a treemap of the top N
// silently redraws those N as the entire estate.
func TestBreakdownTotalsCoverTheTailBeyondTheLimit(t *testing.T) {
	store := startClickHouse(t)
	now := time.Now().UTC()

	// Five services with distinct volumes, asked for the top two.
	var spans []attrSpan
	volumes := map[string]int{"a": 16, "b": 8, "c": 4, "d": 2, "e": 1}
	for svc, n := range volumes {
		for i := 0; i < n; i++ {
			spans = append(spans, attrSpan{
				ts: now, traceID: svc, spanID: svc + string(rune('0'+i)),
				name: "GET /items", kind: "Server", service: svc,
				duration: 10 * time.Millisecond,
			})
		}
	}
	insertAttrSpans(t, store, spans)

	bd, err := store.TraceBreakdown(context.Background(), storage.BreakdownQuery{
		Tenant: "default", Range: breakdownWindow(),
		GroupBy: storage.BreakdownService, Scope: storage.ScopeEntry, Limit: 2,
	})
	if err != nil {
		t.Fatalf("TraceBreakdown: %v", err)
	}
	if len(bd.Groups) != 2 {
		t.Fatalf("got %d groups, want 2 (the limit)", len(bd.Groups))
	}
	if bd.Groups[0].Key != "a" || bd.Groups[1].Key != "b" {
		t.Errorf("groups not ordered by volume: %+v", bd.Groups)
	}
	if bd.Total.Count != 31 {
		t.Errorf("total = %d, want 31 — totals must cover every group, not just the returned ones", bd.Total.Count)
	}
	if bd.GroupCount != 5 {
		t.Errorf("groupCount = %d, want 5", bd.GroupCount)
	}
	// The tail the caller derives from these two numbers.
	if tail := bd.Total.Count - (bd.Groups[0].Count + bd.Groups[1].Count); tail != 7 {
		t.Errorf("derived tail = %d, want 7", tail)
	}
	if bd.Total.DurationSum != 31*10*time.Millisecond {
		t.Errorf("total duration = %v, want %v", bd.Total.DurationSum, 31*10*time.Millisecond)
	}
}

// The three scopes answer three different questions and must not agree.
func TestBreakdownScopesCountDifferentSpans(t *testing.T) {
	store := startClickHouse(t)
	now := time.Now().UTC()

	// One trace: gateway root (Server, parentless) → cart Server → cart Client.
	insertAttrSpans(t, store, []attrSpan{
		{ts: now, traceID: "t1", spanID: "s1", name: "GET /checkout", kind: "Server", service: "gateway", duration: 30 * time.Millisecond},
		{ts: now, traceID: "t1", spanID: "s2", parentID: "s1", name: "GET /cart", kind: "Server", service: "cart", duration: 20 * time.Millisecond},
		{ts: now, traceID: "t1", spanID: "s3", parentID: "s2", name: "SELECT items", kind: "Client", service: "cart", duration: 5 * time.Millisecond},
	})
	ctx := context.Background()
	win := breakdownWindow()

	entry, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownService, Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("entry scope: %v", err)
	}
	if got := groupByKey(entry); got["gateway"].Count != 1 || got["cart"].Count != 1 || entry.Total.Count != 2 {
		t.Errorf("entry scope must count the two Server spans, got %+v", entry.Groups)
	}

	root, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownService, Scope: storage.ScopeRoot})
	if err != nil {
		t.Fatalf("root scope: %v", err)
	}
	if len(root.Groups) != 1 || root.Groups[0].Key != "gateway" || root.Total.Count != 1 {
		t.Errorf("root scope must see only where traffic entered, got %+v", root.Groups)
	}

	all, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownService, Scope: storage.ScopeAll})
	if err != nil {
		t.Fatalf("all scope: %v", err)
	}
	if got := groupByKey(all); got["cart"].Count != 2 || all.Total.Count != 3 {
		t.Errorf("all scope must count every span, got %+v", all.Groups)
	}
}

// Grouping by status has to reproduce the product's three-state answer, not the
// raw OTel StatusCode most instrumentation leaves unset.
func TestBreakdownByStatusSeparatesRefusedFromError(t *testing.T) {
	store := startClickHouse(t)
	now := time.Now().UTC()

	insertAttrSpans(t, store, []attrSpan{
		{ts: now, traceID: "ok1", spanID: "a", name: "GET /x", kind: "Server", service: "api", duration: time.Millisecond,
			attrs: map[string]string{"http.response.status_code": "200"}},
		{ts: now, traceID: "ref", spanID: "b", name: "GET /x", kind: "Server", service: "api", duration: time.Millisecond,
			attrs: map[string]string{"http.response.status_code": "403"}},
		{ts: now, traceID: "err", spanID: "c", name: "GET /x", kind: "Server", service: "api", duration: time.Millisecond,
			attrs: map[string]string{"http.response.status_code": "500"}},
	})

	bd, err := store.TraceBreakdown(context.Background(), storage.BreakdownQuery{
		Tenant: "default", Range: breakdownWindow(), GroupBy: storage.BreakdownStatus, Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("TraceBreakdown: %v", err)
	}
	got := groupByKey(bd)
	for _, want := range []string{"ok", "refused", "error"} {
		if got[want].Count != 1 {
			t.Errorf("status group %q = %d, want 1 (groups %+v)", want, got[want].Count, bd.Groups)
		}
	}
	// The same rows counted through the RED columns: a 4xx is neither.
	if bd.Total.ErrorCount != 1 || bd.Total.RefusedCount != 1 {
		t.Errorf("totals: errors=%d refused=%d, want 1 and 1", bd.Total.ErrorCount, bd.Total.RefusedCount)
	}
}

// The parameterised dimensions carry their key as DATA. This pins both halves:
// the key reaches the map lookup, and a key crafted as SQL does nothing.
func TestBreakdownByAttributeAndResourceBindsTheKey(t *testing.T) {
	store := startClickHouse(t)
	now := time.Now().UTC()

	insertAttrSpans(t, store, []attrSpan{
		{ts: now, traceID: "t1", spanID: "a", name: "GET /items/:id", kind: "Server", service: "cart", duration: time.Millisecond,
			attrs: map[string]string{"http.route": "/items/:id"}, resAttrs: map[string]string{"k8s.namespace.name": "shop"}},
		{ts: now, traceID: "t2", spanID: "b", name: "GET /items/:id", kind: "Server", service: "cart", duration: time.Millisecond,
			attrs: map[string]string{"http.route": "/items/:id"}, resAttrs: map[string]string{"k8s.namespace.name": "shop"}},
		{ts: now, traceID: "t3", spanID: "c", name: "POST /orders", kind: "Server", service: "checkout", duration: time.Millisecond,
			attrs: map[string]string{"http.route": "/orders"}, resAttrs: map[string]string{"k8s.namespace.name": "payments"}},
		// No route attribute at all — its own group, never dropped.
		{ts: now, traceID: "t4", spanID: "d", name: "consume", kind: "Consumer", service: "worker", duration: time.Millisecond},
	})
	ctx := context.Background()
	win := breakdownWindow()

	byRoute, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownAttribute, Key: "http.route", Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("attribute breakdown: %v", err)
	}
	routes := groupByKey(byRoute)
	if routes["/items/:id"].Count != 2 || routes["/orders"].Count != 1 {
		t.Errorf("route groups wrong: %+v", byRoute.Groups)
	}
	if routes[""].Count != 1 {
		t.Errorf("the span carrying no route must be its own group: %+v", byRoute.Groups)
	}

	byNs, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownResource, Key: "k8s.namespace.name", Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("resource breakdown: %v", err)
	}
	ns := groupByKey(byNs)
	if ns["shop"].Count != 2 || ns["payments"].Count != 1 {
		t.Errorf("namespace groups wrong: %+v", byNs.Groups)
	}

	// A key that is SQL is just a key: it matches no attribute, so every span
	// lands in the empty group and nothing is executed.
	hostile, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownAttribute,
		Key: "x'] , (SELECT 1) AS y, SpanAttributes['z", Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("hostile key must be inert, got error: %v", err)
	}
	if len(hostile.Groups) != 1 || hostile.Groups[0].Key != "" || hostile.Groups[0].Count != 4 {
		t.Errorf("hostile key changed the query shape: %+v", hostile.Groups)
	}
}

// A breakdown sits above the trace list and must describe the same population:
// the filters have to bite, and auxiliary traffic has to leave when asked.
func TestBreakdownAppliesFiltersAndAuxExclusion(t *testing.T) {
	store := startClickHouse(t)
	now := time.Now().UTC()

	insertAttrSpans(t, store, []attrSpan{
		{ts: now, traceID: "t1", spanID: "a", name: "GET /items", kind: "Server", service: "cart", duration: 50 * time.Millisecond},
		{ts: now, traceID: "t2", spanID: "b", name: "GET /items", kind: "Server", service: "cart", duration: 5 * time.Millisecond},
		{ts: now, traceID: "t3", spanID: "c", name: "GET /health", kind: "Server", service: "cart", duration: time.Millisecond},
		{ts: now, traceID: "t4", spanID: "d", name: "GET /items", kind: "Server", service: "checkout", duration: 50 * time.Millisecond},
	})
	ctx := context.Background()
	win := breakdownWindow()

	excluded, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownOperation,
		Scope: storage.ScopeEntry, ExcludeAux: true})
	if err != nil {
		t.Fatalf("aux-excluded breakdown: %v", err)
	}
	if _, ok := groupByKey(excluded)["GET /health"]; ok {
		t.Errorf("health traffic survived the aux exclusion: %+v", excluded.Groups)
	}
	if excluded.Total.Count != 3 {
		t.Errorf("total with aux excluded = %d, want 3", excluded.Total.Count)
	}

	included, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownOperation, Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("aux-included breakdown: %v", err)
	}
	if included.Total.Count != 4 {
		t.Errorf("total with aux included = %d, want 4", included.Total.Count)
	}

	filtered, err := store.TraceBreakdown(ctx, storage.BreakdownQuery{
		Tenant: "default", Range: win, GroupBy: storage.BreakdownService, Scope: storage.ScopeEntry,
		Service: "cart", MinDuration: 10 * time.Millisecond, ExcludeAux: true})
	if err != nil {
		t.Fatalf("filtered breakdown: %v", err)
	}
	if len(filtered.Groups) != 1 || filtered.Groups[0].Key != "cart" || filtered.Groups[0].Count != 1 {
		t.Errorf("service + duration filters did not bite: %+v", filtered.Groups)
	}
}

// An empty window must come back as an empty breakdown, not an error — the
// totals row is the part most likely to misbehave when nothing matched.
func TestBreakdownOnEmptyWindow(t *testing.T) {
	store := startClickHouse(t)
	bd, err := store.TraceBreakdown(context.Background(), storage.BreakdownQuery{
		Tenant: "default", Range: breakdownWindow(), GroupBy: storage.BreakdownService, Scope: storage.ScopeEntry})
	if err != nil {
		t.Fatalf("TraceBreakdown on empty data: %v", err)
	}
	if len(bd.Groups) != 0 || bd.Total.Count != 0 || bd.GroupCount != 0 {
		t.Errorf("empty window must yield an empty breakdown, got %+v", bd)
	}
}
