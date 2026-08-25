//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestCheckResultsRoundTrip(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second).Add(-10 * time.Minute)

	rows := []storage.CheckResult{
		{Tenant: "default", CheckID: "core-login", Group: "core", At: base, OK: true, Status: 200, LatencyMs: 9.5},
		{Tenant: "default", CheckID: "core-login", Group: "core", At: base.Add(time.Minute), OK: false,
			Status: 503, LatencyMs: 12, Error: "expected a 2xx, got 503", TraceID: "abc123", SpanID: "def456"},
		{Tenant: "default", CheckID: "other", Group: "core", At: base, OK: true, Status: 200},
	}
	for _, r := range rows {
		if err := store.RecordCheckResult(ctx, r); err != nil {
			t.Fatalf("RecordCheckResult: %v", err)
		}
	}

	got, err := store.CheckResults(ctx, storage.CheckQuery{Tenant: "default", CheckID: "core-login"})
	if err != nil {
		t.Fatalf("CheckResults: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want the 2 belonging to this check: %+v", len(got), got)
	}
	// Newest first: a results panel opens on what just happened.
	if got[0].OK || got[0].Status != 503 {
		t.Errorf("newest result wrong: %+v", got[0])
	}
	if got[0].Error == "" || got[0].TraceID != "abc123" {
		t.Errorf("failure detail lost on the round trip: %+v", got[0])
	}
	if got[0].LatencyMs != 12 {
		t.Errorf("latency = %v, want 12", got[0].LatencyMs)
	}
}

// The consecutive-failure rule reads a bounded recent slice per check, in one
// query rather than one per check.
func TestLatestCheckStatesCapsPerCheck(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second).Add(-10 * time.Minute)

	for i := 0; i < 6; i++ {
		for _, id := range []string{"a", "b"} {
			if err := store.RecordCheckResult(ctx, storage.CheckResult{
				Tenant: "default", CheckID: id, Group: "core",
				At: base.Add(time.Duration(i) * time.Minute), OK: i%2 == 0, Status: 200,
			}); err != nil {
				t.Fatalf("RecordCheckResult: %v", err)
			}
		}
	}

	tr := storage.TimeRange{Start: base.Add(-time.Minute), End: base.Add(10 * time.Minute)}
	states, err := store.LatestCheckStates(ctx, storage.ServiceQuery{Tenant: "default", Range: tr}, 3)
	if err != nil {
		t.Fatalf("LatestCheckStates: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("got %d checks, want 2: %+v", len(states), states)
	}
	for id, rs := range states {
		if len(rs) != 3 {
			t.Errorf("check %q returned %d results, want the 3-cap", id, len(rs))
		}
		// Newest first, because the rule counts the leading run of failures.
		for i := 1; i < len(rs); i++ {
			if rs[i].At.After(rs[i-1].At) {
				t.Errorf("check %q results are not newest-first", id)
				break
			}
		}
	}
}

// Another project's probe results must not answer for this one.
func TestCheckResultsTenantIsolation(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	for _, tenant := range []string{"default", "other"} {
		if err := store.RecordCheckResult(ctx, storage.CheckResult{
			Tenant: tenant, CheckID: "core-login", Group: "core", At: now, OK: tenant == "default", Status: 200,
		}); err != nil {
			t.Fatalf("RecordCheckResult: %v", err)
		}
	}
	got, err := store.CheckResults(ctx, storage.CheckQuery{Tenant: "other", CheckID: "core-login"})
	if err != nil {
		t.Fatalf("CheckResults: %v", err)
	}
	if len(got) != 1 || got[0].OK {
		t.Errorf("tenant isolation broken: %+v", got)
	}
}
