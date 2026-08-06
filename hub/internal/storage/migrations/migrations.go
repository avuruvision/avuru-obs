// Package migrations holds the hub-owned ClickHouse DDL, embedded into the
// binary so `hub migrate` is the single schema mechanism in compose AND k8s
// (see agent_docs/architecture.md and the M2 design spec). Retention/TTL for
// signal data is NOT in these files — it is applied env-driven by the migrator;
// fixed housekeeping TTLs (e.g. session GC in 0010) are the deliberate exception.
package migrations

import (
	"embed"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// FS holds the versioned .sql migrations.
//
//go:embed *.sql
var FS embed.FS

// Ordered is the apply order; each filename is the version id recorded in the
// schema_migrations ledger (lexical order = apply order).
var Ordered = []string{
	"0001_traces.sql",
	"0002_logs.sql",
	"0003_metrics.sql",
	"0004_profiles.sql",
	"0005_span_index.sql",
	"0006_errors.sql",
	"0007_errors_from_logs.sql",
	"0008_alerts.sql",
	"0009_alert_channels.sql",
	"0010_auth.sql",
	"0011_auth_oidc_groups.sql",
	"0012_projects.sql",
	"0013_auth_ingest_keys.sql",
	"0014_collection_overlay.sql",
	"0015_auth_user_deleted.sql",
}

// ByModule tags each migration with the module(s) whose schema it owns; the
// migrator applies a file only when ALL its modules are active. Most files
// belong to one module; a few (a derived view over another module's table)
// require several. Enabling a module later just re-runs `hub migrate`
// (idempotent). Every Ordered entry MUST be tagged — enforced by
// TestByModuleCoversOrdered.
var ByModule = map[string][]modules.Name{
	"0001_traces.sql":     {modules.Core},
	"0002_logs.sql":       {modules.Logs},
	"0003_metrics.sql":    {modules.InfraMetrics},
	"0004_profiles.sql":   {modules.Profiling},
	"0005_span_index.sql": {modules.Core},
	// Span/error-span derivation reads otel_traces (core) — needs only the module.
	"0006_errors.sql": {modules.ErrorTracking},
	// The log-derived view reads otel_logs, so it also needs the logs module.
	"0007_errors_from_logs.sql": {modules.ErrorTracking, modules.Logs},
	// alert_state + alert_history — the evaluator's durable state.
	"0008_alerts.sql": {modules.Alerting},
	// alert_channel — UI-managed delivery channels.
	"0009_alert_channels.sql": {modules.Alerting},
	// Local users, grants, sessions — auth gates everything, so core.
	"0010_auth.sql": {modules.Core},
	// OidcGroups column on auth_user — captured at SSO login, part of core auth.
	"0011_auth_oidc_groups.sql": {modules.Core},
	// UI-managed projects (create/rename/delete). Auth-adjacent core state.
	"0012_projects.sql": {modules.Core},
	// Per-project ingest keys — auth gates ingest, so core.
	"0013_auth_ingest_keys.sql": {modules.Core},
	// collection_overlay — plain feature flag, not a modules.Name; the table
	// exists on every install, only the API routes and RBAC are flag-gated.
	"0014_collection_overlay.sql": {modules.Core},
	// Tombstone column on auth_user — auth gates everything, so core.
	"0015_auth_user_deleted.sql": {modules.Core},
}
