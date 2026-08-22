package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/api"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Per-project retention lives here, not in the migrator. Global retention is a
// table TTL applied at migrate time (ApplyRetention); a per-project window
// cannot be, because the telemetry tables are shared and a TTL expression
// cannot select rows by `Tenant`. So a project that wants to keep less than the
// install does is trimmed by this loop, which issues bounded lightweight
// mutations scoped by tenant (design/2026-07-27-projects-completion.md).
//
// The global TTL remains the backstop for everything: this job only ever
// deletes EARLIER than the install would have, which is why the API refuses a
// per-project window longer than the global one.

// defaultTrimInterval is how often the sweep runs. Retention is a daily-scale
// promise, so an hour is precise enough, and mutations are part rewrites —
// running them more often would cost real IO to delete an hour's worth of rows.
const defaultTrimInterval = time.Hour

// trimJob is one tenant to trim and the cutoff to trim it to.
type trimJob struct {
	tenant string
	cutoff time.Time
}

// planTrim selects the projects with a retention window of their own and turns
// each into a cutoff. Two projects are deliberately skipped:
//
//   - RetentionDays == 0 — the overwhelming majority: they inherit the global
//     TTL and there is nothing for this job to do;
//   - aggregates (members set) — an aggregate owns no rows; its members do, and
//     they carry their own windows. Trimming its id would mutate every table
//     for a tenant that has no data. The API refuses the combination, so this
//     only guards rows written before that rule existed.
func planTrim(projects []storage.Project, now time.Time) []trimJob {
	var jobs []trimJob
	for _, p := range projects {
		if p.RetentionDays <= 0 || len(p.Members) > 0 {
			continue
		}
		jobs = append(jobs, trimJob{
			tenant: p.ID,
			cutoff: now.Add(-time.Duration(p.RetentionDays) * 24 * time.Hour),
		})
	}
	return jobs
}

// runRetentionTrimmer is the background sweep enforcing per-project retention.
// Like the alerting evaluator it assumes ONE active hub replica; a second one
// would issue the same mutations, which is wasteful but not incorrect —
// deleting rows already deleted is a no-op.
func runRetentionTrimmer(ctx context.Context, provider api.StoreProvider, gate *schemaGate, interval time.Duration) {
	// Nothing to trim before the schema exists, and the project table is the
	// first thing the sweep reads.
	if !gate.wait(ctx) {
		return
	}
	if interval <= 0 {
		interval = defaultTrimInterval
	}
	slog.Info("retention trimmer started", "interval", interval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		trimOnce(ctx, provider, time.Now().UTC())
	}
}

// trimOnce runs one sweep. It never returns an error: a background sweep has
// nobody to return one to, and one project's failure must not stop the others
// — a single unreadable table would otherwise freeze retention for the whole
// install.
func trimOnce(ctx context.Context, provider api.StoreProvider, now time.Time) {
	store := provider()
	if store == nil {
		return // ClickHouse not up yet; the next tick retries
	}
	projects, err := store.ListProjects(ctx)
	if err != nil {
		slog.Warn("retention trim: listing projects failed", "error", err)
		return
	}
	for _, job := range planTrim(projects, now) {
		tables, err := store.TrimTenant(ctx, job.tenant, job.cutoff)
		if err != nil {
			slog.Warn("retention trim failed", "project", job.tenant, "cutoff", job.cutoff, "error", err)
			continue
		}
		if len(tables) > 0 {
			slog.Info("retention trim issued", "project", job.tenant, "cutoff", job.cutoff, "tables", tables)
		}
	}
}
