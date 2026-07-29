package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

func TestFakeProjectRoundtrip(t *testing.T) {
	f := &Fake{}
	ctx := context.Background()

	if err := f.SaveProject(ctx, storage.Project{ID: "team-a", Label: "Team A", Members: []string{}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := f.GetProject(ctx, "team-a")
	if err != nil || got.Label != "Team A" {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	list, err := f.ListProjects(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	if err := f.DeleteProject(ctx, "team-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.GetProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
	if err := f.DeleteProject(ctx, "team-a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound on second delete, got %v", err)
	}
}
