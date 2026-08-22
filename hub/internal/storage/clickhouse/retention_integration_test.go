//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// storageProject is the minimal live project row: an id and a window.
func storageProject(id string, retentionDays int) storage.Project {
	return storage.Project{ID: id, Label: id, Members: []string{}, RetentionDays: retentionDays}
}

// syncMutations makes ALTER … DELETE synchronous for the test. In production
// the trimmer fires mutations and moves on (a part rewrite is not something a
// background tick waits for), so without this the assertions would race the
// server.
func syncMutations(t *testing.T) context.Context {
	t.Helper()
	return clickhouse.Context(context.Background(),
		clickhouse.WithSettings(map[string]any{"mutations_sync": 1}))
}

// insertTenantSpan writes one span for a tenant at a given time. Tenant is set
// explicitly rather than through the ResourceAttributes DEFAULT so the test
// states what it means.
func insertTenantSpan(t *testing.T, s *Store, tenant, traceID string, ts time.Time) {
	t.Helper()
	err := s.conn.Exec(context.Background(),
		`INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanKind, ServiceName, Duration, StatusCode, Tenant)
		 VALUES (?, ?, ?, 'GET /', 'Server', 'checkout', 1000000, 'Unset', ?)`,
		ts, traceID, traceID[:8], tenant)
	if err != nil {
		t.Fatalf("insert span: %v", err)
	}
}

func countSpans(t *testing.T, s *Store, tenant string) int {
	t.Helper()
	var n uint64
	if err := s.conn.QueryRow(context.Background(),
		"SELECT count() FROM otel_traces WHERE Tenant = ?", tenant).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return int(n)
}

// TestTrimTenant is the per-project retention mechanism end to end: only the
// named tenant loses rows, only rows older than the cutoff, and a second sweep
// with nothing left to delete issues no mutation at all.
func TestTrimTenant(t *testing.T) {
	store := startClickHouse(t)
	ctx := syncMutations(t)

	now := time.Now().UTC()
	cutoff := now.Add(-48 * time.Hour)
	insertTenantSpan(t, store, "staging", "aaaaaaaaaaaaaaaa", now.Add(-96*time.Hour)) // older than cutoff
	insertTenantSpan(t, store, "staging", "bbbbbbbbbbbbbbbb", now.Add(-1*time.Hour))  // inside the window
	insertTenantSpan(t, store, "prod", "cccccccccccccccc", now.Add(-96*time.Hour))    // another tenant, same age

	trimmed, err := store.TrimTenant(ctx, "staging", cutoff)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if len(trimmed) == 0 {
		t.Fatalf("trimmed = %v, want otel_traces among them", trimmed)
	}

	if got := countSpans(t, store, "staging"); got != 1 {
		t.Fatalf("staging spans = %d, want 1 (only the row inside the window)", got)
	}
	// The whole point of scoping by Tenant: one project's retention must not
	// touch another's data.
	if got := countSpans(t, store, "prod"); got != 1 {
		t.Fatalf("prod spans = %d, want 1 — another tenant's rows are not this project's retention", got)
	}

	// Steady state: yesterday's trim already ran, so the sweep must cost a
	// lookup and issue nothing rather than re-mutating every table every tick.
	trimmed, err = store.TrimTenant(ctx, "staging", cutoff)
	if err != nil {
		t.Fatalf("second trim: %v", err)
	}
	if len(trimmed) != 0 {
		t.Fatalf("second trim = %v, want no mutation issued", trimmed)
	}
}

// A project's retention window survives the ReplacingMergeTree round trip —
// the column is read back on both paths the trimmer and the UI use.
func TestProjectRetentionRoundtrip(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	if err := store.SaveProject(ctx, storageProject("staging", 3)); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.GetProject(ctx, "staging")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RetentionDays != 3 {
		t.Fatalf("RetentionDays = %d, want 3", got.RetentionDays)
	}

	list, err := store.ListProjects(ctx)
	if err != nil || len(list) != 1 || list[0].RetentionDays != 3 {
		t.Fatalf("list = %+v err=%v, want one project keeping 3 days", list, err)
	}

	// Clearing the window back to "inherit the global retention" must stick:
	// FINAL keeps the newest row, so 0 has to be written, not omitted.
	p := got
	p.RetentionDays = 0
	if err := store.SaveProject(ctx, p); err != nil {
		t.Fatalf("resave: %v", err)
	}
	if back, _ := store.GetProject(ctx, "staging"); back.RetentionDays != 0 {
		t.Fatalf("RetentionDays = %d, want 0 after clearing", back.RetentionDays)
	}
}
