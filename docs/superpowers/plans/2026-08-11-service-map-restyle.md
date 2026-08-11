# Service Map Restyle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the service map diagnostic — health-status rings read from the hub's own rollup, real per-edge client-side latency, hover-focus that reveals per-edge rpm/latency, and URL-persisted filters — on both the Service Map screen and the Dashboard's compact map.

**Architecture:** The map stays cytoscape + fcose. The hub's existing `ServiceEdges` self-join gains one `quantiles()` aggregate for real per-edge latency. The UI reads per-service health from `/api/v1/health/groups` instead of re-deriving thresholds, moves the carbon lens off the node border (freeing it for the status ring) onto a halo, and splits the 317-line `service-map.tsx` into focused modules.

**Tech Stack:** Go 1.23 + ClickHouse (hub), Next.js static export + TypeScript strict + TanStack Query + cytoscape/fcose + Tailwind v4/daisyUI (ui), Playwright (e2e).

**Spec:** [`docs/superpowers/specs/2026-08-10-service-map-restyle-design.md`](../specs/2026-08-10-service-map-restyle-design.md)

**Branch:** `feature/service-map-restyle` (worktree `.claude/worktrees/service-map-restyle`). All paths below are repo-relative — run every command from the worktree root.

---

## House rules that apply to every commit

- **No `Co-Authored-By` trailer.** AI_POLICY.md forbids it in this repo.
- **Before pushing Go:** `cd hub && golangci-lint run`. Build and vet are not enough.
- Conventional-commit subjects (`feat(ui):`, `fix(hub):`, `test(hub):`, `docs:`).
- The UI has **no unit-test runner** (no vitest, no jest — `ui/package.json` has only Playwright). UI verification is `npm run lint`, `npm run build`, and the Playwright e2e written in Task 5 *before* the UI code that satisfies it. Do not add a unit runner; it is out of scope.

---

## File Structure

**Hub — modified**

| File | Responsibility after this change |
|---|---|
| `hub/internal/storage/store.go` | `ServiceEdge` carries `P50`/`P95` client-side latency |
| `hub/internal/storage/clickhouse/services.go` | `ServiceEdges` selects and scans the quantiles |
| `hub/internal/storage/clickhouse/status_integration_test.go` | asserts the quantiles come back from real ClickHouse |
| `hub/internal/api/dto.go` | `serviceEdgeDTO` exposes `p50Ms`/`p95Ms` (omitempty) |
| `hub/internal/api/router_test.go` | guards latency through merge → DTO → JSON |

Two files the spec listed need **no** change, so don't go looking: `storagetest/fake.go` returns `f.Edges` as whole `storage.ServiceEdge` values, so it carries the new fields for free, and `mergeEdges` in `traces.go` copies the trace edge wholesale (`merged[i] = e`) and only overwrites `Bytes` — the quantiles already survive. Task 2 writes the test that locks that in.

**UI — created**

| File | Responsibility |
|---|---|
| `ui/src/hooks/use-service-health-status.ts` | flattens `/api/v1/health/groups` into `Map<service, ServiceHealth>` |
| `ui/src/lib/map-filter.ts` | pure `(services, edges, filters, health) → subset` |
| `ui/src/components/service-map/graph-style.ts` | theme tokens + the cytoscape stylesheet (rings, halo, focus/fade) |
| `ui/src/components/service-map/graph-elements.ts` | services + edges + health → cytoscape elements, labels, tooltips |
| `ui/src/components/service-map/graph-focus.ts` | applies `.focus`/`.related`/`.faded` on hover |
| `ui/src/components/service-map/map-toolbar.tsx` | search, problems-only, group select, zoom/fit/re-layout |
| `ui/src/components/service-map/map-legend.tsx` | the legend row |
| `ui/e2e/service-map.spec.ts` | e2e for legend, filters, controls |

**UI — modified**

| File | Change |
|---|---|
| `ui/src/lib/api-types.ts` | `ServiceEdge` gains optional `p50Ms`/`p95Ms` |
| `ui/src/hooks/use-health-data.ts` | `useHealthGroups` gains an `enabled` flag |
| `ui/src/components/service-map/service-map.tsx` | shrinks to shell + cytoscape lifecycle |
| `ui/src/components/service-map/service-map-screen.tsx` | composes toolbar + legend + filtered graph |
| `ui/src/components/dashboard/topology-card.tsx` | reads the time range and health itself, so `dashboard-screen.tsx` keeps its props |
| `ui/e2e/green.spec.ts` | caption string changed by the halo move |

---

## Two amendments to the spec

Both are decided; **Task 0 records them in the spec file** so the spec and the code do not disagree.

1. **Direction on focused edges: a mid-line arrowhead, not an animated dash.** The spec called for animating `line-dash-offset`. That requires `line-style: dashed`, which is already the encoding for *network-unhealthy* edges (`edge[health > 0]`) — making every focused edge dashed would muddy a live signal. A `mid-target-arrow-shape` reads direction just as clearly, costs no timers, and leaves dashed meaning exactly one thing.

2. **Module gating via TanStack Query's `enabled`, not a child component.** The spec proposed the `summary-band.tsx` child-component pattern to avoid a conditional hook. `useQuery({ enabled: false })` is the same guard with less structure: the request never fires when the `service-health` module is off, so nothing 404s, and the screen stays one file.

---

## Task 0: Record the amendments in the spec

**Files:**
- Modify: `docs/superpowers/specs/2026-08-10-service-map-restyle-design.md`

- [ ] **Step 1: Amend the hover-focus bullet**

In section `### 4. Hover-focus`, replace this bullet:

```markdown
- its edges thicken and animate their dash offset along the direction of the
  call, so direction reads as motion rather than only as an arrowhead;
```

with:

```markdown
- its edges thicken and gain a mid-line arrowhead, so direction reads at a
  glance rather than only from the endpoint. (Amended 2026-08-11: the original
  design animated `line-dash-offset`, which needs `line-style: dashed` — already
  the encoding for network-unhealthy edges. A mid-line arrow keeps dashed
  meaning exactly one thing.)
```

- [ ] **Step 2: Amend the module-gating paragraph**

In section `### 2. Node status — read the hub's rollup`, replace:

```markdown
**Module gating.** `/api/v1/health/groups` 404s when the `service-health` module
is off, and hooks cannot be called conditionally. The query therefore lives in a
child component that only mounts when the module is enabled — the pattern
established by
[`summary-band.tsx`](../../../ui/src/components/dashboard/summary-band.tsx).
```

with:

```markdown
**Module gating.** `/api/v1/health/groups` 404s when the `service-health` module
is off. `useHealthGroups` therefore gains an `enabled` flag passed through to
TanStack Query, so the request simply never fires on an install without the
module. (Amended 2026-08-11: the original design used the child-component
pattern from
[`summary-band.tsx`](../../../ui/src/components/dashboard/summary-band.tsx) to
dodge a conditional hook; `enabled` is the same guard with less structure.)
```

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-08-10-service-map-restyle-design.md
git commit -m "docs(design): amend service map spec - mid-line arrows, enabled-flag gating"
```

---

## Task 1: Per-edge latency in the storage layer

The `ServiceEdges` self-join already produces one row per (client span, server span) pair grouped by `(src, dst)`. Adding quantiles is one more aggregate over rows the query already scans — no new join.

**Files:**
- Modify: `hub/internal/storage/store.go:47-54` (the `ServiceEdge` struct)
- Modify: `hub/internal/storage/clickhouse/services.go:57-96` (`ServiceEdges`)
- Test: `hub/internal/storage/clickhouse/status_integration_test.go:199-211`

- [ ] **Step 1: Write the failing assertion**

In `status_integration_test.go`, the `ServiceEdges` subtest ends with the field checks. Every seeded span in that file is inserted with `uint64(time.Millisecond)` as its Duration, so the single `web → api` edge must report a 1ms client-side p95. Replace the subtest body's tail:

```go
		e := edges[0]
		if e.Source != "web" || e.Target != "api" || e.Count != 1 || e.ErrorCount != 1 {
			t.Errorf("edge wrong: %+v", e)
		}
```

with:

```go
		e := edges[0]
		if e.Source != "web" || e.Target != "api" || e.Count != 1 || e.ErrorCount != 1 {
			t.Errorf("edge wrong: %+v", e)
		}
		// Client-side latency for the call path: every fixture span is inserted
		// with Duration = 1ms, so both quantiles over the single client span
		// ("CALL api") must come back as exactly 1ms.
		if e.P50 != time.Millisecond || e.P95 != time.Millisecond {
			t.Errorf("edge latency = p50 %v / p95 %v, want 1ms/1ms", e.P50, e.P95)
		}
