# Declared Service Metadata Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a service declare its own domain, environment, and criticality tier as OTLP resource attributes, so the service-health board groups and tiers itself with no operator config.

**Architecture:** The ClickHouse `ServiceLabels` query gains two dominant-value columns (environment, declared tier) alongside the namespaces it already resolves. The pure `health` package reads them: tier resolves through a four-step precedence chain, and group identity becomes the pair *(domain, environment)*. Everything is optional — an install where nothing is declared produces byte-identical output to today.

**Tech Stack:** Go 1.x (hub), ClickHouse (`argMax` dominant-value pattern), Next.js + TypeScript (ui), Helm + `values.schema.json` (chart).

**Spec:** [design/2026-07-28-declared-service-metadata.md](../../../design/2026-07-28-declared-service-metadata.md)

**Test commands:**
- Hub unit: `cd hub && go test -race ./internal/health/...`
- Hub integration: `cd hub && go test -tags=integration ./internal/storage/clickhouse/...`
  (on Colima, prefix `TESTCONTAINERS_RYUK_DISABLED=true` — the reaper cannot
  bind-mount the Colima docker socket)
- Everything: `make check`
- Chart: `make helm-check`

---

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `hub/internal/storage/store.go` | `ServiceLabel` DTO | Modify (2 fields) |
| `hub/internal/storage/clickhouse/service_labels.go` | dominant-label SQL | Modify |
| `hub/internal/storage/clickhouse/labels_integration_test.go` | ServiceLabels integration test + local span fixture | Create |
| `hub/internal/health/config.go` | config shape, tier validation, `TierOverrides` | Modify |
| `hub/internal/health/tier.go` | tier criticality ordering | Create |
| `hub/internal/health/resolve.go` | assignment, tier precedence, warnings | Modify |
| `hub/internal/health/rollup.go` | composite group key, conflict rule | Modify |
| `hub/internal/api/health.go` | DTO fields, `?environment=` lookup | Modify |
| `hub/internal/alerting/config.go` | `Selector.Environments` | Modify |
| `hub/internal/api/green_budgets.go` | budget environment narrowing | Modify |
| `deploy/helm/avuruops/values.yaml` | `serviceGroups.tierOverrides` | Modify |
| `deploy/helm/avuruops/values.schema.json` | schema for the above | Modify |
| `deploy/helm/template-test.sh` | render assertions | Modify |
| `ui/src/lib/api-types.ts` | `HealthGroup` fields | Modify |
| `ui/src/components/health/group-card.tsx` | env badge | Modify |

---

### Task 1: Storage — carry environment and declared tier

**Files:**
- Modify: `hub/internal/storage/store.go:73-77`
- Modify: `hub/internal/storage/clickhouse/service_labels.go:18-40`
- Modify: `hub/internal/storage/clickhouse/integration_test.go:163-196`
- Test: `hub/internal/storage/clickhouse/labels_integration_test.go`

> **Revised during execution.** The original Step 1 widened the shared
> `testSpan` with a `resAttrs` field. That breaks every **positional** literal
> of the type, and there are 30 of them in `integration_test.go` plus 5 in
> `errors_integration_test.go` — a large diff in tests this change does not
> otherwise touch, with real transcription risk. The fixture is instead local to
> the new file. `testSpan` and `insertSpans` are left untouched.

- [x] **Step 1: (dropped — no shared-fixture change needed)**

- [ ] **Step 2: Write the failing integration test with a local fixture**

Create `hub/internal/storage/clickhouse/labels_integration_test.go`:

```go
//go:build integration

package clickhouse

import (
	"context"
	"testing"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// TestServiceLabelsDeclaredMetadata: ServiceLabels carries the declared
// environment and tier alongside the namespaces, each resolved to its dominant
// value by span count. The environment prefers the current semconv key and
// falls back to the deprecated one.
func TestServiceLabelsDeclaredMetadata(t *testing.T) {
	s := startClickHouse(t)
	now := time.Now().UTC().Truncate(time.Second)

	insertSpans(t, s, []testSpan{
		// Current semconv key wins when both are present.
		{ts: now, traceID: "t1", spanID: "s1", name: "GET /a", kind: "Server", service: "web", duration: time.Millisecond, resAttrs: map[string]string{
			"service.namespace":            "storefront",
			"deployment.environment.name":  "prod",
			"deployment.environment":       "legacy-ignored",
			"avuru.tier":                   "T0",
		}},
		// Deprecated key is the fallback.
		{ts: now, traceID: "t2", spanID: "s2", name: "GET /b", kind: "Server", service: "cart", duration: time.Millisecond, resAttrs: map[string]string{
			"service.namespace":      "storefront",
			"deployment.environment": "staging",
			"avuru.tier":             "T2",
		}},
		// Dominant value: two staging spans beat one prod span.
		{ts: now, traceID: "t3", spanID: "s3", name: "GET /c", kind: "Server", service: "batch", duration: time.Millisecond, resAttrs: map[string]string{
			"deployment.environment.name": "staging",
		}},
		{ts: now, traceID: "t4", spanID: "s4", name: "GET /c", kind: "Server", service: "batch", duration: time.Millisecond, resAttrs: map[string]string{
			"deployment.environment.name": "staging",
		}},
		{ts: now, traceID: "t5", spanID: "s5", name: "GET /c", kind: "Server", service: "batch", duration: time.Millisecond, resAttrs: map[string]string{
			"deployment.environment.name": "prod",
		}},
	})

	got, err := s.ServiceLabels(context.Background(), storage.ServiceQuery{
		Tenant: "default",
		Range:  storage.TimeRange{Start: now.Add(-time.Hour), End: now.Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("ServiceLabels: %v", err)
	}

	byService := map[string]storage.ServiceLabel{}
	for _, l := range got {
		byService[l.Service] = l
	}

	want := map[string]storage.ServiceLabel{
		"web":   {Service: "web", ServiceNamespace: "storefront", Environment: "prod", DeclaredTier: "T0"},
		"cart":  {Service: "cart", ServiceNamespace: "storefront", Environment: "staging", DeclaredTier: "T2"},
		"batch": {Service: "batch", Environment: "staging"},
	}
	for svc, w := range want {
		if byService[svc] != w {
			t.Errorf("ServiceLabels[%q] = %+v, want %+v", svc, byService[svc], w)
		}
	}
}
```

- [ ] **Step 3: Run it to confirm it fails**

