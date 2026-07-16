// Package migrations holds the hub-owned ClickHouse DDL, embedded into the
// binary so `hub migrate` is the single schema mechanism in compose AND k8s
// (see agent_docs/architecture.md and the M2 design spec). Retention/TTL is
// NOT in these files — it is applied env-driven by the migrator.
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
}

// ByModule tags each migration with the module owning its schema; the
// migrator applies only active modules' files. Enabling a module later just
// re-runs `hub migrate` (idempotent) with the new set. Every Ordered entry
// MUST be tagged — enforced by TestByModuleCoversOrdered.
var ByModule = map[string]modules.Name{
	"0001_traces.sql":     modules.Core,
	"0002_logs.sql":       modules.Logs,
	"0003_metrics.sql":    modules.InfraMetrics,
	"0004_profiles.sql":   modules.Profiling,
	"0005_span_index.sql": modules.Core,
	"0006_errors.sql":     modules.ErrorTracking,
}
