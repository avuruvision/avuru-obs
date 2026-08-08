//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestServiceGroupRoundtrip(t *testing.T) {
	store := startClickHouse(t) // migrates otel.service_group via the real migrator
	ctx := context.Background()

	t.Run("save and list", func(t *testing.T) {
		err := store.SaveServiceGroup(ctx, storage.ServiceGroup{
			Name: "payments", Tier: "T0",
			Namespaces: []string{"pay"}, Services: []string{"checkout"}, CreatedBy: "u1",
		})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		list, err := store.ListServiceGroups(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %+v err=%v", list, err)
		}
		got := list[0]
		if got.Tier != "T0" || got.CreatedBy != "u1" || len(got.Namespaces) != 1 || got.Services[0] != "checkout" {
			t.Fatalf("unexpected: %+v", got)
		}
	})

	// A nil selector slice must round-trip as an empty array, not NULL — the
	// column is Array(String), which the driver would otherwise reject.
	t.Run("empty selector side round-trips", func(t *testing.T) {
		err := store.SaveServiceGroup(ctx, storage.ServiceGroup{
			Name: "by-namespace-only", Tier: "T2", Namespaces: []string{"shop"},
		})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		list, _ := store.ListServiceGroups(ctx)
		for _, g := range list {
			if g.Name == "by-namespace-only" && len(g.Services) != 0 {
				t.Fatalf("services = %v, want empty", g.Services)
			}
		}
	})

	t.Run("edit keeps newest via FINAL", func(t *testing.T) {
		err := store.SaveServiceGroup(ctx, storage.ServiceGroup{
			Name: "payments", Tier: "T1", Namespaces: []string{"pay", "billing"}, CreatedBy: "u1",
		})
		if err != nil {
			t.Fatalf("resave: %v", err)
		}
		list, _ := store.ListServiceGroups(ctx)
		for _, g := range list {
			if g.Name != "payments" {
				continue
			}
			if g.Tier != "T1" || len(g.Namespaces) != 2 {
				t.Fatalf("want the newest row, got %+v", g)
			}
			return
		}
		t.Fatal("payments missing after edit")
	})

	t.Run("delete tombstones", func(t *testing.T) {
		if err := store.DeleteServiceGroup(ctx, "payments"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		list, _ := store.ListServiceGroups(ctx)
		for _, g := range list {
			if g.Name == "payments" {
				t.Fatal("deleted group still listed")
			}
		}
		if err := store.DeleteServiceGroup(ctx, "payments"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("want ErrNotFound on missing delete, got %v", err)
		}
	})

	// Saving a tombstoned name brings it back, as with project/alert_channel —
	// the UI's "create" path must work after a delete.
	t.Run("save revives a tombstoned name", func(t *testing.T) {
		err := store.SaveServiceGroup(ctx, storage.ServiceGroup{
			Name: "payments", Tier: "T0", Namespaces: []string{"pay"},
		})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		list, _ := store.ListServiceGroups(ctx)
		for _, g := range list {
			if g.Name == "payments" {
				return
			}
		}
		t.Fatal("revived group not listed")
	})
}
