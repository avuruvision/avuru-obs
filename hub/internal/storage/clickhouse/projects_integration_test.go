//go:build integration

package clickhouse

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestProjectLifecycle(t *testing.T) {
	st := startClickHouse(t) // migrates the schema (incl. 0012_projects.sql)
	ctx := context.Background()

	if err := st.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging", CreatedBy: "admin"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetProject(ctx, "staging")
	if err != nil || got.Label != "Staging" {
		t.Fatalf("get: %+v err=%v", got, err)
	}

	// Upsert-by-Id replaces the label (ReplacingMergeTree + FINAL).
	if err := st.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging EU"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, p := range list {
		if p.ID == "staging" {
			found = true
			if p.Label != "Staging EU" {
				t.Fatalf("label = %q, want Staging EU", p.Label)
			}
		}
	}
	if !found {
		t.Fatalf("staging missing from list: %+v", list)
	}

	if err := st.DeleteProject(ctx, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetProject(ctx, "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted project resolves: %v", err)
	}
	if err := st.DeleteProject(ctx, "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
