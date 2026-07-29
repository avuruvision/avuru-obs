package storagetest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestFakeProjectLifecycle(t *testing.T) {
	f := &storagetest.Fake{}
	ctx := context.Background()

	if err := f.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.GetProject(ctx, "staging")
	if err != nil || got.Label != "Staging" {
		t.Fatalf("get: %+v err=%v", got, err)
	}

	// Upsert by ID replaces the label.
	if err := f.SaveProject(ctx, storage.Project{ID: "staging", Label: "Staging EU"}); err != nil {
		t.Fatal(err)
	}
	list, err := f.ListProjects(ctx)
	if err != nil || len(list) != 1 || list[0].Label != "Staging EU" {
		t.Fatalf("list: %+v err=%v", list, err)
	}

	// Delete tombstones — get and list no longer see it.
	if err := f.DeleteProject(ctx, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.GetProject(ctx, "staging"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted project still resolves: %v", err)
	}
	if list, _ := f.ListProjects(ctx); len(list) != 0 {
		t.Fatalf("deleted project still listed: %+v", list)
	}
	// Deleting a missing project is ErrNotFound.
	if err := f.DeleteProject(ctx, "nope"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
