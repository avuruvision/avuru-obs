//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"
)

// TestTenantUsage is the per-project half of the Status page against a real
// server: one project's rows counted with the tenant filter, its time bounds,
// its recent rate — and, crucially, NOT its neighbour's rows.
func TestTenantUsage(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// staging: two rows, one of them recent. prod: one row, old.
	insertTenantSpan(t, store, "staging", "1111111111111111", now.Add(-30*time.Minute))
	insertTenantSpan(t, store, "staging", "2222222222222222", now.Add(-72*time.Hour))
	insertTenantSpan(t, store, "prod", "3333333333333333", now.Add(-72*time.Hour))

	usage, err := store.TenantUsage(ctx, []string{"staging"}, now)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	var got bool
	for _, sig := range usage.Signals {
		if sig.Signal != "traces" {
			continue
		}
		got = true
		if sig.Rows != 2 {
			t.Fatalf("rows = %d, want 2 — prod's row must not be counted", sig.Rows)
		}
		if sig.Oldest == nil || sig.Newest == nil {
			t.Fatalf("no time bounds: %+v", sig)
		}
		if sig.Newest.Before(*sig.Oldest) {
			t.Fatalf("bounds inverted: %v..%v", sig.Oldest, sig.Newest)
		}
		// One row inside the last hour, over a one-hour window.
		if want := 1.0 / 60.0; sig.RowsPerMinute < want*0.9 || sig.RowsPerMinute > want*1.1 {
			t.Fatalf("rate = %f, want ~%f (one row in the last hour)", sig.RowsPerMinute, want)
		}
	}
	if !got {
		t.Fatalf("no traces signal in %+v", usage.Signals)
	}
}

// An aggregate reads the union of its members, so TenantUsage must too — the
// Status page and the screens have to agree about what the project contains.
func TestTenantUsageUnionsMembers(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC()

	insertTenantSpan(t, store, "prod-eu", "4444444444444444", now.Add(-2*time.Hour))
	insertTenantSpan(t, store, "prod-us", "5555555555555555", now.Add(-2*time.Hour))
	insertTenantSpan(t, store, "elsewhere", "6666666666666666", now.Add(-2*time.Hour))

	usage, err := store.TenantUsage(ctx, []string{"prod-eu", "prod-us"}, now)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	for _, sig := range usage.Signals {
		if sig.Signal == "traces" && sig.Rows != 2 {
			t.Fatalf("rows = %d, want 2 (both members, and only them)", sig.Rows)
		}
	}
}

// A project with no data at all must report no time bounds — min()/max() over
// an empty set return the epoch, which would render as "oldest data: 1970".
func TestTenantUsageEmptyProjectHasNoBounds(t *testing.T) {
	store := startClickHouse(t)

	usage, err := store.TenantUsage(context.Background(), []string{"never-used"}, time.Now().UTC())
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	for _, sig := range usage.Signals {
		if sig.Rows != 0 || sig.Oldest != nil || sig.Newest != nil || sig.EstimatedBytes != 0 {
			t.Fatalf("%s = %+v, want an empty footprint", sig.Signal, sig)
		}
	}
}