Run: `cd hub && go test -tags=integration -run TestServiceLabelsDeclaredMetadata ./internal/storage/clickhouse/ -v`
Expected: FAIL — `storage.ServiceLabel` has no fields `Environment` / `DeclaredTier` (compile error).

- [ ] **Step 4: Add the DTO fields**

In `hub/internal/storage/store.go`, replace the `ServiceLabel` struct (line 73):

```go
type ServiceLabel struct {
	Service          string
	K8sNamespace     string // ResourceAttributes['k8s.namespace.name']
	ServiceNamespace string // ResourceAttributes['service.namespace']
	// Environment is the declared deployment environment: the current semconv
	// key deployment.environment.name, falling back to the deprecated
	// deployment.environment. "" = no environment dimension.
	Environment string
	// DeclaredTier is the raw ResourceAttributes['avuru.tier'] value. It is NOT
	// validated here: application telemetry is untrusted input, so the health
	// package validates it and falls back on garbage rather than failing.
	DeclaredTier string
}
```

- [ ] **Step 5: Extend the SQL**

In `hub/internal/storage/clickhouse/service_labels.go`, replace the query string (lines 18-40) and the scan:

```go
	query := `
SELECT
    ServiceName,
    argMax(k8sns, w) AS k8s_namespace,
    argMax(svcns, w) AS service_namespace,
    argMax(env, w)   AS environment,
    argMax(tier, w)  AS declared_tier
FROM (
    SELECT
        ServiceName,
        ResourceAttributes['k8s.namespace.name'] AS k8sns,
        ResourceAttributes['service.namespace']  AS svcns,
        if(ResourceAttributes['deployment.environment.name'] != '',
           ResourceAttributes['deployment.environment.name'],
           ResourceAttributes['deployment.environment'])  AS env,
        ResourceAttributes['avuru.tier']         AS tier,
        count()                                   AS w
    FROM otel_traces
    WHERE Tenant = ?
      AND Timestamp >= ? AND Timestamp < ?
      AND SpanKind IN ('Server', 'Consumer')`
	args := []any{q.Tenant, q.Range.Start, q.Range.End}
	if q.ExcludeAux {
		query += auxExclusion("")
	}
	query += `
    GROUP BY ServiceName, k8sns, svcns, env, tier
)
GROUP BY ServiceName`
```

And the scan inside the row loop:

```go
		if err := rows.Scan(&l.Service, &l.K8sNamespace, &l.ServiceNamespace, &l.Environment, &l.DeclaredTier); err != nil {
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `cd hub && go test -tags=integration -run TestServiceLabelsDeclaredMetadata ./internal/storage/clickhouse/ -v`
Expected: PASS

- [ ] **Step 7: Verify nothing else broke**

Run: `cd hub && go build ./... && go test -race ./...`
Expected: PASS (health tests still pass — the new fields default to `""`).

- [ ] **Step 8: Commit**

```bash
git add hub/internal/storage/store.go hub/internal/storage/clickhouse/service_labels.go hub/internal/storage/clickhouse/integration_test.go hub/internal/storage/clickhouse/labels_integration_test.go
git commit -m "feat(storage): carry declared environment and tier on ServiceLabel"
```

---

### Task 2: Health — tier criticality ordering

**Files:**
- Create: `hub/internal/health/tier.go`
- Test: `hub/internal/health/tier_test.go`

- [ ] **Step 1: Write the failing test**

Create `hub/internal/health/tier_test.go`:

```go
package health

import "testing"

