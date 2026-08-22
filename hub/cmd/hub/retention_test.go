package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/storagetest"
)

func TestPlanTrimSelectsOnlyProjectsWithTheirOwnWindow(t *testing.T) {
	now := time.Date(2026, 8, 22, 3, 0, 0, 0, time.UTC)
	projects := []storage.Project{
		{ID: "prod"},                      // inherits the global TTL
		{ID: "staging", RetentionDays: 3}, // trims to now-3d
		{ID: "ci", RetentionDays: 1},      // trims to now-1d
		{ID: "estate", RetentionDays: 3, Members: []string{"ci"}}, // aggregate: owns no rows
	}

	jobs := planTrim(projects, now)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %+v, want staging and ci only", jobs)
	}
	if jobs[0].tenant != "staging" || !jobs[0].cutoff.Equal(now.AddDate(0, 0, -3)) {
		t.Errorf("staging job = %+v, want cutoff %v", jobs[0], now.AddDate(0, 0, -3))
	}
	if jobs[1].tenant != "ci" || !jobs[1].cutoff.Equal(now.AddDate(0, 0, -1)) {
		t.Errorf("ci job = %+v, want cutoff %v", jobs[1], now.AddDate(0, 0, -1))
	}
}

func TestTrimOnceTrimsEachProjectToItsOwnCutoff(t *testing.T) {
	now := time.Date(2026, 8, 22, 3, 0, 0, 0, time.UTC)
	f := &storagetest.Fake{Projects: map[string]storage.Project{
		"staging": {ID: "staging", RetentionDays: 3},
		"prod":    {ID: "prod"},
	}}

	trimOnce(context.Background(), func() storage.Store { return f }, now)

	if len(f.Trims) != 1 {
		t.Fatalf("trims = %+v, want only staging", f.Trims)
	}
	if f.Trims[0].Tenant != "staging" || !f.Trims[0].Cutoff.Equal(now.AddDate(0, 0, -3)) {
		t.Fatalf("trim = %+v, want staging at %v", f.Trims[0], now.AddDate(0, 0, -3))
	}
}

// A background sweep has nobody to return an error to, so one project's
// failure must not become every project's: retention for the rest of the
// install would silently stop.
func TestTrimOnceContinuesPastAFailingProject(t *testing.T) {
	f := &storagetest.Fake{
		Projects: map[string]storage.Project{
			"a-staging": {ID: "a-staging", RetentionDays: 3},
			"b-ci":      {ID: "b-ci", RetentionDays: 1},
		},
		TrimErr: errors.New("mutation refused"),
	}

	trimOnce(context.Background(), func() storage.Store { return f }, time.Now().UTC())

	if len(f.Trims) != 2 {
		t.Fatalf("trims = %+v, want both projects attempted", f.Trims)
	}
}

// ClickHouse comes up after the hub does (connectStore), so a nil store is the
// normal startup state, not an error — the next tick picks it up.
func TestTrimOnceWithoutAStoreIsANoOp(t *testing.T) {
	trimOnce(context.Background(), func() storage.Store { return nil }, time.Now().UTC())
}

// A store that cannot list projects must not be read as "no project wants
// trimming" — nothing is trimmed and the sweep retries next tick.
func TestTrimOnceListFailureTrimsNothing(t *testing.T) {
	f := &storagetest.Fake{
		Projects:    map[string]storage.Project{"staging": {ID: "staging", RetentionDays: 3}},
		ProjectsErr: errors.New("clickhouse down"),
	}

	trimOnce(context.Background(), func() storage.Store { return f }, time.Now().UTC())

	if len(f.Trims) != 0 {
		t.Fatalf("trims = %+v, want none", f.Trims)
	}
}
