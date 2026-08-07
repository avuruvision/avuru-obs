package main

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/modules"
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/storage/migrations"
)

// schemaChecker is what the gate needs from the store — narrow on purpose, so
// the gate is unit-testable without a ClickHouse container.
type schemaChecker interface {
	SchemaStatus(ctx context.Context, active modules.Set) (storage.SchemaStatus, error)
	Migrate(ctx context.Context, active modules.Set) error
}

const (
	// schemaRecheckInterval paces the "is it there yet?" poll. It is also the
	// floor between self-heal attempts.
	schemaRecheckInterval = 30 * time.Second
	// schemaMaxApplyAttempts caps how many times the gate will run the DDL
	// itself. Checking continues forever; only APPLYING stops, so a condition
	// the hub cannot fix (no DDL grant, a database the migrations don't target)
	// settles into a steady report instead of a DDL storm against ClickHouse.
	schemaMaxApplyAttempts = 5
)

// schemaGate answers one question for the rest of the hub: is the ClickHouse
// schema this install expects actually there?
//
// It exists because schema application is not guaranteed. In Kubernetes the
// migrations run as a Helm post-install/post-upgrade hook, and Helm runs those
// hooks only AFTER `--wait` succeeds — so a release that times out waiting for
// any component leaves a cluster that looks healthy with an empty database,
// permanently. The four subsystems that need tables (admin bootstrap, demo
// user, ingest-key seeding, the alerting evaluator) then each retried forever
// and warned forever, none of them naming the actual problem.
//
// So the gate does two things: it heals the state when it can (the embedded
// migrations are idempotent and safe to apply concurrently — see Store.Migrate),
// and when it can't it says so ONCE, with the remediation, instead of a warning
// flood.
type schemaGate struct {
	active modules.Set
	auto   bool
	// recheck paces the poll and spaces out failed apply attempts. A field
	// only so tests need not sleep for real.
	recheck time.Duration

	ready     chan struct{}
	closeOnce sync.Once
	status    atomic.Pointer[storage.SchemaStatus]
}

func newSchemaGate(active modules.Set, db string, auto bool) *schemaGate {
	g := &schemaGate{active: active, auto: auto, recheck: schemaRecheckInterval, ready: make(chan struct{})}
	// Publish a truthful not-ready status before ClickHouse has even connected:
	// nothing is applied yet, and the expected set is already known from the
	// module config. Without this the status view would read "0 of 0 applied".
	g.status.Store(&storage.SchemaStatus{
		Database: db,
		Expected: migrations.Expected(active),
	})
	return g
}

// Status is the accessor handed to the API. Never nil.
func (g *schemaGate) Status() storage.SchemaStatus {
	if s := g.status.Load(); s != nil {
		return *s
	}
	return storage.SchemaStatus{}
}

// wait blocks until the schema is ready, reporting false if ctx ends first.
// Callers that need tables start with this instead of their own retry loop.
func (g *schemaGate) wait(ctx context.Context) bool {
	select {
	case <-g.ready:
		return true
	case <-ctx.Done():
		return false
	}
}

// run checks the schema, heals it if allowed, and opens the gate once it is
// complete. It NEVER returns an error upward and never exits the process: a
// ClickHouse problem is degraded service, not a crash loop (same contract as
// connectStore). Runs until ready or ctx ends.
func (g *schemaGate) run(ctx context.Context, store schemaChecker) {
	attempts := 0
	reportedOnce := false

	for {
		status, err := store.SchemaStatus(ctx, g.active)
		if err != nil {
			// The backend itself is unhappy (not merely unmigrated) — connectStore
			// already logs connectivity, so keep this quiet and retry.
			slog.Warn("schema check failed, retrying", "error", err)
		} else {
			g.status.Store(&status)
			if status.Ready {
				slog.Info("clickhouse schema ready",
					"database", status.Database, "migrations", len(status.Applied))
				g.closeOnce.Do(func() { close(g.ready) })
				return
			}

			switch {
			case !g.auto:
				g.reportOnce(&reportedOnce, status,
					"schema incomplete and AVURUOBS_SCHEMA_AUTOMIGRATE=false — run `hub migrate`")
			case attempts >= schemaMaxApplyAttempts:
				g.reportOnce(&reportedOnce, status,
					"schema still incomplete after repeated self-heal attempts — run `hub migrate`, and check the ClickHouse user has DDL rights on this database")
			default:
				attempts++
				slog.Warn("clickhouse schema incomplete, applying migrations",
					"database", status.Database, "missing", len(status.Missing), "attempt", attempts)
				if err := store.Migrate(ctx, g.active); err != nil {
					// Fall through to the wait. Retrying immediately would spend
					// the whole attempt budget inside one bad second, which is
					// the opposite of what a transient ClickHouse error needs.
					slog.Warn("schema self-heal failed", "error", err)
					break
				}
				// Applied cleanly — re-check now rather than in 30s, so the
				// gate opens as soon as the schema is actually there.
				continue
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(g.recheck):
		}
	}
}

// reportOnce emits the actionable ERROR a single time, then leaves the loop
// quiet. Operators get one line naming the cause and the fix; a repeating
// error is the flood this whole type exists to remove.
func (g *schemaGate) reportOnce(reported *bool, status storage.SchemaStatus, msg string) {
	if *reported {
		return
	}
	*reported = true
	slog.Error(msg,
		"database", status.Database,
		"applied", len(status.Applied),
		"expected", len(status.Expected),
		"missing", status.Missing)
}
