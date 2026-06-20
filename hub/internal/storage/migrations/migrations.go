// Package migrations holds the hub-owned ClickHouse DDL, embedded into the
// binary so `hub migrate` is the single schema mechanism in compose AND k8s
// (see agent_docs/architecture.md and the M2 design spec). Retention/TTL is
// NOT in these files — it is applied env-driven by the migrator.
package migrations

import "embed"

// FS holds the versioned .sql migrations.
//
//go:embed *.sql
var FS embed.FS

// Ordered is the apply order; each filename is the version id recorded in the
// schema_migrations ledger (lexical order = apply order).
var Ordered = []string{
	"0001_traces.sql",
	"0002_logs.sql",
}