```

- [ ] **Step 2: Run it to confirm it fails**

```bash
cd hub && TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./internal/storage/clickhouse/ -run TestEffectiveStatusIntegration 2>&1 | tail -20
```

Expected: a **compile** failure — `e.P50 undefined (type storage.ServiceEdge has no field or method P50)`. That is the correct red state; the fields do not exist yet.

`TESTCONTAINERS_RYUK_DISABLED=true` is required on this machine (colima). If Docker is unavailable, the integration test is skipped — in that case note it and rely on Task 2's unit tests, and say so rather than claiming the SQL is verified.

- [ ] **Step 3: Add the fields to the storage type**

In `hub/internal/storage/store.go`, extend `ServiceEdge` (keep the existing doc comment above it):

```go
type ServiceEdge struct {
	Source     string // caller service (edge tail)
	Target     string // callee service (edge head)
	Count      uint64 // trace call volume
	ErrorCount uint64 // trace errored-call volume
	Bytes      uint64 // network flow bytes (flow-derived edges)
	Provenance string // "trace", "flow", or "both"
	// P50/P95 are CLIENT-side latency for this call path — the caller's Client
	// span duration, so the edge reports what the CALLER experienced (network +
	// queueing + the callee's work). Deliberately not the server span duration,
	// which would just repeat the callee node's own p95 and could not reveal a
	// single slow path into an otherwise healthy service. Zero on flow-derived
	// edges, which have no span to measure.
	P50 time.Duration
	P95 time.Duration
}
```

- [ ] **Step 4: Select and scan the quantiles**

In `hub/internal/storage/clickhouse/services.go`, `ServiceEdges`. Change the SELECT list from:

```go
	query := `
SELECT
    client.ServiceName                   AS src,
    server.ServiceName                   AS dst,
    count()                              AS calls,
    countIf(` + errorSpanExpr("server.") + `) AS errors
FROM otel_traces AS server
```

to:

```go
	query := `
SELECT
    client.ServiceName                   AS src,
    server.ServiceName                   AS dst,
    count()                              AS calls,
    countIf(` + errorSpanExpr("server.") + `) AS errors,
    quantiles(0.5, 0.95)(toFloat64(client.Duration)) AS qs
FROM otel_traces AS server
```

and change the scan loop from:

```go
	var out []storage.ServiceEdge
	for rows.Next() {
		var e storage.ServiceEdge
		if err := rows.Scan(&e.Source, &e.Target, &e.Count, &e.ErrorCount); err != nil {
			return nil, fmt.Errorf("scanning edge row: %w", err)
		}
		out = append(out, e)
	}
```

to:

```go
	var out []storage.ServiceEdge
	for rows.Next() {
		var (
			e     storage.ServiceEdge
			quant []float64
		)
		if err := rows.Scan(&e.Source, &e.Target, &e.Count, &e.ErrorCount, &quant); err != nil {
			return nil, fmt.Errorf("scanning edge row: %w", err)
		}
		// nsQuantiles returns three; this query asks for two, and the third
		// comes back zero — the edge has no p99.
		e.P50, e.P95, _ = nsQuantiles(quant)
		out = append(out, e)
	}
```

- [ ] **Step 5: Run the test to verify it passes**

```bash
cd hub && TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./internal/storage/clickhouse/ -run TestEffectiveStatusIntegration 2>&1 | tail -20
```

Expected: `PASS` (or `ok ...`). If it reports `--- FAIL: .../ServiceEdges`, print the actual durations before changing the expectation — a non-1ms value means the query is measuring the wrong span.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/storage/store.go hub/internal/storage/clickhouse/services.go hub/internal/storage/clickhouse/status_integration_test.go
git commit -m "feat(hub): per-edge client-side latency on the service map

The map's edges carried call volume and errors but no latency, so a slow
call path into an otherwise healthy service was invisible. ServiceEdges
already self-joins client and server spans and groups by (src, dst), so
p50/p95 is one more aggregate over rows the query already scans.

Client span duration, not server: it reports what the CALLER experienced
(network + queueing + callee work), which is a different number from the
callee node's own p95 and the one that isolates a bad dependency."
```

---

## Task 2: Latency through merge and out the API

`mergeEdges` copies the whole trace edge (`merged[i] = e`) and only overwrites `Bytes` on a flow match, so the quantiles already survive — but nothing guards that, and a future edit could drop them. Write the guard, then expose the fields.

**Files:**
- Modify: `hub/internal/api/dto.go:30-40` (struct), `:153-163` (mapper)
- Test: `hub/internal/api/router_test.go`

- [ ] **Step 1: Write the failing merge test**

In `router_test.go`, `TestMergeEdges` is table-driven. Add `p95` to the `want` struct, populate it in the existing cases, add a new case, and assert it. Replace the `want` struct and the `tests` slice header:

```go
	type want struct {
		provenance string
		count      uint64
		bytesPos   bool
	}
```

with:

```go
	type want struct {
		provenance string
		count      uint64
		bytesPos   bool
		p95        time.Duration
	}
```

Add this case to the `tests` slice, after the "shared edge becomes both" case:

```go
		{
			name: "merging a flow edge keeps the trace edge's latency",
			trace: []storage.ServiceEdge{
				{Source: "A", Target: "B", Count: 3, P50: 10 * time.Millisecond, P95: 40 * time.Millisecond},
			},
			flow: []storage.ServiceEdge{{Source: "A", Target: "B", Bytes: 1024}},
			expect: map[string]want{
				"A->B": {provenance: "both", count: 3, bytesPos: true, p95: 40 * time.Millisecond},
			},
		},
```

Then, inside the `for _, e := range merged` loop, after the existing checks, add:

```go
				if e.P95 != w.p95 {
					t.Errorf("%s p95 = %v, want %v", k, e.P95, w.p95)
				}
```

Ensure `router_test.go` imports `"time"` (add it to the import block if absent).

- [ ] **Step 2: Write the failing DTO test**

Add this test to `router_test.go`. It proves latency reaches the JSON, and that a flow-only edge omits the fields entirely rather than reporting a misleading `0`:

