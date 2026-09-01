//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestCollectionOverlayRoundTrip(t *testing.T) {
	store := startClickHouse(t) // migrates otel.collection_overlay via the real migrator
	ctx := context.Background()

	if _, err := store.LoadCollectionOverlay(ctx); err != storage.ErrNotFound {
		t.Fatalf("LoadCollectionOverlay before any save: got err=%v, want storage.ErrNotFound", err)
	}

	if err := store.SaveCollectionOverlay(ctx, storage.CollectionOverlay{
		Overlay:   `{"obiEnabled":false}`,
		UpdatedBy: "admin@example.com",
	}); err != nil {
		t.Fatalf("SaveCollectionOverlay: %v", err)
	}

	got, err := store.LoadCollectionOverlay(ctx)
	if err != nil {
		t.Fatalf("LoadCollectionOverlay after save: %v", err)
	}
	if got.Overlay != `{"obiEnabled":false}` || got.UpdatedBy != "admin@example.com" {
		t.Fatalf("LoadCollectionOverlay = %+v, want Overlay/UpdatedBy to match what was saved", got)
	}

	// A second save replaces the singleton (ReplacingMergeTree by UpdatedAt) —
	// FINAL must return the newest write, not both rows.
	if err := store.SaveCollectionOverlay(ctx, storage.CollectionOverlay{
		Overlay:   `{"obiEnabled":true}`,
		UpdatedBy: "someone-else@example.com",
	}); err != nil {
		t.Fatalf("SaveCollectionOverlay (second write): %v", err)
	}
	got, err = store.LoadCollectionOverlay(ctx)
	if err != nil {
		t.Fatalf("LoadCollectionOverlay after second save: %v", err)
	}
	if got.Overlay != `{"obiEnabled":true}` || got.UpdatedBy != "someone-else@example.com" {
		t.Fatalf("LoadCollectionOverlay after second save = %+v, want the newest write only", got)
	}
}

// The rates overlay is the same singleton idiom as the collection overlay
// above: one logical row, newest write wins, never-saved is ErrNotFound rather
// than an empty string that a parser would have to guess about.
func TestRatesOverlayRoundTripsAsASingleton(t *testing.T) {
	store := startClickHouse(t)
	ctx := context.Background()

	if _, err := store.LoadRatesOverlay(ctx); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("load on a fresh install = %v, want ErrNotFound", err)
	}

	if err := store.SaveRatesOverlay(ctx, storage.RatesOverlay{
		Overlay: `{"currency":"EUR"}`, UpdatedBy: "admin@example.com",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.SaveRatesOverlay(ctx, storage.RatesOverlay{
		Overlay: `{"currency":"USD"}`, UpdatedBy: "second@example.com",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.LoadRatesOverlay(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// FINAL collapses to the newest row — two writes, one table.
	if got.Overlay != `{"currency":"USD"}` {
		t.Errorf("overlay = %q, want the newest write", got.Overlay)
	}
	if got.UpdatedBy != "second@example.com" {
		t.Errorf("updatedBy = %q, want the newest author", got.UpdatedBy)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updatedAt is zero; the server-side default did not apply")
	}
}
