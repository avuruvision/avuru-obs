package main

import (
	"context"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestParseIngestSeedKeys(t *testing.T) {
	t.Run("empty is not an error", func(t *testing.T) {
		keys, err := parseIngestSeedKeys("")
		if err != nil || keys != nil {
			t.Fatalf("keys = %+v, err = %v", keys, err)
		}
	})

	t.Run("parses a well-formed array", func(t *testing.T) {
		keys, err := parseIngestSeedKeys(
			`[{"project":"default","name":"sensor","key":"avuruk_abcdefghijkl"}]`)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || keys[0].Project != "default" || keys[0].Name != "sensor" {
			t.Fatalf("keys = %+v", keys)
		}
	})

	// A malformed seed must fail loudly: silently skipping it would look like a
	// healthy enforce install until telemetry stopped landing.
	t.Run("rejects an entry with no project", func(t *testing.T) {
		if _, err := parseIngestSeedKeys(`[{"key":"avuruk_x"}]`); err == nil {
			t.Fatal("accepted a seed key with no project")
		}
	})

	t.Run("rejects an entry with no key", func(t *testing.T) {
		if _, err := parseIngestSeedKeys(`[{"project":"default"}]`); err == nil {
			t.Fatal("accepted a seed entry with no key")
		}
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		if _, err := parseIngestSeedKeys(`not json`); err == nil {
			t.Fatal("accepted malformed JSON")
		}
	})
}

func TestEnsureIngestKeyIsIdempotent(t *testing.T) {
	f := &storagetest.Fake{}
	ctx := context.Background()
	seed := ingestSeedKey{Project: "default", Name: "sensor", Key: "avuruk_abcdefghijkl"}

	if err := ensureIngestKey(ctx, f, seed); err != nil {
		t.Fatal(err)
	}
	// Second call must not create a duplicate — seeding runs on every hub boot.
	if err := ensureIngestKey(ctx, f, seed); err != nil {
		t.Fatal(err)
	}

	keys, err := f.ListIngestKeys(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("got %d keys, want 1 (seeding is not idempotent)", len(keys))
	}

	// Only the hash is persisted; the raw key must never be stored.
	got := keys[0]
	if got.KeyHash != auth.HashIngestKey(seed.Key) {
		t.Fatalf("hash = %q, want hash of the raw key", got.KeyHash)
	}
	if got.KeyHash == seed.Key || got.Prefix == seed.Key {
		t.Fatal("raw key leaked into storage")
	}
	if got.CreatedBy != "chart" {
		t.Fatalf("createdBy = %q, want chart", got.CreatedBy)
	}
}

func TestEnsureIngestKeyLooksUpByHash(t *testing.T) {
	f := &storagetest.Fake{}
	ctx := context.Background()
	seed := ingestSeedKey{Project: "default", Name: "sensor", Key: "avuruk_abcdefghijkl"}
	if err := ensureIngestKey(ctx, f, seed); err != nil {
		t.Fatal(err)
	}

	// The seeded key resolves by the hash of the raw value — this is exactly the
	// lookup the gateway's validate call performs.
	got, err := f.GetIngestKeyByHash(ctx, auth.HashIngestKey(seed.Key))
	if err != nil {
		t.Fatalf("seeded key does not resolve: %v", err)
	}
	if got.Project != "default" {
		t.Fatalf("project = %q, want default", got.Project)
	}

	// An unrelated key must not resolve.
	if _, err := f.GetIngestKeyByHash(ctx, auth.HashIngestKey("avuruk_other")); err == nil {
		t.Fatal("an unseeded key resolved")
	} else if err != storage.ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
