//go:build integration

package clickhouse

import (
	"context"
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
