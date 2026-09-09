//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestServicePresenceFindsServicesListServicesCannot pins both halves of the
// defect this fixes: what ListServices structurally cannot see, and that the
// probe sees it.
//
// It is an integration test rather than a unit test because everything else in
// this change runs against the fake, and the fake cannot catch a wrong column
// name, a bind in the wrong statement position, or a tenant filter that renders
// badly — which is most of what could go wrong in new SQL.
func TestServicePresenceFindsServicesListServicesCannot(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	win := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(10 * time.Minute)}

	// Three shapes of workload, only the first of which has an ENTRY span.
	insertSpans(t, store, []testSpan{
		{base, "p1", "s0000000000000201", "", "GET /traced", "Server", "traced-svc", time.Millisecond, "Ok"},
		{base, "p2", "s0000000000000202", "", "SELECT users", "Client", "client-only-svc", time.Millisecond, "Ok"},
	})
	insertLogs(t, store, []testLog{
		{base, "", "", "ERROR", 17, "log-only-svc", "redis timeout"},
		{base.Add(time.Minute), "", "", "INFO", 9, "log-only-svc", "started"},
		{base.Add(2 * time.Minute), "", "", "INFO", 9, "log-only-svc", "healthy"},
	})

	// The premise, pinned: this is what made the tool call a live service dead.
	listed, err := store.ListServices(ctx, storage.ServiceQuery{Tenant: "default", Range: win})
	if err != nil {
		t.Fatalf("listing services: %v", err)
	}
	names := map[string]bool{}
	for _, svc := range listed {
		names[svc.Name] = true
	}
	if !names["traced-svc"] {
		t.Error("ListServices missed a service with entry spans")
	}
	if names["log-only-svc"] || names["client-only-svc"] {
		t.Errorf("ListServices unexpectedly knows a span-less service: %v", names)
	}

	present, err := store.ServicePresence(ctx, storage.ServiceQuery{Tenant: "default", Range: win},
		[]storage.Signal{storage.SignalSpans, storage.SignalLogs})
	if err != nil {
		t.Fatalf("probing presence: %v", err)
	}
	got := map[string]storage.ServicePresence{}
	for _, p := range present {
		got[p.Name] = p
	}
	for _, want := range []string{"traced-svc", "client-only-svc", "log-only-svc"} {
		if _, ok := got[want]; !ok {
			t.Errorf("presence is missing %s: %+v", want, present)
		}
	}
	if p := got["log-only-svc"]; p.LogRecords != 3 || p.ErrorLogRecords != 1 || p.Spans != 0 {
		t.Errorf("log-only-svc = %+v, want 3 records, 1 at ERROR, 0 spans", p)
	}
	// Client spans count here even though ListServices ignores them: a job that
	// only calls out is still a name someone can ask about.
	if p := got["client-only-svc"]; p.Spans == 0 {
		t.Errorf("client-only-svc = %+v, want its client span counted", p)
	}
	if got["log-only-svc"].LastSeen.IsZero() {
		t.Error("LastSeen is zero — it is what tells a reader the service is still alive")
	}

	// No signals means no query at all, not an empty scan.
	none, err := store.ServicePresence(ctx, storage.ServiceQuery{Tenant: "default", Range: win}, nil)
	if err != nil || none != nil {
		t.Errorf("empty signal set: got %v, %v; want nil, nil", none, err)
	}
}

// Aux exclusion has to mean the same thing here as in ListServices, or the two
// populations disagree about whether a health-check-only workload "reported".
func TestServicePresenceHonoursAuxExclusionForSpans(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-20 * time.Minute)
	win := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(10 * time.Minute)}

	insertSpans(t, store, []testSpan{
		{base, "h1", "s0000000000000301", "", "GET /healthz", "Server", "probe-only-svc", time.Millisecond, "Ok"},
	})

	present, err := store.ServicePresence(ctx,
		storage.ServiceQuery{Tenant: "default", Range: win, ExcludeAux: true},
		[]storage.Signal{storage.SignalSpans})
	if err != nil {
		t.Fatalf("probing presence: %v", err)
	}
	for _, p := range present {
		if p.Name == "probe-only-svc" {
			t.Errorf("a health-check-only workload survived aux exclusion: %+v", p)
		}
	}
}
