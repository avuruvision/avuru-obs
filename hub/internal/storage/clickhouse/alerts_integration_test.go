//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestMultiTenantAlertHistory proves the Tenant IN (?) fan-out for the alert
// history read — the last of the store's tenant filters to convert. The row's
// own Tenant now comes back with it, so an aggregate project can tell which
// member fired; alert STATE stays single-tenant on purpose (evaluation is
// leaf-only, so LoadAlertStates keeps its per-tenant loop).
func TestMultiTenantAlertHistory(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	base := time.Now().UTC().Truncate(time.Minute).Add(-30 * time.Minute)
	both := []string{"m1", "m2"}

	if err := store.AppendAlertHistory(ctx, []storage.AlertHistoryEntry{
		{Tenant: "m1", RuleName: "checkout-down", Target: "checkout", Kind: "fired", Status: "down", Reason: "no traffic", FiredAt: base.Add(1 * time.Minute)},
		{Tenant: "m2", RuleName: "api-degraded", Target: "api", Kind: "fired", Status: "degraded", Reason: "p95 above threshold", FiredAt: base.Add(2 * time.Minute)},
		{Tenant: "m1", RuleName: "checkout-down", Target: "checkout", Kind: "resolved", Status: "healthy", Reason: "recovered", FiredAt: base.Add(3 * time.Minute)},
		{Tenant: "other", RuleName: "noise", Target: "noise", Kind: "fired", Status: "down", Reason: "not a member", FiredAt: base.Add(4 * time.Minute)},
	}); err != nil {
		t.Fatalf("AppendAlertHistory: %v", err)
	}

	t.Run("SingleTenantParity", func(t *testing.T) {
		got, err := store.ListAlertHistory(ctx, storage.AlertHistoryQuery{
			Tenant: "m1", Tenants: []string{"m1"},
		})
		if err != nil {
			t.Fatalf("ListAlertHistory one: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("single-tenant history = %d entries, want 2 (%+v)", len(got), got)
		}
		if got[0].Kind != "resolved" || got[1].Kind != "fired" {
			t.Errorf("not newest-first: %+v", got)
		}
		for _, e := range got {
			if e.Tenant != "m1" {
				t.Errorf("entry tenant = %q, want m1: %+v", e.Tenant, e)
			}
		}
	})

	t.Run("UnionCarriesTheFiringMember", func(t *testing.T) {
		got, err := store.ListAlertHistory(ctx, storage.AlertHistoryQuery{
			Tenant: "m1", Tenants: both,
		})
		if err != nil {
			t.Fatalf("ListAlertHistory merged: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("merged history = %d entries, want 3 (%+v)", len(got), got)
		}
		wantTenants := []string{"m1", "m2", "m1"} // base+3, +2, +1
		for i, want := range wantTenants {
			if got[i].Tenant != want {
				t.Fatalf("entry %d tenant = %q, want %q (%+v)", i, got[i].Tenant, want, got)
			}
		}
		for _, e := range got {
			if e.Target == "noise" {
				t.Errorf("non-member tenant leaked into the union: %+v", e)
			}
		}
	})

	t.Run("RangeAndLimitOverUnion", func(t *testing.T) {
		got, err := store.ListAlertHistory(ctx, storage.AlertHistoryQuery{
			Tenant: "m1", Tenants: both,
			Range: storage.TimeRange{Start: base.Add(2 * time.Minute), End: base.Add(3 * time.Minute)},
		})
		if err != nil {
			t.Fatalf("ListAlertHistory ranged: %v", err)
		}
		if len(got) != 1 || got[0].Tenant != "m2" {
			t.Fatalf("ranged union wrong: %+v", got)
		}

		capped, err := store.ListAlertHistory(ctx, storage.AlertHistoryQuery{
			Tenant: "m1", Tenants: both, Limit: 2,
		})
		if err != nil {
			t.Fatalf("ListAlertHistory limited: %v", err)
		}
		if len(capped) != 2 || capped[0].Tenant != "m1" || capped[1].Tenant != "m2" {
			t.Fatalf("limit over the union wrong: %+v", capped)
		}
	})

	t.Run("EmptySetKeepsTheLegacyTenant", func(t *testing.T) {
		got, err := store.ListAlertHistory(ctx, storage.AlertHistoryQuery{Tenant: "m1"})
		if err != nil {
			t.Fatalf("ListAlertHistory default: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("empty Tenants must mean [Tenant]: %+v", got)
		}
	})
}
