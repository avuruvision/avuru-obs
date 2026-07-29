//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestIngestKeyLifecycle(t *testing.T) {
	store := startClickHouse(t) // migrates otel.auth_ingest_key via the real migrator
	ctx := context.Background()

	k := storage.AuthIngestKey{
		KeyHash: "abc123", Project: "payments",
		Name: "prod-exporter", Prefix: "avuruk_ab12", CreatedBy: "admin",
	}

	t.Run("create and get", func(t *testing.T) {
		if err := store.CreateIngestKey(ctx, k); err != nil {
			t.Fatalf("create: %v", err)
		}
		got, err := store.GetIngestKeyByHash(ctx, "abc123")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Project != "payments" || got.Name != "prod-exporter" || got.Prefix != "avuruk_ab12" {
			t.Fatalf("unexpected: %+v", got)
		}
	})

	t.Run("list live only, scoped to project", func(t *testing.T) {
		other := storage.AuthIngestKey{KeyHash: "def456", Project: "billing", Name: "b", Prefix: "avuruk_de45", CreatedBy: "admin"}
		if err := store.CreateIngestKey(ctx, other); err != nil {
			t.Fatalf("create other: %v", err)
		}
		list, err := store.ListIngestKeys(ctx, "payments")
		if err != nil || len(list) != 1 || list[0].KeyHash != "abc123" {
			t.Fatalf("list: %+v err=%v", list, err)
		}
	})

	t.Run("revoke tombstones and stops resolving", func(t *testing.T) {
		if err := store.RevokeIngestKey(ctx, "payments", "abc123"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := store.GetIngestKeyByHash(ctx, "abc123"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("revoked key still resolves: %v", err)
		}
		list, _ := store.ListIngestKeys(ctx, "payments")
		if len(list) != 0 {
			t.Fatalf("revoked key still listed: %+v", list)
		}
	})

	t.Run("revoke is project-scoped and idempotent-safe", func(t *testing.T) {
		// Wrong project cannot revoke another project's key.
		if err := store.RevokeIngestKey(ctx, "payments", "def456"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("cross-project revoke: want ErrNotFound, got %v", err)
		}
		// Already-revoked / unknown → ErrNotFound.
		if err := store.RevokeIngestKey(ctx, "payments", "abc123"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("double revoke: want ErrNotFound, got %v", err)
		}
	})
}
