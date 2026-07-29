//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestProjectRoundtrip(t *testing.T) {
	store := startClickHouse(t) // migrates otel.project via the real migrator
	ctx := context.Background()

	t.Run("save and get", func(t *testing.T) {
		if err := store.SaveProject(ctx, storage.Project{ID: "team-a", Label: "Team A", Members: []string{}, CreatedBy: "u1"}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := store.GetProject(ctx, "team-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Label != "Team A" || got.CreatedBy != "u1" {
			t.Fatalf("unexpected: %+v", got)
		}
	})

	t.Run("rename keeps newest via FINAL", func(t *testing.T) {
		p, _ := store.GetProject(ctx, "team-a")
		p.Label = "Renamed"
		if err := store.SaveProject(ctx, p); err != nil {
			t.Fatalf("resave: %v", err)
		}
		got, _ := store.GetProject(ctx, "team-a")
		if got.Label != "Renamed" {
			t.Fatalf("want Renamed, got %q", got.Label)
		}
	})

	t.Run("list live only", func(t *testing.T) {
		list, err := store.ListProjects(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %+v err=%v", list, err)
		}
	})

	t.Run("delete tombstones", func(t *testing.T) {
		if err := store.DeleteProject(ctx, "team-a"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := store.GetProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("want ErrNotFound, got %v", err)
		}
		if err := store.DeleteProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("want ErrNotFound on missing delete, got %v", err)
		}
	})
}