```go
// TestServiceMapEdgeLatency proves per-edge client-side latency survives the
// store → DTO → JSON path, and that a flow-derived edge (no client span to
// measure) OMITS the fields rather than reporting a misleading 0ms.
func TestServiceMapEdgeLatency(t *testing.T) {
	fake := &storagetest.Fake{
		Services: []storage.ServiceStats{{Name: "A", SpanCount: 5}},
		Edges: []storage.ServiceEdge{
			{Source: "A", Target: "B", Count: 3, P50: 10 * time.Millisecond, P95: 40 * time.Millisecond},
		},
		NetEdges: []storage.ServiceEdge{{Source: "C", Target: "D", Bytes: 2048}},
	}
	mux := newMux(fake)

	rec := get(t, mux, "/api/v1/service-map")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	// Decode loosely so an ABSENT key is distinguishable from a zero value.
	var raw struct {
		Edges []map[string]any `json:"edges"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byPair := map[string]map[string]any{}
	for _, e := range raw.Edges {
		byPair[e["source"].(string)+"->"+e["target"].(string)] = e
	}
	ab, ok := byPair["A->B"]
	if !ok {
		t.Fatalf("A->B missing: %+v", raw.Edges)
	}
	if ab["p95Ms"] != float64(40) || ab["p50Ms"] != float64(10) {
		t.Errorf("A->B latency = p50 %v / p95 %v, want 10/40", ab["p50Ms"], ab["p95Ms"])
	}
	cd, ok := byPair["C->D"]
	if !ok {
		t.Fatalf("C->D missing: %+v", raw.Edges)
	}
	if _, present := cd["p95Ms"]; present {
		t.Errorf("flow-only edge should omit p95Ms, got %+v", cd)
	}
}
```

- [ ] **Step 3: Run both tests to verify they fail**

```bash
cd hub && go test ./internal/api/ -run 'TestMergeEdges|TestServiceMapEdgeLatency' 2>&1 | tail -20
```

Expected: `TestServiceMapEdgeLatency` fails with `A->B latency = p50 <nil> / p95 <nil>, want 10/40` (the DTO does not carry the fields yet). `TestMergeEdges` should already pass — `mergeEdges` copies the struct wholesale — which is exactly what the new case now locks in.

- [ ] **Step 4: Expose the fields on the DTO**

In `hub/internal/api/dto.go`, extend `serviceEdgeDTO`:

```go
type serviceEdgeDTO struct {
	Source            string  `json:"source"`
	Target            string  `json:"target"`
	Calls             uint64  `json:"calls"`
	ErrorCount        uint64  `json:"errorCount"`
	ErrorRate         float64 `json:"errorRate"`
	Bytes             uint64  `json:"bytes,omitempty"`             // network flow bytes (flow/both edges)
	Provenance        string  `json:"provenance"`                  // "trace", "flow", or "both"
	RTTMs             float64 `json:"rttMs,omitempty"`             // OBI TCP RTT p95 (network-health edges)
	FailedConnections uint64  `json:"failedConnections,omitempty"` // OBI failed/reset TCP connections
	// Client-side latency for this call path. omitempty on purpose: a
	// flow-derived edge has no span to measure, and 0ms would read as "instant"
	// rather than "not measured".
	P50Ms float64 `json:"p50Ms,omitempty"`
	P95Ms float64 `json:"p95Ms,omitempty"`
}
```

and the mapper:

```go
func toServiceEdgeDTO(e storage.ServiceEdge) serviceEdgeDTO {
	return serviceEdgeDTO{
		Source:     e.Source,
		Target:     e.Target,
		Calls:      e.Count,
		ErrorCount: e.ErrorCount,
		ErrorRate:  ratio(e.ErrorCount, e.Count),
		Bytes:      e.Bytes,
		Provenance: e.Provenance,
		P50Ms:      ms(e.P50),
		P95Ms:      ms(e.P95),
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
cd hub && go test ./internal/api/ 2>&1 | tail -10
```

Expected: `ok  github.com/avuru/avuru-obs/hub/internal/api`.

- [ ] **Step 6: Lint and commit**

```bash
cd hub && golangci-lint run && cd ..
git add hub/internal/api/dto.go hub/internal/api/router_test.go
git commit -m "feat(hub): expose per-edge p50/p95 on the service-map API

omitempty on both: a flow-derived edge has no client span to measure, and
0ms would read as instant rather than not-measured. Locks the merge path
too - mergeEdges copies the trace edge wholesale, so quantiles survive a
flow merge, and now a test says so."
```

---

## Task 3: UI data layer — types, health status, filter

Pure data plumbing, no rendering. Verified by the TypeScript compiler (`strict`) and lint; Playwright covers the behavior from Task 5 onward.

**Files:**
- Modify: `ui/src/lib/api-types.ts:45-55`
- Modify: `ui/src/hooks/use-health-data.ts`
- Create: `ui/src/hooks/use-service-health-status.ts`
- Create: `ui/src/lib/map-filter.ts`

- [ ] **Step 1: Mirror the new API fields**

In `ui/src/lib/api-types.ts`, extend `ServiceEdge`:

```ts
export interface ServiceEdge {
  source: string;
  target: string;
  calls: number;
  errorCount: number;
  errorRate: number;
  bytes?: number; // network flow bytes (flow/both edges)
  provenance?: string; // "trace" | "flow" | "both"
  rttMs?: number; // OBI TCP RTT p95 (network-health edges)
  failedConnections?: number; // OBI failed/reset TCP connections
  // Client-side latency for this call path — what the CALLER experienced
  // (network + queueing + callee work). Absent on flow-derived edges, which
  // have no span to measure: treat absent as "not measured", never as 0.
  p50Ms?: number;
  p95Ms?: number;
}
```

- [ ] **Step 2: Let the health query be disabled**

`/api/v1/health/groups` 404s when the `service-health` module is off. In `ui/src/hooks/use-health-data.ts`, replace the hook with:

```ts
// `enabled` gates the request for callers that render on installs WITHOUT the
// service-health module (the service map). The endpoint 404s there, so the
// query must never fire rather than fail loudly on every map load.
export function useHealthGroups(time: TimeParams, includeAux: boolean, enabled = true) {
  const { project } = useProject();
  return useQuery({
    queryKey: queryKeys.healthGroups(project, time, includeAux),
    queryFn: () =>
      apiGet<HealthGroupsResponse>(
        "/api/v1/health/groups",
        { ...time, includeAux: includeAux ? "true" : undefined },
        { project },
      ),
    enabled,
  });
}
```

The default keeps every existing caller (`summary-band.tsx`, the health screen) unchanged.

- [ ] **Step 3: Create the per-service health hook**

Create `ui/src/hooks/use-service-health-status.ts`:

```ts
"use client";

import { useMemo } from "react";
import { useHealthGroups } from "@/hooks/use-health-data";
import type { TimeParams } from "@/lib/query-keys";
import type { HealthGroup, HealthStatus } from "@/lib/api-types";

export interface ServiceHealth {
  status: HealthStatus;
  reason: string;
  group: string;
  tier: string;
}

// Severity order for the worst-of pick below. Mirrors hub/internal/health's
// ordering, where idle/unknown sit BELOW healthy so a quiet service never makes
// anything look worse. This is ordering only — the thresholds that produce a
// status stay in the hub, where they are configurable per group.
const SEVERITY: Record<string, number> = { healthy: 1, degraded: 2, down: 3 };
const severity = (s: string) => SEVERITY[s] ?? 0;

// Per-service health for the map's status rings, flattened from the group
// rollup the Service Health screen already reads. effectiveStatus, not
// baseStatus: a service dragged down by a dependency must read the same on the
// map as it does on the health board.
//
// `enabled` is false on an install without the service-health module — the
// endpoint does not exist there, and the map falls back to error presence.
export function useServiceHealthStatus(
  time: TimeParams,
  includeAux: boolean,
  enabled: boolean,
) {
  const { data, isLoading } = useHealthGroups(time, includeAux, enabled);

  const byService = useMemo(() => {
    const m = new Map<string, ServiceHealth>();
    for (const g of data?.groups ?? []) {
      for (const mem of g.members) {
        // A service can belong to more than one group; the worst status wins so
        // the ring never under-reports.
        const prev = m.get(mem.service);
        if (prev && severity(prev.status) >= severity(mem.effectiveStatus)) continue;
        m.set(mem.service, {
          status: mem.effectiveStatus,
          reason: mem.reason,
          group: g.name,
          tier: g.tier,
        });
      }
    }
    return m;
  }, [data]);

  const groups: HealthGroup[] = useMemo(() => data?.groups ?? [], [data]);

  return { byService, groups, isLoading };
}
```

- [ ] **Step 4: Create the pure filter**

Create `ui/src/lib/map-filter.ts`:

```ts
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";

export interface MapFilters {
  /** Case-insensitive substring on the service name. */
  q?: string;
  /** Keep only degraded + down. */
  problemsOnly?: boolean;
  /** Keep only members of this service group. */
  group?: string;
}

export function hasActiveFilter(f: MapFilters): boolean {
  return Boolean(f.q?.trim() || f.problemsOnly || f.group);
}

// Narrows the graph to the services a filter keeps, then drops every edge with
// an endpoint that went away — a dangling edge would draw to nothing.
//
// Kept pure and cytoscape-free on purpose: this is the logic that decides what
// the user sees, and it should be readable without knowing the graph library.
export function filterMap(
  services: ServiceStats[],
  edges: ServiceEdge[],
  filters: MapFilters,
  health: Map<string, ServiceHealth>,
): { services: ServiceStats[]; edges: ServiceEdge[] } {
  if (!hasActiveFilter(filters)) return { services, edges };

  const q = filters.q?.trim().toLowerCase();
  const kept = services.filter((s) => {
    if (q && !s.name.toLowerCase().includes(q)) return false;
    const h = health.get(s.name);
    // A service with no health entry is unknown, not healthy — so it is never
    // what "problems only" keeps, and it belongs to no group.
    if (filters.problemsOnly && h?.status !== "degraded" && h?.status !== "down") return false;
    if (filters.group && h?.group !== filters.group) return false;
    return true;
  });

  const names = new Set(kept.map((s) => s.name));
  return {
    services: kept,
    edges: edges.filter((e) => names.has(e.source) && names.has(e.target)),
  };
}
```

- [ ] **Step 5: Typecheck and lint**

```bash
cd ui && npm run lint && npx tsc --noEmit
```

Expected: no errors. (`npx tsc --noEmit` is the fast typecheck; the full `npm run build` runs in Task 8.)

- [ ] **Step 6: Commit**

```bash
git add ui/src/lib/api-types.ts ui/src/lib/map-filter.ts ui/src/hooks/use-health-data.ts ui/src/hooks/use-service-health-status.ts
git commit -m "feat(ui): per-service health and a pure map filter

The map is about to draw status rings; the verdict comes from the hub's
own rollup (effectiveStatus, so a dependency-dragged service reads the
same as on the health board) rather than a second set of thresholds in
the browser. useHealthGroups gains an enabled flag because the endpoint
does not exist on an install without the service-health module."
```

---

## Task 4: The graph — rings, halo, focus

The restyle itself. `service-map.tsx` is 317 lines and this roughly doubles it, past the ~300-line house limit in `agent_docs/ui_patterns.md`, so it splits first.

**Files:**
- Create: `ui/src/components/service-map/graph-style.ts`
- Create: `ui/src/components/service-map/graph-elements.ts`
- Create: `ui/src/components/service-map/graph-focus.ts`
- Modify: `ui/src/components/service-map/service-map.tsx` (rewrite)

- [ ] **Step 1: Create the stylesheet module**

Create `ui/src/components/service-map/graph-style.ts`:

```ts
import type { Core } from "cytoscape";

// Resolve Avuru Gold tokens from the live theme (daisyUI CSS vars) so the graph
// follows light/dark — never hardcode hex (agent_docs/ui_patterns.md).
export function themeColors() {
  const cs = getComputedStyle(document.documentElement);
  const v = (name: string, fallback: string) =>
    cs.getPropertyValue(name).trim() || fallback;
  return {
    primary: v("--color-primary", "#c9a96a"),
    error: v("--color-error", "#f87171"),
    warning: v("--color-warning", "#f59e0b"),
    success: v("--color-success", "#34d399"),
    surface: v("--color-base-200", "#0f1729"),
    base100: v("--color-base-100", "#0b1120"),
    text: v("--color-base-content", "#e8e5dc"),
    neutral: v("--color-neutral", "#33415580"),
  };
}

// Compact mode (the Dashboard's band 2) is the SAME graph at overview scale.
const scale = (compact: boolean) => ({
  node: compact ? "mapData(rate, 0, 10, 14, 40)" : "mapData(rate, 0, 10, 22, 64)",
  fontSize: compact ? 9 : 11,
  labelMargin: compact ? 3 : 5,
  edge: compact ? "mapData(calls, 0, 50, 0.8, 3.2)" : "mapData(calls, 0, 50, 1.2, 5)",
});

// applyStyle rebuilds the whole stylesheet. Channels, and why each one:
//
//   ring (border)  health status — ALWAYS on, so it must own a stable channel
//   fill           service identity (primary)
//   size           request rate
//   halo (underlay) gCO2e, carbon lens only — moved off the border in v0.5 W7
//                  so the status ring could have it; the lens reads as an
//                  overlay rather than a repaint
//   width          call volume
//   line color     plain / amber (network health) / red (trace errors)
//
// carbon=false must leave the graph byte-identical to a non-green install.
export function applyStyle(cy: Core, carbon = false, compact = false) {
  const c = themeColors();
  const s = scale(compact);

  const withNodes = cy
    .style()
    .resetToDefault()
    .selector("node")
    .style({
      "background-color": c.primary,
      label: "data(label)",
      color: c.text,
      "font-size": s.fontSize,
      "text-valign": "bottom",
      "text-margin-y": s.labelMargin,
      // Keep labels legible where they sit over edges/nodes.
      "text-background-color": c.base100,
      "text-background-opacity": 0.7,
      "text-background-padding": "2px",
      "text-background-shape": "roundrectangle",
      // wrap so the focus label's second line breaks on its \n.
      "text-wrap": "wrap",
      width: s.node,
      height: s.node,
      "border-width": 3,
      // Default ring: unknown/idle. Never green — an unmeasured service must
      // not read as healthy.
      //
      // These four colors are the daisyUI tokens that lib/health-status.ts's
      // `bg-success`/`bg-warning`/`bg-error`/`bg-base-content/30` resolve to.
      // Do NOT import statusDotClass here — it returns Tailwind class names,
      // which cytoscape cannot use; the shared thing is the token, not the code.
      "border-color": c.neutral,
      "transition-property": "opacity, border-width",
      "transition-duration": 120,
    })
    // Status rings. Colors follow lib/health-status.ts so the map and the
    // health board cannot disagree about what amber means.
    .selector('node[status = "healthy"]')
    .style({ "border-color": c.success })
    .selector('node[status = "degraded"]')
    .style({ "border-color": c.warning })
    .selector('node[status = "down"]')
    .style({ "border-color": c.error });

  // Carbon halo (low→high gCO2e). Applied after the ring selectors so a node
  // can read status (ring) and carbon (halo) at once. Only bucketed nodes carry
  // the `carbon` attribute, so nodes without energy get no halo.
  const halo = (color: string) => ({
    "underlay-color": color,
    "underlay-opacity": 0.25,
    "underlay-padding": compact ? 4 : 7,
  });
  const withCarbon = carbon
    ? withNodes
        .selector("node[carbon = 0]")
        .style(halo(c.success))
        .selector("node[carbon = 1]")
        .style(halo(c.warning))
        .selector("node[carbon = 2]")
        .style(halo(c.error))
    : withNodes;

  withCarbon
    .selector("edge")
    .style({
      width: s.edge,
      "line-color": c.neutral,
      "target-arrow-color": c.neutral,
      "target-arrow-shape": "triangle",
      "arrow-scale": 0.9,
      "curve-style": "bezier",
      opacity: 0.85,
      "transition-property": "opacity, width",
      "transition-duration": 120,
    })
    // Network-health amber (high RTT or failed connections) — before the error
    // selector so trace-error red always wins when an edge is both.
    .selector("edge[health > 0]")
    .style({ "line-color": c.warning, "target-arrow-color": c.warning, "line-style": "dashed" })
    .selector("edge[error > 0]")
    .style({ "line-color": c.error, "target-arrow-color": c.error, "line-style": "solid" })
    // ---- Hover focus ----
    // Everything outside the focused neighbourhood recedes.
    .selector(".faded")
    .style({ opacity: 0.18, "text-opacity": 0.18 })
    // The hovered node: thicker ring and the expanded two-line label.
    .selector("node.focus")
    .style({ "border-width": 5, label: "data(focusLabel)", "font-size": s.fontSize + 1 })
    // Its edges: thicker, fully opaque, labelled with rpm/latency, and carrying
    // a mid-line arrowhead so direction reads without following the line to its
    // end. NOT an animated dash — dashed already means "network-unhealthy".
    .selector("edge.related")
    .style({
      opacity: 1,
      width: compact ? 3 : 5,
      label: "data(focusLabel)",
      "font-size": s.fontSize,
      color: c.text,
      "text-background-color": c.base100,
      "text-background-opacity": 0.85,
      "text-background-padding": "2px",
      "text-background-shape": "roundrectangle",
      "text-rotation": "autorotate",
      "mid-target-arrow-shape": "triangle",
      "mid-target-arrow-color": c.neutral,
      "arrow-scale": 1.1,
    })
    .update();
}
```

- [ ] **Step 2: Create the elements module**

Create `ui/src/components/service-map/graph-elements.ts`:

```ts
import type { ElementDefinition } from "cytoscape";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";

// carbonBucket maps a node's gCO2e into the 3-step halo scale, relative to the
// heaviest node in view so the lens is meaningful at any absolute scale.
export function carbonBucket(gco2e: number, max: number): 0 | 1 | 2 {
  if (max <= 0) return 0;
  const r = gco2e / max;
  return r < 1 / 3 ? 0 : r < 2 / 3 ? 1 : 2;
}

// An edge is network-unhealthy when RTT p95 is high or any connections failed.
const RTT_UNHEALTHY_MS = 100;
function edgeUnhealthy(e: ServiceEdge): boolean {
  return (e.rttMs ?? 0) > RTT_UNHEALTHY_MS || (e.failedConnections ?? 0) > 0;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export function formatMs(v: number): string {
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${Math.round(v)}ms`;
}

export function formatRpm(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k rpm`;
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} rpm`;
}

// nodeEnergyTooltip is the hover text a node gains under the carbon overlay.
export function nodeEnergyTooltip(label: string, wh: number, gco2e: number): string {
  return `${label} · ${wh.toFixed(1)} Wh · ${gco2e.toFixed(2)} gCO2e`;
}

// edgeTooltip is the hover text for an edge: call volume, this path's latency,
// plus any network health OBI measured for the connection.
export function edgeTooltip(e: ServiceEdge, windowMinutes: number): string {
  const parts = [`${e.source} → ${e.target}`, formatRpm(e.calls / windowMinutes)];
  if (e.p95Ms) parts.push(`p95 ${formatMs(e.p95Ms)}`);
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% errors`);
  if (e.rttMs) parts.push(`RTT p95 ${e.rttMs.toFixed(0)}ms`);
  if (e.failedConnections) parts.push(`${e.failedConnections} failed conns`);
  if (e.bytes) parts.push(formatBytes(e.bytes));
  return parts.join(" · ");
}

// The label an edge reveals while its neighbourhood is focused. Kept short —
// it is drawn along the line. p95 is omitted when the edge is flow-derived and
// has no span to measure; showing 0ms there would be a lie.
function edgeFocusLabel(e: ServiceEdge, windowMinutes: number): string {
  const parts = [formatRpm(e.calls / windowMinutes)];
  if (e.p95Ms) parts.push(`p95 ${formatMs(e.p95Ms)}`);
  if ((e.errorRate ?? 0) > 0) parts.push(`${(e.errorRate * 100).toFixed(1)}% err`);
  if (e.rttMs) parts.push(`RTT ${e.rttMs.toFixed(0)}ms`);
  return parts.join(" · ");
}

export interface BuildOptions {
  services: ServiceStats[];
  edges: ServiceEdge[];
  health: Map<string, ServiceHealth>;
  windowMs: number;
  carbon: boolean;
}

// Builds the cytoscape element list. Every derived string lives here so the
// React shell stays a lifecycle wrapper and the stylesheet stays declarative.
export function buildElements({
  services,
  edges,
  health,
  windowMs,
  carbon,
}: BuildOptions): ElementDefinition[] {
  const windowMinutes = Math.max(windowMs / 60_000, 1 / 60);
  const names = new Set(services.map((s) => s.name));
  // Heaviest node in view anchors the relative carbon scale (green only).
  const maxGco2e = carbon ? Math.max(0, ...services.map((s) => s.gco2e ?? 0)) : 0;

  const nodes: ElementDefinition[] = services.map((s) => {
    const rpm = s.ratePerSec * 60;
    return {
      data: {
        id: s.name,
        label: s.name,
        // The ring's channel. Absent from the rollup → "unknown", which is the
        // neutral ring: an unmeasured service is never drawn as healthy.
        status: health.get(s.name)?.status ?? "unknown",
        focusLabel: `${s.name}\n${formatRpm(rpm)} · p95 ${formatMs(s.p95Ms)}`,
        error: s.errorRate,
        rate: s.ratePerSec,
        // Carbon fields are added ONLY under the overlay, so a non-green node
        // carries the exact same data as before.
        ...(carbon && s.gco2e !== undefined
          ? { wh: s.wh, gco2e: s.gco2e, carbon: carbonBucket(s.gco2e, maxGco2e) }
          : {}),
      },
    };
  });

  // Only edges between known nodes (a callee may have aged out of the window).
  const links: ElementDefinition[] = edges
    .filter((e) => names.has(e.source) && names.has(e.target) && e.source !== e.target)
    .map((e) => ({
      data: {
        id: `${e.source}->${e.target}`,
        source: e.source,
        target: e.target,
        calls: e.calls,
        error: e.errorRate,
        health: edgeUnhealthy(e) ? 1 : 0,
        focusLabel: edgeFocusLabel(e, windowMinutes),
        tooltip: edgeTooltip(e, windowMinutes),
      },
    }));

  return [...nodes, ...links];
}
```

- [ ] **Step 3: Create the focus module**

Create `ui/src/components/service-map/graph-focus.ts`:

```ts
import type { Core, NodeSingular } from "cytoscape";

const CLASSES = "focus related faded";

// Hover focus: the hovered node and its 1-hop neighbourhood stay lit, its edges
// thicken and reveal their rpm/latency labels, and everything else recedes.
// Classes only — the layout must NOT move, so nothing is added or removed.
export function focusNeighbourhood(cy: Core, node: NodeSingular) {
  const edges = node.connectedEdges();
  const keep = edges.connectedNodes().union(node);
  cy.elements().removeClass(CLASSES);
  cy.elements().difference(keep.union(edges)).addClass("faded");
  edges.addClass("related");
  node.addClass("focus");
}

export function clearFocus(cy: Core) {
  cy.elements().removeClass(CLASSES);
}
```

- [ ] **Step 4: Rewrite the graph component**

Replace the whole of `ui/src/components/service-map/service-map.tsx` with:

```tsx
"use client";

import { useEffect, useImperativeHandle, useRef } from "react";
import { useRouter } from "next/navigation";
import cytoscape, { type Core, type LayoutOptions, type NodeSingular } from "cytoscape";
import fcose from "cytoscape-fcose";
import { cn } from "@/lib/cn";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";
import type { ServiceHealth } from "@/hooks/use-service-health-status";
import { applyStyle } from "./graph-style";
import { buildElements, nodeEnergyTooltip } from "./graph-elements";
import { clearFocus, focusNeighbourhood } from "./graph-focus";

let layoutRegistered = false;
function ensureLayout() {
  if (!layoutRegistered) {
    cytoscape.use(fcose);
    layoutRegistered = true;
  }
}

// Label-aware fcose options: without nodeDimensionsIncludeLabels the simulation
// ignores label width and stacks the names on top of each other. The initial
// layout is deterministic (no animation, seeded positions); a re-layout
// randomizes the seed to escape a tangled local minimum, animated so the
// untangle reads as movement, not a flash.
const layoutOptions = (animate: boolean, compact = false) =>
  ({
    name: "fcose",
    quality: "proof",
    animate,
    randomize: animate,
    padding: compact ? 26 : 60,
    nodeDimensionsIncludeLabels: true,
    nodeSeparation: compact ? 95 : 170,
    idealEdgeLength: compact ? 95 : 170,
    nodeRepulsion: compact ? 5200 : 9000,
  }) as unknown as LayoutOptions;

export interface ServiceMapHandle {
  relayout: () => void;
  fit: () => void;
  zoomBy: (factor: number) => void;
}

const EMPTY_HEALTH: Map<string, ServiceHealth> = new Map();

// Service-map graph. Nodes = services (sized by request rate, ring = health
// status); edges = caller→callee call volume derived from trace spans. Hover a
// node to focus its neighbourhood and reveal per-edge rpm/latency; click a node
// to open its traces.
export function ServiceMap({
  services,
  edges,
  windowMs,
  health = EMPTY_HEALTH,
  handleRef,
  carbon = false,
  compact = false,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
  /** Window length, for turning edge call counts into rpm. */
  windowMs: number;
  /** Per-service health for the rings. Empty on an install without the module,
   *  which leaves every ring neutral. */
  health?: Map<string, ServiceHealth>;
  handleRef?: React.Ref<ServiceMapHandle>;
  // carbon (module green) turns on the gCO2e halo + node energy tooltip.
  // Default off: on a non-green install the map is byte-unchanged.
  carbon?: boolean;
  // compact renders the overview-scale variant for the Dashboard's band 2.
  compact?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const cyRef = useRef<Core | null>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const router = useRouter();

  useImperativeHandle(handleRef, () => ({
    relayout: () => cyRef.current?.layout(layoutOptions(true, compact)).run(),
    fit: () => cyRef.current?.fit(undefined, compact ? 26 : 60),
    zoomBy: (factor: number) => {
      const cy = cyRef.current;
      if (!cy) return;
      cy.zoom({
        level: cy.zoom() * factor,
        renderedPosition: { x: cy.width() / 2, y: cy.height() / 2 },
      });
    },
  }));

  useEffect(() => {
    if (!ref.current) return;
    ensureLayout();
    const cy = cytoscape({
      container: ref.current,
      elements: buildElements({ services, edges, health, windowMs, carbon }),
      layout: layoutOptions(false, compact),
      minZoom: 0.3,
      maxZoom: 2.5,
    });
    applyStyle(cy, carbon, compact);
    // Compact sits in a short card, so a wide estate hangs off the edges unless
    // it is fitted. Safe to call straight away: the initial layout runs with
    // animate:false, so positions are already final here.
    if (compact) cy.fit(undefined, 26);

    cy.on("tap", "node", (e) => {
      router.push(`/traces?service=${encodeURIComponent(e.target.id())}&tab=traces`);
    });

    const tip = tooltipRef.current;
    const showTip = (text: string, p?: { x: number; y: number }) => {
      if (!tip || !text) return;
      tip.textContent = text;
      if (p) {
        tip.style.left = `${p.x}px`;
        tip.style.top = `${p.y}px`;
      }
      tip.style.opacity = "1";
    };
    const hideTip = () => {
      if (tip) tip.style.opacity = "0";
    };

    // Edge hover tooltip: rpm, this path's p95, and any OBI network health.
    cy.on("mouseover", "edge", (evt) => {
      showTip(String(evt.target.data("tooltip") ?? ""), evt.renderedPosition);
    });
    cy.on("mouseout", "edge", hideTip);
    cy.on("pan zoom drag", hideTip);

    // Node hover drives the focus, and (under the carbon lens only) the energy
    // tooltip. Both live in one handler so they cannot fight over mouseover.
    cy.on("mouseover", "node", (evt) => {
      focusNeighbourhood(cy, evt.target as NodeSingular);
      if (!carbon) return;
      const d = evt.target.data();
      if (d.wh === undefined || d.gco2e === undefined) return;
      showTip(
        nodeEnergyTooltip(String(d.label), Number(d.wh), Number(d.gco2e)),
        evt.renderedPosition,
      );
    });
    cy.on("mouseout", "node", () => {
      clearFocus(cy);
      hideTip();
    });

    cyRef.current = cy;
    return () => {
      cy.destroy();
      cyRef.current = null;
    };
  }, [services, edges, health, windowMs, router, carbon, compact]);

  // Re-theme the graph when the user toggles light/dark.
  useEffect(() => {
    const obs = new MutationObserver(() => {
      if (cyRef.current) applyStyle(cyRef.current, carbon, compact);
    });
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => obs.disconnect();
  }, [carbon, compact]);

  return (
    <div className="relative">
      <div
        ref={ref}
        data-testid={compact ? "service-map-compact" : "service-map"}
        className={cn(
          // Compact sits inside the Dashboard's Card, which already draws the
          // surface — a second border there would read as a box in a box.
          "w-full rounded-xl",
          compact ? "h-75" : "h-[70vh] border border-neutral bg-base-200",
        )}
      />
      {/* Hover tooltip (edge detail, or node energy under the carbon lens).
          Positioned by the cytoscape handlers; pointer-events-none so it never
          eats hover. */}
      <div
        ref={tooltipRef}
        className="pointer-events-none absolute z-10 max-w-xs -translate-x-1/2 -translate-y-full rounded-md border border-neutral bg-base-100 px-2 py-1 text-xs text-base-content opacity-0 shadow-md transition-opacity"
        style={{ left: 0, top: 0 }}
      />
    </div>
  );
}
```

- [ ] **Step 5: Keep both call sites compiling**

`ServiceMap` now requires `windowMs`, which breaks its two callers. Every commit must build — Task 5 runs Playwright, and `make e2e-ui` builds the static export first, so a broken tree there would fail for the wrong reason. Make the minimal change now; Tasks 6 and 7 do the real wiring.

In `ui/src/components/service-map/service-map-screen.tsx`, add the import and the prop:

```tsx
import { useTimeRange } from "@/hooks/use-time-range";   // already imported
```

```tsx
      <ServiceMap
        services={services}
        edges={edges}
        windowMs={new Date(time.end).getTime() - new Date(time.start).getTime()}
        handleRef={mapRef}
        carbon={carbon}
      />
```

In `ui/src/components/dashboard/topology-card.tsx`, add `useTimeRange` and the same prop:

```tsx
import { useTimeRange } from "@/hooks/use-time-range";
```

```tsx
export function TopologyCard({
  services,
  edges,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
}) {
  const { time } = useTimeRange();
```

```tsx
        <ServiceMap
          services={services}
          edges={edges}
          windowMs={new Date(time.end).getTime() - new Date(time.start).getTime()}
          compact
        />
```

- [ ] **Step 6: Typecheck**

```bash
cd ui && npm run lint && npx tsc --noEmit
```

Expected: clean. Rings will be neutral everywhere until Task 6 passes `health` — that is correct at this commit, not a bug.

- [ ] **Step 7: Commit**

```bash
git add ui/src/components/service-map/
git commit -m "feat(ui): status rings, carbon halo and hover focus on the service map

Splits the 317-line component along the seams the restyle creates:
stylesheet, element building, focus behaviour, and a React shell that is
now just cytoscape lifecycle.

The ring reads the hub's health verdict, so it needed the border - which
the carbon lens owned. Carbon moves to an underlay halo: the always-on
channel keeps the stable one, and the lens reads as an overlay rather
than a repaint. Hover focuses a node's neighbourhood and reveals per-edge
rpm and this-path p95, with a mid-line arrow for direction (not an
animated dash - dashed already means network-unhealthy)."
```

Include both call sites in this commit (`git add ui/src/components/dashboard/topology-card.tsx` too) so the tree builds at this revision.

---

## Task 5: The failing e2e

Written before the toolbar exists, because the count line and the controls are the only user-facing contract Playwright can hold on a canvas.

Seeded fixtures (`deploy/compose/seed/fixtures`, rebased to now−5m by `tools/seed`): `traces_multiservice.json` gives exactly one cross-service call, `seed-gateway → seed-payments`, plus `seed-checkout` from `traces_checkout.json`. The default 15m range covers them.

**Files:**
- Create: `ui/e2e/service-map.spec.ts`

- [ ] **Step 1: Write the spec**

Create `ui/e2e/service-map.spec.ts`:

```ts
import { test, expect } from "@playwright/test";

// The restyled service map (v0.5 W7). Seeded fixtures give three services
// (seed-checkout, seed-gateway, seed-payments) and exactly one call edge,
// seed-gateway → seed-payments, rebased to now−5m so the default 15m range
// covers them.
//
// Honest limitation: cytoscape draws to a canvas, so rings, halos, focus and
// edge labels CANNOT be asserted from the DOM. The contract held here is the
// surrounding chrome — the legend, the controls, and the count line that every
// filter must move. The count line is the proof a filter reached the graph.
test.describe("service map (seeded data)", () => {
  test("explains its encoding in a legend", async ({ page }) => {
    await page.goto("/service-map");

    const legend = page.getByTestId("map-legend");
    await expect(legend).toBeVisible();
    await expect(legend.getByText("Healthy")).toBeVisible();
    await expect(legend.getByText("Degraded")).toBeVisible();
    await expect(legend.getByText("Down")).toBeVisible();
    await expect(legend.getByText(/size = rate/)).toBeVisible();
  });

  test("search narrows the graph and round-trips through the URL", async ({ page }) => {
    await page.goto("/service-map");

    const count = page.getByTestId("map-count");
    await expect(count).toContainText(/3 services · 1 call edges/);

    await page.getByRole("searchbox", { name: "Filter services" }).fill("gateway");
    await expect(count).toContainText(/1 services · 0 call edges/);
    await expect(count).toContainText(/filtered from 3/);
    await expect(page).toHaveURL(/q=gateway/);

    // The filter survives a reload — the URL is the truth, not component state.
    await page.reload();
    await expect(page.getByTestId("map-count")).toContainText(/1 services/);
  });

  test("problems-only keeps just the unhealthy services", async ({ page }) => {
    await page.goto("/service-map");

    // The seed is healthy, so "problems only" empties the graph. That is the
    // assertion: the filter reached the graph and the screen said so.
    await page.getByRole("checkbox", { name: "Problems only" }).check();
    await expect(page).toHaveURL(/problems=true/);
    await expect(page.getByTestId("map-count")).toContainText(/0 services/);
    await expect(page.getByText("No services match")).toBeVisible();

    await page.getByRole("checkbox", { name: "Problems only" }).uncheck();
    await expect(page.getByTestId("map-count")).toContainText(/3 services/);
  });

  test("offers zoom, fit and re-layout", async ({ page }) => {
    await page.goto("/service-map");

    // Canvas state is not observable, so this holds the affordance contract:
    // the controls exist, are reachable by name, and clicking them does not
    // break the screen.
    await page.getByRole("button", { name: "Zoom in" }).click();
    await page.getByRole("button", { name: "Zoom out" }).click();
    await page.getByRole("button", { name: "Fit to view" }).click();
    await page.getByRole("button", { name: "Re-run layout" }).click();
    await expect(page.getByTestId("service-map")).toBeVisible();
  });
});
```

- [ ] **Step 2: Run it to confirm it fails**

The compose stack must be up and seeded. Per the isolated-run rule, check what is already running first — `make e2e-ui` destroys the shared stack:

```bash
docker compose ls
```

If a shared `avuru-obs` stack is running, run the e2e under its own project name rather than tearing that one down. Otherwise:

```bash
make e2e-ui 2>&1 | tail -40
```

Expected: `service-map.spec.ts` fails — no `map-legend`, no `map-count`, no `Filter services` searchbox. Every other spec must still pass except the two `green.spec.ts` cases noted in Task 7.

- [ ] **Step 3: Commit the failing spec**

```bash
git add ui/e2e/service-map.spec.ts
git commit -m "test(ui): e2e for the restyled service map (currently failing)

Cytoscape draws to a canvas, so rings, focus and edge labels cannot be
asserted from the DOM. This holds the chrome instead: the legend, the
controls, and the count line every filter must move - which is the proof
a filter actually reached the graph."
```

---

## Task 6: Toolbar, legend, and the screen

**Files:**
- Create: `ui/src/components/service-map/map-legend.tsx`
- Create: `ui/src/components/service-map/map-toolbar.tsx`
- Modify: `ui/src/components/service-map/service-map-screen.tsx` (rewrite)

- [ ] **Step 1: Create the legend**

Create `ui/src/components/service-map/map-legend.tsx`:

```tsx
"use client";

// What the map's channels mean. Dense single row: the map is the content, and
// a legend that takes vertical space costs the thing it explains.
//
// `health` false = the service-health module is off, so there are no status
// rings to explain — the map falls back to marking error presence.
export function MapLegend({ health, carbon }: { health: boolean; carbon: boolean }) {
  return (
    <div
      data-testid="map-legend"
      className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-base-content/55"
    >
      {health ? (
        <span className="flex items-center gap-3">
          <span className="text-base-content/45">ring:</span>
          <Swatch className="border-success" label="Healthy" />
          <Swatch className="border-warning" label="Degraded" />
          <Swatch className="border-error" label="Down" />
          <Swatch className="border-base-content/30" label="Idle" />
        </span>
      ) : (
        <span>ring: red = errors in window</span>
      )}
      <span>size = rate</span>
      <span>width = calls</span>
      <span className="text-warning/80">amber dashed = network health</span>
      <span className="text-error/80">red = errors</span>
      {carbon && <span className="text-success/80">halo = gCO2e</span>}
      <span className="text-base-content/40">hover a node for its edges</span>
    </div>
  );
}

function Swatch({ className, label }: { className: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span className={`h-2.5 w-2.5 rounded-full border-2 ${className}`} />
      {label}
    </span>
  );
}
```

- [ ] **Step 2: Create the toolbar**

Create `ui/src/components/service-map/map-toolbar.tsx`:

```tsx
"use client";

import { Maximize2, Shuffle, ZoomIn, ZoomOut } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { HealthGroup } from "@/lib/api-types";
import type { MapFilters } from "@/lib/map-filter";

// Filters + view controls. Every filter is URL state (a map URL must be
// pasteable into Slack — agent_docs/ui_patterns.md), so this component owns no
// state: it reads the current filters and reports changes upward.
//
// The status and group filters exist ONLY when the service-health module is on,
// because both read the rollup that module produces.
export function MapToolbar({
  filters,
  groups,
  healthEnabled,
  canCarbon,
  carbon,
  includeAux,
  onFilters,
  onCarbon,
  onIncludeAux,
  onZoomIn,
  onZoomOut,
  onFit,
  onRelayout,
}: {
  filters: MapFilters;
  groups: HealthGroup[];
  healthEnabled: boolean;
  canCarbon: boolean;
  carbon: boolean;
  includeAux: boolean;
  onFilters: (next: MapFilters) => void;
  onCarbon: (on: boolean) => void;
  onIncludeAux: (on: boolean) => void;
  onZoomIn: () => void;
  onZoomOut: () => void;
  onFit: () => void;
  onRelayout: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
      <input
        type="search"
        aria-label="Filter services"
        placeholder="Filter services…"
        value={filters.q ?? ""}
        onChange={(e) => onFilters({ ...filters, q: e.target.value })}
        className="h-7 w-44 rounded-lg border border-neutral bg-base-100 px-2 text-xs text-base-content placeholder:text-base-content/40 focus-visible:outline-2 focus-visible:outline-primary"
      />

      {healthEnabled && (
        <>
          <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
            <input
              type="checkbox"
              checked={Boolean(filters.problemsOnly)}
              onChange={(e) => onFilters({ ...filters, problemsOnly: e.target.checked })}
              className="accent-warning"
            />
            Problems only
          </label>
          <select
            aria-label="Filter by group"
            value={filters.group ?? ""}
            onChange={(e) => onFilters({ ...filters, group: e.target.value || undefined })}
            className="h-7 rounded-lg border border-neutral bg-base-100 px-2 text-xs text-base-content"
          >
            <option value="">All groups</option>
            {groups.map((g) => (
              <option key={g.name} value={g.name}>
                {g.name}
              </option>
            ))}
          </select>
        </>
      )}

      {canCarbon && (
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
          <input
            type="checkbox"
            checked={carbon}
            onChange={(e) => onCarbon(e.target.checked)}
            className="accent-success"
          />
          Carbon
        </label>
      )}

      <label className="flex cursor-pointer items-center gap-1.5 text-xs text-base-content/70">
        <input
          type="checkbox"
          checked={includeAux}
          onChange={(e) => onIncludeAux(e.target.checked)}
          className="accent-primary"
        />
        Show auxiliary requests
      </label>

      <div className="ml-auto flex items-center gap-1">
        <Button variant="ghost" size="icon" aria-label="Zoom in" onClick={onZoomIn}>
          <ZoomIn className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Zoom out" onClick={onZoomOut}>
          <ZoomOut className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="icon" aria-label="Fit to view" onClick={onFit}>
          <Maximize2 className="h-3.5 w-3.5" />
        </Button>
        <Button variant="ghost" size="sm" aria-label="Re-run layout" onClick={onRelayout}>
          <Shuffle className="h-3.5 w-3.5" /> Re-layout
        </Button>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Rewrite the screen**

Replace the whole of `ui/src/components/service-map/service-map-screen.tsx` with:

```tsx
"use client";

import { useMemo, useRef } from "react";
import { Map as MapIcon } from "lucide-react";
import { useTimeRange } from "@/hooks/use-time-range";
import { useURLState } from "@/hooks/use-url-state";
import { useServiceMapData } from "@/hooks/use-service-map-data";
import { useServiceHealthStatus } from "@/hooks/use-service-health-status";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import { CenteredSpinner } from "@/components/ui/spinner";
import { EmptyState } from "@/components/ui/empty-state";
import { filterMap, hasActiveFilter, type MapFilters } from "@/lib/map-filter";
import { ServiceMap, type ServiceMapHandle } from "./service-map";
import { MapToolbar } from "./map-toolbar";
import { MapLegend } from "./map-legend";

export function ServiceMapScreen() {
  const { time } = useTimeRange();
  const { get, setMany } = useURLState();
  const includeAux = get("includeAux") === "true";
  const { data, isLoading } = useServiceMapData(time, includeAux);
  const greenEnabled = useModuleEnabled("green");
  const healthEnabled = useModuleEnabled("service-health");
  // Aux stays excluded from the health read, matching the Health screen's
  // default — the two screens must not disagree about a group's status.
  const { byService, groups } = useServiceHealthStatus(time, false, healthEnabled);
  const mapRef = useRef<ServiceMapHandle>(null);

  const filters: MapFilters = useMemo(
    () => ({
      q: get("q"),
      problemsOnly: get("problems") === "true",
      group: get("group"),
    }),
    [get],
  );

  const services = useMemo(() => data?.services ?? [], [data]);
  const edges = useMemo(() => data?.edges ?? [], [data]);
  const shown = useMemo(
    () => filterMap(services, edges, filters, byService),
    [services, edges, filters, byService],
  );

  const setFilters = (next: MapFilters) =>
    setMany({
      q: next.q || undefined,
      problems: next.problemsOnly ? "true" : undefined,
      group: next.group || undefined,
    });

  if (isLoading) return <CenteredSpinner />;

  // The carbon lens is offered ONLY when green runs AND the map actually
  // carries energy (the hub stamps wh/gco2e only then). When it isn't offered,
  // carbon stays off regardless of a stale ?carbon= — the map renders
  // byte-unchanged.
  const canCarbon = greenEnabled && services.some((s) => s.wh !== undefined);
  const carbon = canCarbon && get("carbon") === "true";

  if (!services.length) {
    return (
      <EmptyState icon={MapIcon} title="No services yet">
        The service map draws itself from the services sending OTLP — point an
        OTel SDK at the gateway and they appear here. Call edges are derived from
        trace spans; eBPF flows will enrich them in a later milestone.
      </EmptyState>
    );
  }

  return (
    <div className="flex flex-col gap-3">
      <MapToolbar
        filters={filters}
        groups={groups}
        healthEnabled={healthEnabled}
        canCarbon={canCarbon}
        carbon={carbon}
        includeAux={includeAux}
        onFilters={setFilters}
        onCarbon={(on) => setMany({ carbon: on ? "true" : undefined })}
        onIncludeAux={(on) => setMany({ includeAux: on ? "true" : undefined })}
        onZoomIn={() => mapRef.current?.zoomBy(1.25)}
        onZoomOut={() => mapRef.current?.zoomBy(0.8)}
        onFit={() => mapRef.current?.fit()}
        onRelayout={() => mapRef.current?.relayout()}
      />

      <p data-testid="map-count" className="text-xs text-base-content/55">
        {shown.services.length} services · {shown.edges.length} call edges
        {hasActiveFilter(filters) && ` · filtered from ${services.length}`} · click
        a node for its traces.
      </p>

      <MapLegend health={healthEnabled} carbon={carbon} />

      {shown.services.length === 0 ? (
        <EmptyState icon={MapIcon} title="No services match">
          No service in this window matches the current filter. Clear it, or
          widen the time range.
        </EmptyState>
      ) : (
        <ServiceMap
          services={shown.services}
          edges={shown.edges}
          windowMs={new Date(time.end).getTime() - new Date(time.start).getTime()}
          health={byService}
          handleRef={mapRef}
          carbon={carbon}
        />
      )}
    </div>
  );
}
```

- [ ] **Step 4: Typecheck**

```bash
cd ui && npm run lint && npx tsc --noEmit
```

Expected: clean. The `windowMs` line from Task 4 Step 5 is now part of the rewritten screen — make sure the rewrite kept it.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/service-map/
git commit -m "feat(ui): filters, legend and view controls on the service map

Search, problems-only and group filters, all URL state so a narrowed map
is a pasteable link. The status and group filters appear only when the
service-health module is on, since both read its rollup. The count line
reports the filtered totals and what they were filtered from - which is
also the only way to prove on a canvas that a filter reached the graph."
```

---

## Task 7: The Dashboard's compact map, and the specs the halo broke

**Files:**
- Modify: `ui/src/components/dashboard/topology-card.tsx`
- Modify: `ui/e2e/green.spec.ts:394` and the tooltip test at `:432`

- [ ] **Step 1: Thread windowMs and health into the compact map**

Replace the body of `ui/src/components/dashboard/topology-card.tsx`:

```tsx
"use client";

import Link from "next/link";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { ServiceMap } from "@/components/service-map/service-map";
import { useTimeRange } from "@/hooks/use-time-range";
import { useServiceHealthStatus } from "@/hooks/use-service-health-status";
import { useModuleEnabled } from "@/hooks/use-capabilities";
import type { ServiceEdge, ServiceStats } from "@/lib/api-types";

// Band 2, left column: the Service Map at overview scale. This is the SAME
// component the /service-map screen renders, in its compact mode — so the v0.5
// W7 restyle improved both surfaces at once.
//
// The health read costs nothing here: band 1 already issues this exact query
// with the same key (summary-band.tsx), so the rings come out of the cache.
export function TopologyCard({
  services,
  edges,
}: {
  services: ServiceStats[];
  edges: ServiceEdge[];
}) {
  const { time } = useTimeRange();
  const healthEnabled = useModuleEnabled("service-health");
  const { byService } = useServiceHealthStatus(time, false, healthEnabled);

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Topology</CardTitle>
        <div className="flex items-center gap-3">
          <span className="text-xs text-base-content/45">
            {services.length} services · {edges.length} edges
          </span>
          <Link href="/service-map" className="text-xs text-primary hover:underline">
            Open full map →
          </Link>
        </div>
      </CardHeader>
      <div className="px-3 pb-3">
        <ServiceMap
          services={services}
          edges={edges}
          windowMs={new Date(time.end).getTime() - new Date(time.start).getTime()}
          health={byService}
          compact
        />
      </div>
    </Card>
  );
}
```

`dashboard-screen.tsx` needs no change — `TopologyCard` reads the time range itself, which keeps the Dashboard's props exactly as they are. **If** `npx tsc --noEmit` disagrees, fix the call site rather than the card.

- [ ] **Step 2: Update the green spec's caption assertion**

The carbon caption moved from border to halo. In `ui/src/components/service-map/service-map-screen.tsx` the old caption paragraph is gone (the legend carries it now), so `green.spec.ts` must assert against the legend instead. Replace, in the "offers the carbon lens with a legend" test:

```ts
    await expect(page.getByText(/Carbon lens on · node border = gCO2e/)).toBeVisible();
```

with:

```ts
    await expect(page.getByTestId("map-legend").getByText(/halo = gCO2e/)).toBeVisible();
```

and update that test's comment, which still says "border colors":

```ts
    // Honest limitation: the halo colors are drawn on a canvas and cannot be
    // asserted from the DOM. The user-facing contract held here is the toggle,
    // its URL round-trip, and the legend; the tooltip test below proves the
    // per-node energy actually reaches the graph.
```

Also check the uncheck branch at the end of that test: if it asserts the caption is gone, point it at the same legend text.

- [ ] **Step 3: Re-check the flake-flagged tooltip test**

`green.spec.ts:432` sweeps a 3×3 grid over the canvas until a node's `mouseover` fires, then reads the tooltip. Node `mouseover` now ALSO triggers focus. The tooltip still shows — both live in one handler (Task 4, Step 4) — but re-run it specifically:

```bash
cd ui && npx playwright test e2e/green.spec.ts --repeat-each=3 2>&1 | tail -20
```

Expected: 3/3 pass. If it flakes, that spec's own note authorizes downgrading it to the toggle+legend contract — do that and record the tooltip as manually verified in the commit message. Do not silently delete it.

That test also asserts `page.getByText(/click a node for its traces/)`, which the new count line still contains — confirm it does.

- [ ] **Step 4: Run the full UI e2e**

```bash
make e2e-ui 2>&1 | tail -40
```

Expected: all specs pass, including the new `service-map.spec.ts`. If `service-map.spec.ts` reports different seeded counts than `3 services · 1 call edges`, print what `/api/v1/service-map` actually returns and correct the spec to the real seed — do not loosen the assertion to a regex that would pass on anything.

- [ ] **Step 5: Commit**

```bash
git add ui/src/components/dashboard/ ui/e2e/green.spec.ts
git commit -m "feat(ui): status rings on the Dashboard's compact map

TopologyCard reads the time range and health itself, so the Dashboard's
props are unchanged and the health query comes free from band 1's cache.

Updates the two green specs the halo move broke: the carbon caption is
now a legend entry, and the flake-flagged tooltip test shares mouseover
with focus."
```

---

## Task 8: Full verification

- [ ] **Step 1: Hub gates**

```bash
make check 2>&1 | tail -30
cd hub && golangci-lint run && cd ..
```

Expected: build, vet, lint and unit tests clean. `golangci-lint` must be run explicitly — `make check` alone is not enough before pushing Go.

- [ ] **Step 2: Integration and API e2e**

```bash
cd hub && TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./... 2>&1 | tail -20
cd .. && make e2e 2>&1 | tail -20
```

Expected: pass. If Docker is unavailable, say the integration tests were skipped — do not report them as passing.

- [ ] **Step 3: Chart render assertions**

```bash
make helm-check 2>&1 | tail -10
```

Expected: pass, unchanged — W7 touches no chart values, so a failure here means something unrelated is broken.

- [ ] **Step 4: UI build and e2e**

```bash
cd ui && npm run lint && npm run build 2>&1 | tail -20
cd .. && make e2e-ui 2>&1 | tail -40
```

Expected: the static export builds (it fails hard on any SSR/route violation), and every spec passes.

- [ ] **Step 5: Look at it**

Start the dev server and check the two surfaces by eye — the canvas assertions above cannot see color:

```bash
cd ui && npx next dev -p 3005
```

Ports 3000/3001 are taken on this machine; 3005 is free. Open `http://localhost:3005/service-map` and confirm: rings are visible and match the Service Health screen's verdicts; hovering a node dims the rest and reveals edge rpm/p95; the carbon toggle draws halos, not borders; light/dark both work. Then `http://localhost:3005/dashboard` for the compact map.

- [ ] **Step 6: Commit anything the review turned up, then push**

```bash
git status --short   # re-check the branch: sessions share this working tree
git push -u origin feature/service-map-restyle
```

`gh` is not authenticated here, so open the PR from the link git prints, or hand the user `https://github.com/avuruvision/avuru-obs/compare/feature/service-map-restyle?expand=1`.

---

## Task 9: Docs

By house rule a feature is not shipped here until the docs site says so, and W7 changes a screen users read about.

- [ ] **Step 1: Run the docs-align skill**

Invoke the `docs-align` skill for the service map restyle. It produces bilingual (EN + FR) updates in the `avuru-obs-docs` repo: the changelog entry, the feature-status matrix, and the service-map page — covering status rings, per-edge latency, hover focus, and the filters.

Frame the copy around what it is worth, not the mechanics: the map now tells you *which* dependency is slow, using the same health verdict as the rest of the product.

- [ ] **Step 2: Update the engine README/ROADMAP if they describe the map**

```bash
grep -rn "service map\|topology" README.md ROADMAP.md
```

If either describes the map as flat or lists the restyle as pending, update it in the same branch.

- [ ] **Step 3: Commit**

```bash
git add README.md ROADMAP.md
git commit -m "docs: service map restyle in the engine docs"
```

---

## Done means

An operator opens the service map and sees, without hovering anything, which services are unhealthy — by the same verdict the Service Health screen gives. Hovering one shows exactly what calls it, how fast, and how often. A filter narrows a large estate to one group or just the problems, and the URL carries that view to a colleague. The Dashboard's compact map gained all of it for free, and the docs say so in both languages.
