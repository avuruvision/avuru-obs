//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestOIDCMappingRoundtrip(t *testing.T) {
	store := startClickHouse(t) // migrates otel.oidc_group_mapping via the real migrator
	ctx := context.Background()

	t.Run("save two and list", func(t *testing.T) {
		err := store.SaveOIDCGroupMapping(ctx, storage.OIDCGroupMapping{
			Group: "platform", Role: "admin", Projects: []string{"*"}, CreatedBy: "u1",
		})
		if err != nil {
			t.Fatalf("save platform: %v", err)
		}
		err = store.SaveOIDCGroupMapping(ctx, storage.OIDCGroupMapping{
			Group: "oncall", Role: "editor", Projects: []string{"default"}, CreatedBy: "u1",
		})
		if err != nil {
			t.Fatalf("save oncall: %v", err)
		}
		list, err := store.ListOIDCGroupMappings(ctx)
		if err != nil || len(list) != 2 {
			t.Fatalf("list: %+v err=%v", list, err)
		}
	})

	t.Run("edit keeps newest via FINAL", func(t *testing.T) {
		err := store.SaveOIDCGroupMapping(ctx, storage.OIDCGroupMapping{
			Group: "platform", Role: "viewer", Projects: []string{"default"}, CreatedBy: "u1",
		})
		if err != nil {
			t.Fatalf("resave: %v", err)
		}
		list, err := store.ListOIDCGroupMappings(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var found bool
		for _, m := range list {
			if m.Group != "platform" {
				continue
			}
			found = true
			// The upsert must win: the OLD role/projects must be gone, not just
			// the new value present alongside it.
			if m.Role != "viewer" || len(m.Projects) != 1 || m.Projects[0] != "default" {
				t.Fatalf("want the newest row (viewer/[default]), got %+v", m)
			}
			if m.Role == "admin" {
				t.Fatalf("old role leaked through: %+v", m)
			}
		}
		if !found {
			t.Fatal("platform missing after edit")
		}
	})

	t.Run("delete tombstones and the other rule survives", func(t *testing.T) {
		if err := store.DeleteOIDCGroupMapping(ctx, "platform"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		list, err := store.ListOIDCGroupMappings(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var sawPlatform, sawOncall bool
		for _, m := range list {
			if m.Group == "platform" {
				sawPlatform = true
			}
			if m.Group == "oncall" {
				sawOncall = true
			}
		}
		if sawPlatform {
			t.Fatal("deleted rule still listed")
		}
		if !sawOncall {
			t.Fatal("surviving rule disappeared alongside the deleted one")
		}
		if err := store.DeleteOIDCGroupMapping(ctx, "platform"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("want ErrNotFound on missing delete, got %v", err)
		}
	})

	t.Run("reset tombstones every live rule", func(t *testing.T) {
		if err := store.ResetOIDCGroupMappings(ctx); err != nil {
			t.Fatalf("reset: %v", err)
		}
		list, err := store.ListOIDCGroupMappings(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("want empty after reset, got %+v", list)
		}
	})
}
