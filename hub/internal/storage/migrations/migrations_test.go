package migrations

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// TestByModuleCoversOrdered guards the migration↔module mapping: an untagged
// migration would silently apply for every install (or worse, a stale tag
// would point at a module that no longer exists).
func TestByModuleCoversOrdered(t *testing.T) {
	known := map[modules.Name]bool{}
	for _, m := range modules.All {
		known[m] = true
	}

	for _, version := range Ordered {
		mods, ok := ByModule[version]
		if !ok || len(mods) == 0 {
			t.Errorf("migration %s has no module tag in ByModule", version)
			continue
		}
		for _, mod := range mods {
			if !known[mod] {
				t.Errorf("migration %s tagged with unknown module %q", version, mod)
			}
		}
	}
	for version := range ByModule {
		found := false
		for _, v := range Ordered {
			if v == version {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ByModule entry %s is not in Ordered", version)
		}
	}
}

func TestExpectedFiltersByModule(t *testing.T) {
	all := Expected(modules.AllSet())
	if !slices.Equal(all, Ordered) {
		t.Errorf("Expected(all) = %v, want every Ordered entry in order", all)
	}
	if got := Expected(nil); !slices.Equal(got, Ordered) {
		t.Errorf("Expected(nil) = %v, want the same as Expected(all)", got)
	}

	// Logs off must drop 0002 (the logs table) AND 0007 — the log-derived error
	// view needs BOTH error-tracking and logs, which is the whole reason
	// ByModule tags a list rather than a single module.
	noLogs, err := modules.Parse("core,error-tracking")
	if err != nil {
		t.Fatalf("parsing module set: %v", err)
	}
	got := Expected(noLogs)
	for _, dropped := range []string{"0002_logs.sql", "0007_errors_from_logs.sql"} {
		if slices.Contains(got, dropped) {
			t.Errorf("Expected(core,error-tracking) contains %s, want it filtered out", dropped)
		}
	}
	for _, kept := range []string{"0001_traces.sql", "0006_errors.sql", "0010_auth.sql"} {
		if !slices.Contains(got, kept) {
			t.Errorf("Expected(core,error-tracking) is missing %s", kept)
		}
	}
}

// ddlVerb matches the leading keyword of a DDL statement.
var ddlVerb = regexp.MustCompile(`(?is)^\s*(CREATE|ALTER|DROP|RENAME|TRUNCATE)\b`)

// TestEveryStatementIsIdempotent is load-bearing, not hygiene: Store.Migrate is
// documented as safe to run concurrently (several hub replicas, or a replica
// racing the chart's migrate Job) WITHOUT a lock, and the only thing making
// that true is that re-executing any statement is a no-op. A new migration that
// forgets `IF NOT EXISTS` would silently turn a concurrent apply into an error.
func TestEveryStatementIsIdempotent(t *testing.T) {
	for _, version := range Ordered {
		body, err := FS.ReadFile(version)
		if err != nil {
			t.Fatalf("reading %s: %v", version, err)
		}
		stmts := Statements(string(body), "testdb")
		if len(stmts) == 0 {
			t.Errorf("%s produced no statements", version)
		}
		for _, stmt := range stmts {
			if !ddlVerb.MatchString(stmt) {
				continue // INSERTs and the like are not schema statements
			}
			if !strings.Contains(strings.ToUpper(stmt), "IF NOT EXISTS") {
				t.Errorf("%s: DDL statement is not idempotent (no IF NOT EXISTS):\n%.120s", version, stmt)
			}
		}
	}
}

// TestNoHardcodedDatabase keeps the .sql files database-agnostic. They used to
// hardcode `otel.`, so any install configuring a different database name got a
// schema created in `otel` while the hub queried the empty configured one — an
// install that looks fine and answers "table does not exist" to everything.
func TestNoHardcodedDatabase(t *testing.T) {
	for _, version := range Ordered {
		body, err := FS.ReadFile(version)
		if err != nil {
			t.Fatalf("reading %s: %v", version, err)
		}
		for _, stmt := range Statements(string(body), "testdb") {
			if strings.Contains(stmt, "otel.") {
				t.Errorf("%s names a database literally; use %s:\n%.120s", version, DatabasePlaceholder, stmt)
			}
			// The migrator creates the configured database itself; a CREATE
			// DATABASE here could only name the wrong one.
			if strings.Contains(strings.ToUpper(stmt), "CREATE DATABASE") {
				t.Errorf("%s creates a database; Store.Migrate owns that:\n%.120s", version, stmt)
			}
		}
	}
}

func TestStatementsSubstitutesDatabase(t *testing.T) {
	got := Statements("-- a comment with ; inside\nCREATE TABLE IF NOT EXISTS {db}.t (a String);\n", "mydb")
	want := []string{"CREATE TABLE IF NOT EXISTS mydb.t (a String)"}
	if !slices.Equal(got, want) {
		t.Errorf("Statements() = %q, want %q", got, want)
	}
}