// TestMoreCritical: the group conflict rule takes the most critical tier, so a
// group holding one T0 service is a T0 group regardless of member order.
func TestMoreCritical(t *testing.T) {
	cases := []struct{ a, b, want Tier }{
		{TierT2, TierT0, TierT0},
		{TierT0, TierT2, TierT0},
		{TierT1, TierT3, TierT1},
		{TierT2, TierT2, TierT2},
		{TierT3, TierT0, TierT0},
	}
	for _, c := range cases {
		if got := moreCritical(c.a, c.b); got != c.want {
			t.Errorf("moreCritical(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// TestParseTierSoft: declared tiers are untrusted input — a valid value parses,
// anything else reports ok=false so the caller can fall back and warn.
func TestParseTierSoft(t *testing.T) {
	valid := map[string]Tier{"T0": TierT0, "T1": TierT1, "T2": TierT2, "T3": TierT3}
	for in, want := range valid {
		got, ok := parseTierSoft(in)
		if !ok || got != want {
			t.Errorf("parseTierSoft(%q) = (%q, %v), want (%q, true)", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "T9", "t0", "critical", "0"} {
		if got, ok := parseTierSoft(in); ok {
			t.Errorf("parseTierSoft(%q) = (%q, true), want ok=false", in, got)
		}
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test -race -run 'TestMoreCritical|TestParseTierSoft' ./internal/health/ -v`
Expected: FAIL — `undefined: moreCritical`, `undefined: parseTierSoft`.

- [ ] **Step 3: Implement**

Create `hub/internal/health/tier.go`:

```go
package health

// tierRank orders tiers by criticality: lower rank is MORE critical. Unknown
// tiers rank last so they never win a conflict.
func tierRank(t Tier) int {
	switch t {
	case TierT0:
		return 0
	case TierT1:
		return 1
	case TierT2:
		return 2
	case TierT3:
		return 3
	default:
		return 4
	}
}

// moreCritical returns the more critical of two tiers. It is the group conflict
// rule: members of one group may declare different tiers, and a group holding a
// T0 service is a T0 group. Understating criticality is the dangerous
// direction, so the most critical member wins.
func moreCritical(a, b Tier) Tier {
	if tierRank(b) < tierRank(a) {
		return b
	}
	return a
}

// parseTierSoft validates a DECLARED tier. Unlike Config.Validate, which fails
// the hub loud on operator typos, this never errors: declarations arrive from
// application telemetry with no review gate, and one team shipping
// `avuru.tier: T9` must not take the health board down for everyone. The caller
// falls back to the default tier and surfaces a warning.
func parseTierSoft(v string) (Tier, bool) {
	t := Tier(v)
	if knownTiers[t] {
		return t, true
	}
	return "", false
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd hub && go test -race -run 'TestMoreCritical|TestParseTierSoft' ./internal/health/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add hub/internal/health/tier.go hub/internal/health/tier_test.go
git commit -m "feat(health): tier criticality ordering and soft declared-tier parsing"
```

---

### Task 3: Health — `tierOverrides` config

**Files:**
- Modify: `hub/internal/health/config.go:69-74` (Config), `:121-147` (Validate)
- Test: `hub/internal/health/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `hub/internal/health/config_test.go`:

```go
// TestParseConfigTierOverrides: the operator's per-service tier override parses
// and validates. Unlike a declared tier, a bad value here fails LOUD — config
// is operator-controlled and a typo must not silently mis-tier.
func TestParseConfigTierOverrides(t *testing.T) {
	c, err := ParseConfig([]byte(`{
	    "defaultTier":"T2",
	    "tierOverrides":{"checkout":"T0","batch":"T3"}
	}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if c.TierOverrides["checkout"] != TierT0 {
		t.Errorf("tierOverrides[checkout] = %q, want T0", c.TierOverrides["checkout"])
	}
	if c.TierOverrides["batch"] != TierT3 {
		t.Errorf("tierOverrides[batch] = %q, want T3", c.TierOverrides["batch"])
	}

	if _, err := ParseConfig([]byte(`{"defaultTier":"T2","tierOverrides":{"checkout":"T9"}}`)); err == nil {
		t.Error("ParseConfig accepted an invalid tierOverrides tier, want error")
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test -race -run TestParseConfigTierOverrides ./internal/health/ -v`
Expected: FAIL — `unknown field "tierOverrides"` (the decoder uses `DisallowUnknownFields`).

- [ ] **Step 3: Add the field**

In `hub/internal/health/config.go`, add to the `Config` struct (after `CriticalEdges`):

```go
	// TierOverrides is the operator's per-service tier, winning over a declared
	// avuru.tier and over a matched group's tier. It exists because a config
	// group is the only other override and it also forces group membership: an
	// operator must be able to correct one service's tier without renaming its
	// group.
	TierOverrides map[string]Tier `json:"tierOverrides,omitempty"`
```

- [ ] **Step 4: Validate it fail-loud**

In `Config.Validate`, insert before the closing `return nil` (after the `Thresholds.Tiers` loop):

```go
	for svc, t := range c.TierOverrides {
		if !knownTiers[t] {
			return fmt.Errorf("tierOverrides[%q] has invalid tier %q (known: T0, T1, T2, T3)", svc, t)
		}
	}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd hub && go test -race ./internal/health/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add hub/internal/health/config.go hub/internal/health/config_test.go
git commit -m "feat(health): serviceGroups.tierOverrides — per-service operator tier"
```

---

### Task 4: Health — tier precedence and environment on the assignment

**Files:**
- Modify: `hub/internal/health/resolve.go`
- Test: `hub/internal/health/resolve_test.go`

- [ ] **Step 1: Write the failing test**

Append to `hub/internal/health/resolve_test.go`:

```go
// TestResolveTierPrecedence pins the four-step chain: tierOverrides beats a
// config group, which beats a declared avuru.tier, which beats defaultTier.
func TestResolveTierPrecedence(t *testing.T) {
	cfg := Config{
		DefaultTier:   TierT2,
		Groups:        []Group{{Name: "payments", Tier: TierT1, Selector: Selector{Services: []string{"checkout", "ledger"}}}},
		TierOverrides: map[string]Tier{"checkout": TierT0},
	}
	stats := []storage.ServiceStats{
		{Name: "checkout"}, {Name: "ledger"}, {Name: "web"}, {Name: "batch"},
	}
	labels := []storage.ServiceLabel{
		{Service: "checkout", DeclaredTier: "T3"},                          // override wins
		{Service: "ledger", DeclaredTier: "T3"},                            // config group wins
		{Service: "web", ServiceNamespace: "storefront", DeclaredTier: "T1"}, // declaration wins
		{Service: "batch", ServiceNamespace: "jobs"},                        // default wins
	}

	got, warnings := resolve(cfg, stats, labels)
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	want := map[string]struct {
		tier   Tier
		source string
	}{
		"checkout": {TierT0, "override"},
		"ledger":   {TierT1, "config"},
		"web":      {TierT1, "declared"},
		"batch":    {TierT2, "default"},
	}
	for svc, w := range want {
		if got[svc].Tier != w.tier || got[svc].TierSource != w.source {
			t.Errorf("resolve[%q] tier=%q source=%q, want tier=%q source=%q",
				svc, got[svc].Tier, got[svc].TierSource, w.tier, w.source)
		}
	}
}

// TestResolveInvalidDeclaredTierIsSoft: a garbage declared tier falls back to
// defaultTier and produces a warning — never an error, never a crash.
func TestResolveInvalidDeclaredTierIsSoft(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{{Name: "rogue"}}
	labels := []storage.ServiceLabel{{Service: "rogue", ServiceNamespace: "apps", DeclaredTier: "T9"}}

	got, warnings := resolve(cfg, stats, labels)
	if got["rogue"].Tier != TierT2 {
		t.Errorf("tier = %q, want fallback T2", got["rogue"].Tier)
	}
	if got["rogue"].TierSource != "default" {
		t.Errorf("tierSource = %q, want default", got["rogue"].TierSource)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
	if !strings.Contains(warnings[0], "rogue") || !strings.Contains(warnings[0], "T9") {
		t.Errorf("warning %q should name the service and the bad value", warnings[0])
	}
}

// TestResolveEnvironmentCarried: the declared environment lands on the
// assignment; a service declaring none gets "" (today's behavior).
func TestResolveEnvironmentCarried(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{{Name: "web"}, {Name: "legacy"}}
	labels := []storage.ServiceLabel{
		{Service: "web", ServiceNamespace: "storefront", Environment: "prod"},
		{Service: "legacy", ServiceNamespace: "storefront"},
	}

	got, _ := resolve(cfg, stats, labels)
	if got["web"].Environment != "prod" {
		t.Errorf("web environment = %q, want prod", got["web"].Environment)
	}
	if got["legacy"].Environment != "" {
		t.Errorf("legacy environment = %q, want empty", got["legacy"].Environment)
	}
}
```

Add `"strings"` to that file's imports.

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test -race -run TestResolve ./internal/health/ -v`
Expected: FAIL — `resolve` returns 1 value not 2; `assignment` has no `TierSource` / `Environment`.

- [ ] **Step 3: Implement**

Replace `hub/internal/health/resolve.go` lines 10-54 (the `assignment` type, `namespaceOf`, and `resolve`) with:

```go
// assignment is the resolved grouping for one service.
type assignment struct {
	Service     string
	Group       string
	Environment string // declared deployment environment; "" = no dimension
	Tier        Tier
	Source      string // "config" (matched a group selector) or "auto" (by namespace)
	TierSource  string // "override" | "config" | "declared" | "default"
}

// namespaceOf resolves a service's grouping namespace: k8s.namespace.name,
// falling back to service.namespace, then the unlabeled bucket.
func namespaceOf(l storage.ServiceLabel) string {
	switch {
	case l.K8sNamespace != "":
		return l.K8sNamespace
	case l.ServiceNamespace != "":
		return l.ServiceNamespace
	default:
		return unlabeledNamespace
	}
}

// resolve assigns every service to a group. Config selectors win (first
// matching group, in registry order); unmatched services auto-group by their
// namespace — the hybrid auto+config model. Tier resolves independently through
// tierOverrides > config group > declared avuru.tier > defaultTier. The service
// set comes from stats (the RED population); labels supply the declarations.
//
// It returns warnings for declarations it could not honour. Warnings are never
// errors: application telemetry has no operator review gate, so bad input
// degrades one service's tier, never the whole board.
func resolve(cfg Config, stats []storage.ServiceStats, labels []storage.ServiceLabel) (map[string]assignment, []string) {
	labelByService := make(map[string]storage.ServiceLabel, len(labels))
	for _, l := range labels {
		labelByService[l.Service] = l
	}

	out := make(map[string]assignment, len(stats))
	var warnings []string
	for _, s := range stats {
		// A service with no label row yields the zero ServiceLabel, and
		// namespaceOf already maps that to the unlabeled bucket.
		l := labelByService[s.Name]
		ns := namespaceOf(l)

		a := assignment{Service: s.Name, Environment: l.Environment}
		g, matched := matchGroup(cfg, s.Name, ns)
		if matched {
			a.Group, a.Source = g.Name, "config"
			a.Tier, a.TierSource = g.Tier, "config"
		} else {
			a.Group, a.Source = ns, "auto"
			a.Tier, a.TierSource = cfg.DefaultTier, "default"
			if l.DeclaredTier != "" {
				if t, ok := parseTierSoft(l.DeclaredTier); ok {
					a.Tier, a.TierSource = t, "declared"
				} else {
					warnings = append(warnings, fmt.Sprintf(
						"service %q declared an invalid avuru.tier %q — using %s",
						s.Name, l.DeclaredTier, cfg.DefaultTier))
				}
			}
		}
		// A config group still loses to an explicit per-service override.
		if t, ok := cfg.TierOverrides[s.Name]; ok {
			a.Tier, a.TierSource = t, "override"
		}
		out[s.Name] = a
	}
	sort.Strings(warnings)
	return out, warnings
}
```

Update that file's imports to:

```go
import (
	"fmt"
	"sort"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)
```

- [ ] **Step 4: Keep the exported `Assign` wrapper compiling**

`Assign` is the green module's seam and must keep its single-value signature. In the same file, update its body and the `Assignment` struct:

```go
// Assignment is the exported view of one service's resolved grouping, for
// callers outside the rollup (the green module maps services→groups with it).
type Assignment struct {
	Service     string
	Group       string
	Environment string
	Tier        Tier
	Source      string // "config" (matched a group selector) or "auto" (by namespace)
	TierSource  string // "override" | "config" | "declared" | "default"
}
```

```go
func Assign(cfg Config, stats []storage.ServiceStats, labels []storage.ServiceLabel) map[string]Assignment {
	in, _ := resolve(cfg, stats, labels)
	out := make(map[string]Assignment, len(in))
	for svc, a := range in {
		out[svc] = Assignment(a)
	}
	return out
}
```

- [ ] **Step 5: Fix the existing caller in `rollup.go`**

`Rollup` calls `resolve` at line 60. Change it to:

```go
	assign, _ := resolve(cfg, stats, labels)
```

(Task 6 replaces this with real warning plumbing.)

- [ ] **Step 6: Update the existing assignment expectations**

`TestAssignMatchesResolve` in `resolve_test.go` compares whole structs. Update its `want` map and its call:

```go
	got := Assign(cfg, stats, labels)
	want, _ := resolve(cfg, stats, labels)
```

```go
	checks := map[string]Assignment{
		"checkout": {Service: "checkout", Group: "payments", Tier: TierT0, Source: "config", TierSource: "config"},
		"web":      {Service: "web", Group: "storefront", Tier: TierT1, Source: "config", TierSource: "config"},
		"batch":    {Service: "batch", Group: "jobs", Tier: TierT2, Source: "auto", TierSource: "default"},
		"ghost":    {Service: "ghost", Group: unlabeledNamespace, Tier: TierT2, Source: "auto", TierSource: "default"},
	}
```

- [ ] **Step 7: Run to verify it passes**

Run: `cd hub && go test -race ./internal/health/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add hub/internal/health/resolve.go hub/internal/health/resolve_test.go hub/internal/health/rollup.go
git commit -m "feat(health): tier precedence chain and declared environment on assignments"
```

---

### Task 5: Health — composite group key and conflict rule

**Files:**
- Modify: `hub/internal/health/rollup.go:35-48` (GroupHealth), `:176-230` (groupAndRoll)
- Test: `hub/internal/health/rollup_test.go`

- [ ] **Step 1: Write the failing test**

Append to `hub/internal/health/rollup_test.go`:

```go
// TestGroupsSplitByEnvironment: one domain declared in two environments becomes
// two groups, each keeping its own tier. Name stays the domain; Environment
// carries the dimension.
func TestGroupsSplitByEnvironment(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{
		{Name: "pay-prod", SpanCount: 100},
		{Name: "pay-stg", SpanCount: 100},
	}
	labels := []storage.ServiceLabel{
		{Service: "pay-prod", ServiceNamespace: "payments", Environment: "prod", DeclaredTier: "T0"},
		{Service: "pay-stg", ServiceNamespace: "payments", Environment: "staging", DeclaredTier: "T2"},
	}

	rep := Rollup(cfg, time.Minute, stats, labels, nil)
	if len(rep.Groups) != 2 {
		t.Fatalf("got %d groups, want 2: %+v", len(rep.Groups), rep.Groups)
	}
	byEnv := map[string]GroupHealth{}
	for _, g := range rep.Groups {
		if g.Name != "payments" {
			t.Errorf("group name = %q, want payments", g.Name)
		}
		byEnv[g.Environment] = g
	}
	if byEnv["prod"].Tier != TierT0 {
		t.Errorf("prod tier = %q, want T0", byEnv["prod"].Tier)
	}
	if byEnv["staging"].Tier != TierT2 {
		t.Errorf("staging tier = %q, want T2", byEnv["staging"].Tier)
	}
}

// TestGroupTierMostCriticalWins: members of one group declaring different tiers
// roll up to the most critical, regardless of iteration order.
func TestGroupTierMostCriticalWins(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{
		{Name: "a", SpanCount: 10}, {Name: "b", SpanCount: 10}, {Name: "c", SpanCount: 10},
	}
	labels := []storage.ServiceLabel{
		{Service: "a", ServiceNamespace: "shop", DeclaredTier: "T3"},
		{Service: "b", ServiceNamespace: "shop", DeclaredTier: "T0"},
		{Service: "c", ServiceNamespace: "shop", DeclaredTier: "T2"},
	}

	rep := Rollup(cfg, time.Minute, stats, labels, nil)
	if len(rep.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(rep.Groups))
	}
	if rep.Groups[0].Tier != TierT0 {
		t.Errorf("group tier = %q, want T0 (most critical member)", rep.Groups[0].Tier)
	}
}

// TestNoEnvironmentKeepsTodaysShape: declaring nothing yields exactly one group
// per namespace with an empty Environment — the backward-compatibility gate.
func TestNoEnvironmentKeepsTodaysShape(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{{Name: "web", SpanCount: 10}, {Name: "api", SpanCount: 10}}
	labels := []storage.ServiceLabel{
		{Service: "web", K8sNamespace: "shop"},
		{Service: "api", K8sNamespace: "shop"},
	}

	rep := Rollup(cfg, time.Minute, stats, labels, nil)
	if len(rep.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(rep.Groups))
	}
	if rep.Groups[0].Name != "shop" || rep.Groups[0].Environment != "" {
		t.Errorf("group = %q env %q, want shop with empty env", rep.Groups[0].Name, rep.Groups[0].Environment)
	}
	if len(rep.Groups[0].Members) != 2 {
		t.Errorf("members = %d, want 2", len(rep.Groups[0].Members))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test -race -run 'TestGroups|TestGroupTier|TestNoEnvironment' ./internal/health/ -v`
Expected: FAIL — `GroupHealth` has no field `Environment`; the two-environment case returns 1 group.

- [ ] **Step 3: Add the fields to `GroupHealth`**

In `hub/internal/health/rollup.go`, replace the `GroupHealth` struct (line 36):

```go
// GroupHealth is one group's rolled-up health. Identity is the pair
// (Name, Environment): Name stays the domain so name-keyed consumers (the
// group-detail route, alerting selectors, green budgets) keep matching, and
// Environment carries the new dimension.
type GroupHealth struct {
	Name        string
	Environment string
	Tier        Tier
	Source      string // "config" | "auto"
	TierSource  string // "override" | "config" | "declared" | "default"
	Status      string
	Reason      string
	Counts      map[string]int // per-status member tally
	SpanCount   uint64
	RatePerSec  float64
	ErrorRate   float64
	P95Ms       float64
	Members     []Member
}
```

- [ ] **Step 4: Key `groupAndRoll` on the pair**

Replace `groupAndRoll` (lines 178-230) with:

```go
// groupKey is a group's composite identity. An empty environment collapses to
// the bare group name so installs that declare nothing keep today's keys.
func groupKey(group, env string) string {
	if env == "" {
		return group
	}
	return group + "\x00" + env
}

// groupAndRoll buckets members by (group, environment) and computes each
// group's rolled-up status (worst-of effective, ignoring idle) and aggregate
// RED. The group's tier is its MOST CRITICAL member's tier.
func groupAndRoll(assign map[string]assignment, members map[string]Member) []GroupHealth {
	type acc struct {
		name, env         string
		tier              Tier
		source            string
		tierSource        string
		members           []Member
		spanCount, errCnt uint64
		rate, p95         float64
	}
	byGroup := map[string]*acc{}
	order := []string{}
	for svc, a := range assign {
		k := groupKey(a.Group, a.Environment)
		g, ok := byGroup[k]
		if !ok {
			g = &acc{name: a.Group, env: a.Environment, tier: a.Tier, source: a.Source, tierSource: a.TierSource}
			byGroup[k] = g
			order = append(order, k)
		} else if t := moreCritical(g.tier, a.Tier); t != g.tier {
			// A more critical member takes over the group's tier and its provenance.
			g.tier, g.tierSource = t, a.TierSource
		}
		m := members[svc]
		g.members = append(g.members, m)
		g.spanCount += m.SpanCount
		g.errCnt += uint64(m.ErrorRate * float64(m.SpanCount))
		g.rate += m.RatePerSec
		if m.P95Ms > g.p95 {
			g.p95 = m.P95Ms // conservative group headline: worst member p95
		}
	}
	sort.Strings(order)

	out := make([]GroupHealth, 0, len(order))
	for _, k := range order {
		g := byGroup[k]
		sort.Slice(g.members, func(i, j int) bool { return g.members[i].Service < g.members[j].Service })
		status, counts := rollUpMembers(g.members)
		var errRate float64
		if g.spanCount > 0 {
			errRate = float64(g.errCnt) / float64(g.spanCount)
		}
		out = append(out, GroupHealth{
			Name:        g.name,
			Environment: g.env,
			Tier:        g.tier,
			Source:      g.source,
			TierSource:  g.tierSource,
			Status:      status,
			Reason:      groupReason(status, g.members),
			Counts:      counts,
			SpanCount:   g.spanCount,
			RatePerSec:  g.rate,
			ErrorRate:   errRate,
			P95Ms:       g.p95,
			Members:     g.members,
		})
	}
	return out
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `cd hub && go test -race ./internal/health/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add hub/internal/health/rollup.go hub/internal/health/rollup_test.go
git commit -m "feat(health): group identity becomes (domain, environment) with most-critical tier"
```

---

### Task 6: Health + API — surface warnings

**Files:**
- Modify: `hub/internal/health/rollup.go:50-82` (Report, Rollup)
- Modify: `hub/internal/api/health.go:41-60`, `:135-173`
- Test: `hub/internal/health/rollup_test.go`

- [ ] **Step 1: Write the failing test**

Append to `hub/internal/health/rollup_test.go`:

```go
// TestRollupSurfacesWarnings: a bad declaration reaches the report so the API
// can show it, without failing the rollup.
func TestRollupSurfacesWarnings(t *testing.T) {
	cfg := Config{DefaultTier: TierT2}
	stats := []storage.ServiceStats{{Name: "rogue", SpanCount: 10}}
	labels := []storage.ServiceLabel{{Service: "rogue", ServiceNamespace: "apps", DeclaredTier: "nonsense"}}

	rep := Rollup(cfg, time.Minute, stats, labels, nil)
	if len(rep.Warnings) != 1 {
		t.Fatalf("Warnings = %v, want 1", rep.Warnings)
	}
	if len(rep.Groups) != 1 || rep.Groups[0].Tier != TierT2 {
		t.Errorf("rollup should still produce a T2 group, got %+v", rep.Groups)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test -race -run TestRollupSurfacesWarnings ./internal/health/ -v`
Expected: FAIL — `rep.Warnings` undefined.

- [ ] **Step 3: Add `Warnings` to `Report` and plumb it**

In `hub/internal/health/rollup.go`, replace the `Report` struct (line 51):

```go
// Report is the whole tenant's group health for a window. Warnings carry
// declarations the hub could not honour (e.g. an invalid avuru.tier); they are
// informational and never block the report.
type Report struct {
	Overall  string
	Groups   []GroupHealth
	Warnings []string
}
```

In `Rollup`, change line 60 and the return:

```go
	assign, warnings := resolve(cfg, stats, labels)
```

```go
	return Report{Overall: overallStatus(groups), Groups: groups, Warnings: warnings}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd hub && go test -race ./internal/health/ -v`
Expected: PASS

- [ ] **Step 5: Expose the new fields on the API**

In `hub/internal/api/health.go`, replace `healthGroupDTO` (line 41) and `healthGroupsResponse` (line 55):

```go
type healthGroupDTO struct {
	Name        string            `json:"name"`
	Environment string            `json:"environment,omitempty"`
	Tier        string            `json:"tier"`
	Source      string            `json:"source"`
	TierSource  string            `json:"tierSource"`
	Status      string            `json:"status"`
	Reason      string            `json:"reason"`
	Counts      map[string]int    `json:"counts"`
	SpanCount   uint64            `json:"spanCount"`
	RatePerSec  float64           `json:"ratePerSec"`
	ErrorRate   float64           `json:"errorRate"`
	P95Ms       float64           `json:"p95Ms"`
	Members     []healthMemberDTO `json:"members"`
}

type healthGroupsResponse struct {
	Overall   string           `json:"overall"`
	CheckedAt string           `json:"checkedAt"`
	Window    healthWindowDTO  `json:"window"`
	Groups    []healthGroupDTO `json:"groups"`
	Warnings  []string         `json:"warnings,omitempty"`
}
```

In `handleHealthGroups`, add the warnings to the response literal:

```go
		Groups:    make([]healthGroupDTO, 0, len(report.Groups)),
		Warnings:  report.Warnings,
```

In `toHealthGroupDTO`, replace the returned literal's head:

```go
	return healthGroupDTO{
		Name:        g.Name,
		Environment: g.Environment,
		Tier:        string(g.Tier),
		Source:      g.Source,
		TierSource:  g.TierSource,
		Status:      g.Status,
		Reason:      g.Reason,
		Counts:      g.Counts,
		SpanCount:   g.SpanCount,
		RatePerSec:  g.RatePerSec,
		ErrorRate:   g.ErrorRate,
		P95Ms:       g.P95Ms,
		Members:     members,
	}
```

- [ ] **Step 6: Narrow the group-detail route by environment**

Still in `health.go`, replace `handleHealthGroup` (line 83):

```go
// handleHealthGroup drills into a single group by name (404 if absent). With
// environments, one name can address several groups; ?environment= narrows to
// one. Without it the first match wins, so existing links keep working.
func (a *API) handleHealthGroup(w http.ResponseWriter, r *http.Request) error {
	report, _, err := a.buildHealthReport(r)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	env := r.URL.Query().Get("environment")
	for _, g := range report.Groups {
		if g.Name != name {
			continue
		}
		if env != "" && g.Environment != env {
			continue
		}
		writeJSON(w, http.StatusOK, toHealthGroupDTO(g))
		return nil
	}
	return storage.ErrNotFound
}
```

- [ ] **Step 7: Verify the whole hub builds and passes**

Run: `cd hub && go build ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add hub/internal/health/rollup.go hub/internal/health/rollup_test.go hub/internal/api/health.go
git commit -m "feat(api): expose group environment, tierSource, and declaration warnings"
```

---

### Task 7: Name-keyed consumers — alerting and green budgets

**Files:**
- Modify: `hub/internal/alerting/config.go:49-57`
- Modify: `hub/internal/api/green_budgets.go:176`
- Test: `hub/internal/alerting/config_test.go`

**Why this task is load-bearing:** `buildTargets` (`evaluator.go:139-158`) keys
targets by `"group:"+g.Name` into a `map[string]targetInfo`. Once one domain
exists in two environments, the second group **overwrites the first** and one
environment's alerts silently disappear. Composite target keys are the fix, not
a nicety.

- [ ] **Step 1: Write the failing test**

Append to `hub/internal/alerting/config_test.go`:

```go
// TestSelectorEnvironments: a selector naming no environment matches every
// environment (existing rules keep their blast radius); naming one narrows.
func TestSelectorEnvironments(t *testing.T) {
	all := Selector{Groups: []string{"payments"}}
	if !all.matchesEnvironment("prod") || !all.matchesEnvironment("staging") || !all.matchesEnvironment("") {
		t.Error("a selector with no environments should match every environment")
	}

	narrowed := Selector{Groups: []string{"payments"}, Environments: []string{"prod"}}
	if !narrowed.matchesEnvironment("prod") {
		t.Error("narrowed selector should match its own environment")
	}
	if narrowed.matchesEnvironment("staging") {
		t.Error("narrowed selector should not match another environment")
	}
}
```

Append to `hub/internal/alerting/evaluator_test.go`:

```go
// TestBuildTargetsPerEnvironment: two environments of one domain must be TWO
// targets. Keying by bare name would let the second overwrite the first and
// silently drop an environment's alerts.
func TestBuildTargetsPerEnvironment(t *testing.T) {
	report := health.Report{Groups: []health.GroupHealth{
		{Name: "payments", Environment: "prod", Tier: health.TierT0, Status: health.StatusDown, Reason: "prod is down"},
		{Name: "payments", Environment: "staging", Tier: health.TierT2, Status: health.StatusHealthy, Reason: "ok"},
		{Name: "legacy", Status: health.StatusHealthy, Reason: "ok"},
	}}

	got := buildTargets(report)
	if got["group:payments[prod]"].status != health.StatusDown {
		t.Errorf("group:payments[prod] = %+v, want down", got["group:payments[prod]"])
	}
	if got["group:payments[staging]"].status != health.StatusHealthy {
		t.Errorf("group:payments[staging] = %+v, want healthy", got["group:payments[staging]"])
	}
	// An environment-less group keeps its bare key: existing rules and stored
	// alert state must not be invalidated.
	if got["group:legacy"].status != health.StatusHealthy {
		t.Errorf("group:legacy = %+v, want healthy", got["group:legacy"])
	}
}

// TestSelectorMatchesEnvironmentScopedTarget: a rule naming the domain matches
// every environment of it; adding environments narrows to one.
func TestSelectorMatchesEnvironmentScopedTarget(t *testing.T) {
	all := Selector{Groups: []string{"payments"}}
	for _, target := range []string{"group:payments", "group:payments[prod]", "group:payments[staging]"} {
		if !selectorMatches(all, target) {
			t.Errorf("unnarrowed selector should match %q", target)
		}
	}

	narrowed := Selector{Groups: []string{"payments"}, Environments: []string{"prod"}}
	if !selectorMatches(narrowed, "group:payments[prod]") {
		t.Error("narrowed selector should match its own environment")
	}
	if selectorMatches(narrowed, "group:payments[staging]") {
		t.Error("narrowed selector should not match another environment")
	}
}
```

Ensure that file imports `"github.com/avuru/avuru-obs/hub/internal/health"`.

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd hub && go test -race -run 'TestSelectorEnvironments|TestBuildTargets|TestSelectorMatchesEnvironment' ./internal/alerting/ -v`
Expected: FAIL — `Environments` / `matchesEnvironment` undefined, and
`group:payments[prod]` is absent because targets are keyed by bare name.

- [ ] **Step 3: Implement**

In `hub/internal/alerting/config.go`, replace the `Selector` struct and `empty()`:

```go
type Selector struct {
	Groups   []string `json:"groups,omitempty"`
	Services []string `json:"services,omitempty"`
	Tiers    []string `json:"tiers,omitempty"`
	// Environments narrows a group match to specific declared environments.
	// EMPTY MEANS ALL — a rule written before environments existed must keep
	// firing on the same set once services start declaring one.
	Environments []string `json:"environments,omitempty"`
}

func (s Selector) empty() bool {
	return len(s.Groups) == 0 && len(s.Services) == 0 && len(s.Tiers) == 0
}

// matchesEnvironment reports whether a group in env is in scope. No configured
// environments = every environment.
func (s Selector) matchesEnvironment(env string) bool {
	if len(s.Environments) == 0 {
		return true
	}
	for _, e := range s.Environments {
		if e == env {
			return true
		}
	}
	return false
}
```

Note `empty()` deliberately ignores `Environments`: environments narrow a match, they never constitute one on their own.

- [ ] **Step 4: Key targets by the composite identity**

In `hub/internal/alerting/evaluator.go`, add these two helpers directly above `buildTargets`:

```go
// groupTargetKey addresses a group. An environment-less group keeps the bare
// "group:<name>" key so existing rules and stored alert state stay valid; a
// declared environment gets its own key, because two environments of one domain
// are two targets — keying both by name would let one silently overwrite the
// other in the targets map.
func groupTargetKey(g health.GroupHealth) string {
	if g.Environment == "" {
		return "group:" + g.Name
	}
	return "group:" + g.Name + "[" + g.Environment + "]"
}

// splitGroupTarget splits "payments[prod]" into ("payments", "prod"), and
// "payments" into ("payments", "").
func splitGroupTarget(s string) (name, env string) {
	if i := strings.LastIndex(s, "["); i >= 0 && strings.HasSuffix(s, "]") {
		return s[:i], s[i+1 : len(s)-1]
	}
	return s, ""
}
```

In `buildTargets`, replace the group-target line:

```go
		out[groupTargetKey(g)] = targetInfo{g.Status, g.Reason}
```

and, in the same loop, replace the tier-worst reason so the environment is visible in the alert text:

```go
			if !ok || severity(g.Status) > severity(cur.status) {
				label := g.Name
				if g.Environment != "" {
					label += " [" + g.Environment + "]"
				}
				tierWorst[tk] = targetInfo{g.Status, label + " is " + g.Status}
			}
```

- [ ] **Step 5: Match the name portion, then narrow by environment**

Still in `evaluator.go`, replace the `group:` case of `selectorMatches`:

```go
	case strings.HasPrefix(target, "group:"):
		name, env := splitGroupTarget(strings.TrimPrefix(target, "group:"))
		return contains(sel.Groups, name) && sel.matchesEnvironment(env)
```

- [ ] **Step 6: Make green budgets environment-aware**

In `hub/internal/api/green_budgets.go`, `usedKgByGroup` keys carbon by `Assignment.Group`. Because `Assignment` now carries `Environment`, a budget naming a domain must sum across every environment for that domain — which is what keying by `Group` alone already does. Add the clarifying comment above `usedKgByGroup` so the behavior is deliberate rather than accidental:

```go
// usedKgByGroup sums carbon per GROUP NAME, deliberately across environments:
// a budget names a domain, and with no environment configured it covers every
// environment of that domain — matching the alerting selector rule.
```

- [ ] **Step 7: Run to verify it passes**

Run: `cd hub && go build ./... && go test -race ./...`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add hub/internal/alerting/config.go hub/internal/alerting/config_test.go hub/internal/alerting/evaluator.go hub/internal/alerting/evaluator_test.go hub/internal/api/green_budgets.go
git commit -m "feat(alerting): per-environment group targets and optional selector environments"
```

---

### Task 8: Chart — `tierOverrides` values and schema

**Files:**
- Modify: `deploy/helm/avuruops/values.yaml:156-180` (serviceGroups block)
- Modify: `deploy/helm/avuruops/values.schema.json` (serviceGroups properties)
- Modify: `deploy/helm/template-test.sh`

- [ ] **Step 1: Add the values key**

In `deploy/helm/avuruops/values.yaml`, inside the `serviceGroups:` block, insert after the `groups: []` entry and its commented example:

```yaml
  # Per-service tier, set by the operator. Wins over a service's declared
  # avuru.tier AND over a matched group's tier — the way to correct one
  # service's criticality without moving it into a different group.
  tierOverrides: {}
  #  valife-financial-staging: T1
```

- [ ] **Step 2: Add the schema**

In `deploy/helm/avuruops/values.schema.json`, inside `properties.serviceGroups.properties`, add a sibling to `groups`:

```json
      "tierOverrides": {
        "type": "object",
        "additionalProperties": {
          "type": "string",
          "enum": ["T0", "T1", "T2", "T3"]
        }
      },
```

- [ ] **Step 3: Add render assertions**

In `deploy/helm/template-test.sh`, next to the existing `AVURUOPS_GROUPS_CONFIG` assertions (around line 157), add:

```bash
out=$(helm template t "$CHART" --set 'serviceGroups.tierOverrides.checkout=T0')
grep -q 'tierOverrides' <<<"$out" || fail "tierOverrides missing from the groups ConfigMap"

if helm template t "$CHART" --set 'serviceGroups.tierOverrides.checkout=T9' >/dev/null 2>&1; then
  fail "schema accepted an invalid tierOverrides tier"
fi
```

- [ ] **Step 4: Run the chart checks**

Run: `make helm-check`
Expected: PASS, including the two new assertions.

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/avuruops/values.yaml deploy/helm/avuruops/values.schema.json deploy/helm/template-test.sh
git commit -m "feat(chart): serviceGroups.tierOverrides value and schema"
```

---

### Task 9: UI — environment badge on group cards

**Files:**
- Modify: `ui/src/lib/api-types.ts:371-390`
- Modify: `ui/src/components/health/group-card.tsx:32-37`

- [ ] **Step 1: Extend the types**

In `ui/src/lib/api-types.ts`, replace the `HealthGroup` and `HealthGroupsResponse` interfaces:

```ts
export interface HealthGroup {
  name: string;
  // Declared deployment environment. Absent = the service declared none, and
  // the group is identified by name alone (pre-environment behavior).
  environment?: string;
  tier: string;
  source: "config" | "auto";
  tierSource: "override" | "config" | "declared" | "default";
  status: HealthStatus;
  reason: string;
  counts: Record<string, number>;
  spanCount: number;
  ratePerSec: number;
  errorRate: number;
  p95Ms: number;
  members: HealthMember[];
}

export interface HealthGroupsResponse {
  overall: HealthStatus;
  checkedAt: string;
  window: { start: string; end: string };
  groups: HealthGroup[];
  // Declarations the hub could not honour (e.g. an invalid avuru.tier).
  warnings?: string[];
}
```

- [ ] **Step 2: Render the badge**

In `ui/src/components/health/group-card.tsx`, replace the name row (lines 32-37):

```tsx
            <div className="flex items-center gap-1.5">
              <span className="truncate text-sm font-semibold">{group.name}</span>
              {group.environment && (
                <span className="rounded bg-primary/15 px-1 text-[10px] text-primary">
                  {group.environment}
                </span>
              )}
              {group.source === "auto" && (
                <span className="rounded bg-base-300 px-1 text-[10px] text-base-content/50">auto</span>
              )}
            </div>
```

- [ ] **Step 3: Verify lint and build**

Run: `cd ui && npm run lint && npm run build`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add ui/src/lib/api-types.ts ui/src/components/health/group-card.tsx
git commit -m "feat(ui): environment badge on service-health group cards"
```

---

### Task 10: Docs and changelog

**Files:**
- Modify: `deploy/helm/README.md`
- Modify: `CHANGELOG.md`
- Modify: `design/2026-07-18-service-health-groups.md`
- Modify: `design/README.md`

- [ ] **Step 1: Document the declared contract**

Append to `deploy/helm/README.md`, after the collection-matrix section:

```markdown
## Declared service metadata

A service can group and tier itself with three optional OTLP resource
attributes — no hub config needed:

| Attribute | Meaning |
|---|---|
| `service.namespace` | logical domain; becomes the group name (spans k8s namespaces) |
| `deployment.environment.name` | environment; splits a domain into per-env groups |
| `avuru.tier` | criticality `T0`–`T3` |

Declaring nothing keeps the zero-config behavior: services auto-group by
Kubernetes namespace at `serviceGroups.defaultTier`.

Tier precedence, most specific first:
`serviceGroups.tierOverrides[<service>]` → a matched `serviceGroups.groups`
entry → the declared `avuru.tier` → `serviceGroups.defaultTier`.

An invalid declared tier never fails the hub: the service falls back to the
default tier and the `/api/v1/health/groups` response carries a warning.
```

- [ ] **Step 2: Record the behavior change**

Add to the Unreleased section of `CHANGELOG.md`:

```markdown
### Added
- Service health: services can declare their own domain (`service.namespace`),
  environment (`deployment.environment.name`), and criticality (`avuru.tier`)
  as resource attributes. Group identity becomes (domain, environment).
- `serviceGroups.tierOverrides` — per-service operator tier, winning over both
  a declared tier and a matched group's tier.

### Changed
- Alerting rules and green budgets that name a group now match that group in
  **every** environment. Narrow with `selector.environments` on a rule. Rules
  are unaffected until services begin declaring `deployment.environment.name`.
```

- [ ] **Step 3: Add the supersession note**

Per `design/README.md` ("supersede rather than rewrite"), add directly under the front-matter of `design/2026-07-18-service-health-groups.md`:

```markdown
> **Amended by [AEP 2026-07-28](2026-07-28-declared-service-metadata.md)**:
> services may declare their own domain, environment, and tier, and group
> identity became the pair (domain, environment). The hybrid auto+config model
> below is unchanged for installs that declare nothing.
```

- [ ] **Step 4: Flip the AEP to Accepted**

In `design/2026-07-28-declared-service-metadata.md`, change `- **Status:** Draft` to `- **Status:** Accepted`, and update the same row in the `design/README.md` index table from `Draft` to `Accepted`.

- [ ] **Step 5: Full verification**

Run: `make check && make helm-check`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add deploy/helm/README.md CHANGELOG.md design/2026-07-18-service-health-groups.md design/2026-07-28-declared-service-metadata.md design/README.md
git commit -m "docs: declared service metadata contract, changelog, AEP acceptance"
```

---

## Deferred to a follow-up

- **e2e coverage** (`e2e/`, seeded two-environment stack asserting two groups in
  two lanes). The seed fixtures in `deploy/compose/seed/fixtures` need declared
  attributes added, which touches the demo stack — worth its own change so a
  fixture regression cannot be confused with a health-logic regression.
- **UI warnings affordance.** Task 6 ships `warnings` on the API and Task 9
  types it, but no component renders it. Wire it into the health screen header
  once there is a real declaration to show.
