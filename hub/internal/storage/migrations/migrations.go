// Package migrations holds the hub-owned ClickHouse DDL, embedded into the
// binary so `hub migrate` is the single schema mechanism in compose AND k8s
// (see agent_docs/architecture.md and the M2 design spec). Retention/TTL for
// signal data is NOT in these files — it is applied env-driven by the migrator;
// fixed housekeeping TTLs (e.g. session GC in 0010) are the deliberate exception.
//
// The .sql files never name a database: they write {db}, which Statements
// substitutes with the configured one. They used to hardcode `otel.`, which
// silently broke every install that set a different database name — the DDL
// landed in `otel` while the hub queried the configured database and saw an
// empty schema.
package migrations

import (
	"embed"
	"strings"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// DatabasePlaceholder is the token the .sql files use wherever the telemetry
// database is named. Enforced by TestNoHardcodedDatabase.
const DatabasePlaceholder = "{db}"

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
	// Local users, grants, sessions — auth gates everything, so core. (0010's
	// header says auth_user has no Deleted column; superseded by 0015.)
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

// Expected returns, in apply order, the versions that should exist on an
// install running exactly this module set. It is the single definition of
// "what belongs here": the migrator applies it, and the readiness check
// compares the ledger against it. A nil set means every module.
func Expected(active modules.Set) []string {
	if active == nil {
		active = modules.AllSet()
	}
	out := make([]string, 0, len(Ordered))
	for _, version := range Ordered {
		// Apply only when ALL the migration's modules are active. Untagged
		// migrations belong everywhere (belt and braces — TestByModuleCoversOrdered
		// enforces full tagging).
		if mods, ok := ByModule[version]; ok && !allEnabled(active, mods) {
			continue
		}
		out = append(out, version)
	}
	return out
}

// allEnabled reports whether every module in mods is active.
func allEnabled(active modules.Set, mods []modules.Name) bool {
	for _, m := range mods {
		if !active.Enabled(m) {
			return false
		}
	}
	return true
}

// Statements breaks a migration body into individual statements on ';' and
// resolves DatabasePlaceholder to db. Line (`--`) comments are stripped FIRST
// so a ';' inside a comment can't split a statement (our DDL has no '--' or
// ';' inside string literals).
//
// db is interpolated straight into DDL — callers must have validated it as an
// identifier (cmd/hub does, at config load).
func Statements(body, db string) []string {
	var stripped strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}

	var out []string
	for _, chunk := range strings.Split(stripped.String(), ";") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		out = append(out, strings.ReplaceAll(chunk, DatabasePlaceholder, db))
	}
	return out
}
