//go:build integration

package clickhouse

import (
	"context"
	"testing"
)

// The TTL reported for a signal has to come from ClickHouse, not from the
// hub's own configuration — the whole reason to show it is that the two can
// disagree.
//
// It also pins the shape ApplyRetention writes. Retention is an ALTER … MODIFY
// TTL run at migrate time, not part of the embedded schema, so a freshly
// migrated database has NO TTL until that step runs; and the reader parses the
// day count back out of the engine definition. Change either side — an hour
// interval, a moveTo rule, a different expression — and this fails, instead of
// the UI quietly showing a blank column.
func TestSystemStatsReadsTheEnforcedTTL(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	// Before retention is applied there is nothing to report, and reporting
	// nothing is the correct answer rather than echoing the configured value.
	before, err := store.SystemStats(ctx)
	if err != nil {
		t.Fatalf("SystemStats: %v", err)
	}
	if len(before.Signals) == 0 {
		t.Fatal("no signals reported from a migrated database")
	}
	for _, s := range before.Signals {
		if s.TTLDays != 0 {
			t.Errorf("signal %q reports TTL %d before ApplyRetention ran", s.Signal, s.TTLDays)
		}
	}

	want := Retention{TracesDays: 7, LogsDays: 3, MetricsDays: 14, ProfilesDays: 2, ErrorsDays: 5}
	if err := store.ApplyRetention(ctx, want); err != nil {
		t.Fatalf("ApplyRetention: %v", err)
	}

	after, err := store.SystemStats(ctx)
	if err != nil {
		t.Fatalf("SystemStats: %v", err)
	}
	expected := map[string]int{
		"traces": want.TracesDays, "logs": want.LogsDays, "metrics": want.MetricsDays,
		"profiles": want.ProfilesDays, "errors": want.ErrorsDays,
	}
	for _, s := range after.Signals {
		if exp, ok := expected[s.Signal]; ok && s.TTLDays != exp {
			t.Errorf("signal %q TTL = %d days, want %d", s.Signal, s.TTLDays, exp)
		}
		delete(expected, s.Signal)
	}
	for signal := range expected {
		t.Errorf("signal %q missing from SystemStats", signal)
	}
}
