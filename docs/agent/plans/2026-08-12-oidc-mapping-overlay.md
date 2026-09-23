# OIDC group→role mapping overlay (v0.5 W2b) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an admin add and edit OIDC group→role mappings from Settings → Access, layered over the chart-declared mapping, without a `helm upgrade` and without giving the hub cluster-write permissions.

**Architecture:** The OIDC ConfigMap stays the base and keeps hot-reloading. UI-authored rules live in a new ClickHouse table and are merged over it by one resolver. The merged result is cached in an atomic snapshot because SSO grants are derived on **every authenticated request** — a per-request ClickHouse read is not acceptable here, which is the one place this diverges from the collection-overlay precedent.

**Tech Stack:** Go 1.23 + ClickHouse (hub), Next.js static export + TypeScript strict + TanStack Query (ui), Playwright (e2e).

**Branch:** `feature/oidc-mapping-overlay` (worktree `.claude/worktrees/oidc-mapping-overlay`). All paths repo-relative; run commands from the worktree root.

---

## Why this shape

The plan for v0.5 W2 fixed three things, and they are not up for re-litigation:

- **Apply the collection-overlay pattern.** ConfigMap = base, DB = overlay, chart-declared entries read-only, explicit reset.
- **The hub must NOT write the ConfigMap.** That would trade a clean overlay for cluster-write RBAC the collection AEP deliberately scoped away.
- **Config wins a collision.** Exactly like `service_group`: a UI-authored rule whose group name the chart also declares is stored but **shadowed** — it stops applying, and the UI says so. `ServiceGroupDef.shadowed` is the established precedent and its vocabulary should be reused verbatim.

### The seam

SSO grants are derived at read time, not at login:

- [`auth/service.go:457`](hub/internal/auth/service.go#L457) — `if m := s.mapper.Load(); m != nil && len(u.OidcGroups) > 0 { id.Grants = append(...) }`
- [`auth/service.go:74`](hub/internal/auth/service.go#L74) — `SetGroupMapper(func(groups []string) []Grant)`, an `atomic.Pointer` swap
- `cmd/hub/oidc.go` installs that mapper on load and re-installs it on each ConfigMap hot-reload

So the whole feature lands by changing **what closure gets installed**, not by touching identity resolution.

### The one thing the precedents don't answer: staleness across replicas

The collection overlay is read per request ([`api/collection.go:42`](hub/internal/api/collection.go#L42)) because it backs an admin screen nobody hits in a loop. This mapper runs on every authenticated request, so it must be cached — and a cache introduces a problem single-replica testing will not reveal:

**With two hub replicas (the prod overlay sets `replicas: 2`), an admin's write on replica A does not invalidate replica B's cache.** Refresh-on-write alone would leave replica B serving the old mapping indefinitely.

So the snapshot refreshes on **three** triggers: startup, ConfigMap hot-reload, and any local overlay write — plus a **periodic poll** as the cross-replica backstop. The poll interval is the staleness bound, and it must be documented in the UI, not just the code: an admin who edits a mapping needs to know the change lands within seconds, not instantly. Use **15s**, matching the ConfigMap hot-reload cadence the OIDC ConfigMap already advertises.

---

## File Structure

**Hub — created**

| File | Responsibility |
|---|---|
| `hub/internal/storage/migrations/0017_oidc_group_mapping.sql` | the overlay table |
| `hub/internal/storage/clickhouse/oidcmapping.go` | list / upsert / tombstone / reset |
| `hub/internal/auth/mapping.go` | the pure merge (config + overlay → effective rules + provenance) |
| `hub/internal/auth/mapping_test.go` | merge semantics, incl. shadowing |
| `hub/internal/api/oidc_mapping.go` | admin-gated CRUD handlers |

**Hub — modified**

| File | Change |
|---|---|
| `hub/internal/storage/store.go` | `OIDCGroupMapping` type + 4 Store methods |
| `hub/internal/storage/storagetest/fake.go` | fake implementations |
| `hub/internal/api/router.go` | 4 routes, admin-gated, registered only when OIDC is configured |
| `hub/cmd/hub/oidc.go` | install the merged+cached mapper instead of the config-only one |

**UI — created**

| File | Responsibility |
|---|---|
| `ui/src/hooks/use-oidc-mapping.ts` | query + mutations |
| `ui/src/components/settings/oidc-mapping-panel.tsx` | the list |
| `ui/src/components/settings/oidc-mapping-form.tsx` | add/edit a rule |

**UI — modified**

| File | Change |
|---|---|
| `ui/src/lib/api-types.ts` | `OIDCMappingRule`, response shapes |
| `ui/src/lib/query-keys.ts` | `oidcMapping` key |
| `ui/src/components/settings/access-tab.tsx` | host the panel, gated on SSO being configured |
| `ui/e2e/settings-config.spec.ts` or a new spec | coverage |

---

## House rules for every commit

- **NO `Co-Authored-By` trailer** (AI_POLICY.md).
- Conventional commits (`feat(hub):`, `feat(ui):`, `test:`, `docs:`).
- Before pushing Go: `cd hub && golangci-lint run` (it lives at `~/go/bin/golangci-lint` on this machine if not on PATH). Build and vet are not enough.
- The UI has **no unit-test runner** (Playwright only, by design). Do not add one.
- Never hardcode hex in components; use daisyUI semantic tokens.
- API types are hand-written to mirror the Go types — read them, never invent a shape.

---

## Task 1: Storage — the overlay table

**Files:**
- Create: `hub/internal/storage/migrations/0017_oidc_group_mapping.sql`
- Create: `hub/internal/storage/clickhouse/oidcmapping.go`
- Create: `hub/internal/storage/clickhouse/oidcmapping_integration_test.go`
- Modify: `hub/internal/storage/store.go`, `hub/internal/storage/storagetest/fake.go`

Model it on `service_group` — read [`0016_service_groups.sql`](hub/internal/storage/migrations/0016_service_groups.sql) and [`clickhouse/servicegroup.go`](hub/internal/storage/clickhouse/servicegroup.go) first and follow them closely; this is the same shape with different columns.

- [ ] **Step 1: Write the migration**

```sql
-- 0017_oidc_group_mapping.sql
-- UI-authored OIDC group→role rules. Chart-declared rules keep living in the
-- OIDC ConfigMap and are NOT copied here; the resolver merges the two and lets
-- the config win a name collision, exactly as service_group does.
--
-- Group is the identity, as it already is in the config schema, so an edit is
-- an upsert and a delete is a tombstone. Same ReplacingMergeTree + FINAL +
-- tombstone pattern as service_group / project / alert_channel.
CREATE TABLE IF NOT EXISTS {db}.oidc_group_mapping
(
    `Group`     String,
    `Role`      String,
    `Projects`  Array(String) DEFAULT [],
    `Deleted`   UInt8 DEFAULT 0,
    `CreatedBy` String,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (`Group`);
```

Register it in `hub/internal/storage/migrations/migrations.go` in **both** places — the `.sql` files are embedded by glob, but registration is explicit and a test enforces it:

1. Append `"0017_oidc_group_mapping.sql"` to `Ordered` (lexical order = apply order).
2. Add it to `ByModule` as `{modules.Core}`, with a one-line comment saying why. **Core, not a feature module** — auth gates everything, matching how `0011_auth_oidc_groups.sql`, `0013_auth_ingest_keys.sql` and `0015_auth_user_deleted.sql` are tagged. Do NOT invent an OIDC module; OIDC is a configuration of core auth, not a `modules.Name`.

`TestByModuleCoversOrdered` fails if you add to `Ordered` and forget `ByModule`, so run `go test ./internal/storage/migrations/` after this step and expect it to catch you if you miss one.

- [ ] **Step 2: Add the storage type and interface methods**

In `hub/internal/storage/store.go`, beside the service-group types:

```go
// OIDCGroupMapping is one UI-authored IdP-group → role-on-projects rule. It
// overlays the chart-declared mapping in the OIDC ConfigMap, which stays the
// base and stays hot-reloading; the hub never writes that ConfigMap.
type OIDCGroupMapping struct {
	Group     string
	Role      string
	Projects  []string
	CreatedBy string
	UpdatedAt time.Time
}
```

and on the Store interface:

```go
	ListOIDCGroupMappings(ctx context.Context) ([]OIDCGroupMapping, error)
	SaveOIDCGroupMapping(ctx context.Context, m OIDCGroupMapping) error
	DeleteOIDCGroupMapping(ctx context.Context, group string) error
	// ResetOIDCGroupMappings tombstones every UI-authored rule, returning the
	// install to exactly what the chart declares.
	ResetOIDCGroupMappings(ctx context.Context) error
```

- [ ] **Step 3: Implement them in `clickhouse/oidcmapping.go`**

Mirror `servicegroup.go` exactly: `SELECT ... FROM oidc_group_mapping FINAL WHERE Deleted = 0 ORDER BY Group`, an upsert INSERT, and a tombstone INSERT with `Deleted = 1`. `Group` is a ClickHouse keyword — **quote it with backticks in every statement** or the query will fail in ways the error message does not make obvious.

`ResetOIDCGroupMappings` tombstones every currently-live row (list, then insert a tombstone per group) rather than issuing a `TRUNCATE`, so the operation is replicated and auditable like every other delete here.

- [ ] **Step 4: Fake + integration test**

Add the four methods to `storagetest/fake.go` (follow the service-group ones). Write `oidcmapping_integration_test.go` modelled on `servicegroup_integration_test.go`: save two rules, list them, edit one (assert the upsert wins), delete one (assert it disappears), reset (assert empty).

- [ ] **Step 5: Run it**

```bash
cd hub && go build ./... && TESTCONTAINERS_RYUK_DISABLED=true go test -tags integration ./internal/storage/clickhouse/ -run OIDCMapping 2>&1 | tail -15
```

Expected: PASS. `TESTCONTAINERS_RYUK_DISABLED=true` is required on this machine.

- [ ] **Step 6: Commit**

```bash
git add hub/internal/storage/
git commit -m "feat(hub): store UI-authored OIDC group mappings

Same ReplacingMergeTree + FINAL + tombstone shape as service_group, and
for the same reason: the chart-declared mapping stays in its ConfigMap
and stays hot-reloading, while this table only carries what an admin
authored in the app. The hub never writes the ConfigMap."
```

---

## Task 2: The merge, as a pure function

**Files:**
- Create: `hub/internal/auth/mapping.go`, `hub/internal/auth/mapping_test.go`

- [ ] **Step 1: Write the failing test**

`hub/internal/auth/mapping_test.go`. The semantics to pin:

1. A config-only group produces one effective rule, `Source: "config"`, `Editable: false`.
2. A DB-only group produces one effective rule, `Source: "db"`, `Editable: true`.
3. **A group in both: the config rule applies, and the DB rule is returned with `Shadowed: true` and does NOT contribute grants.** This is the collision rule and the most important case.
4. Ordering is stable (sort by group) so the UI list doesn't jitter between reads.
5. An empty overlay reproduces the config mapping exactly — an install that never edits anything must behave byte-identically to today.

```go
func TestMergeMapping(t *testing.T) {
	cfg := []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}}
	db := []storage.OIDCGroupMapping{
		{Group: "platform", Role: "viewer", Projects: []string{"default"}},
		{Group: "oncall", Role: "editor", Projects: []string{"default"}},
	}
	got := MergeMapping(cfg, db)

	if len(got) != 3 {
		t.Fatalf("got %d rules, want 3: %+v", got, got)
	}
	byKey := map[string]EffectiveMapping{}
	for _, r := range got {
		byKey[r.Group+"|"+r.Source] = r
	}
	if r := byKey["platform|config"]; r.Editable || r.Shadowed {
		t.Errorf("config rule should be read-only and applying: %+v", r)
	}
	if r := byKey["platform|db"]; !r.Shadowed {
		t.Errorf("db rule colliding with config must be shadowed: %+v", r)
	}
	if r := byKey["oncall|db"]; !r.Editable || r.Shadowed {
		t.Errorf("db-only rule should be editable and applying: %+v", r)
	}
}
```

Add a second test proving the **grant** path: `EffectiveGrants` (or whatever you name the function that feeds `SetGroupMapper`) must not return the shadowed rule's grants.

```go
func TestShadowedRuleGrantsNothing(t *testing.T) {
	cfg := []GroupMap{{Group: "platform", Role: RoleAdmin, Projects: []string{"*"}}}
	db := []storage.OIDCGroupMapping{{Group: "platform", Role: "viewer", Projects: []string{"default"}}}
	grants := GrantsFor(MergeMapping(cfg, db), []string{"platform"})
	for _, g := range grants {
		if g.Role == RoleViewer && g.Scope == "default" {
			t.Fatalf("shadowed db rule leaked a grant: %+v", grants)
		}
	}
}
```

Adapt names to whatever reads best, but keep both behaviors covered.

- [ ] **Step 2: Run it, confirm it fails to compile** (`MergeMapping` undefined).

```bash
cd hub && go test ./internal/auth/ -run 'TestMergeMapping|TestShadowedRule' 2>&1 | tail -10
```

- [ ] **Step 3: Implement `mapping.go`**

Keep it pure — no context, no store, no clock. It takes the config rules and the DB rows and returns the merged view. The existing `GrantsForGroups` on `OIDCConfig` ([`oidcconfig.go:80`](hub/internal/auth/oidcconfig.go#L80)) is the model for grant derivation, including its de-duplication via a `seen` map and its `defaultRole` fallback when nothing matched — **preserve that fallback**, since an install relying on `defaultRole` must not lose it when the overlay is empty.

```go
// EffectiveMapping is one merged rule plus where it came from, so the UI can
// render chart-declared rules read-only and mark a shadowed one.
type EffectiveMapping struct {
	Group    string
	Role     Role
	Projects []string
	Source   string // "config" | "db"
	Editable bool   // db rules only
	Shadowed bool   // a db rule whose group the config also declares
}
```

- [ ] **Step 4: Run the tests to green**, then `cd hub && go test ./internal/auth/ 2>&1 | tail -5`.

- [ ] **Step 5: Commit**

```bash
git add hub/internal/auth/mapping.go hub/internal/auth/mapping_test.go
git commit -m "feat(hub): merge chart-declared and UI-authored OIDC mappings

Config wins a group-name collision and the overlay rule is marked
shadowed rather than dropped, so the UI can explain why an authored rule
stopped applying instead of silently losing it - the same contract
service groups already give. Pure function: no store, no clock, so the
collision semantics are testable on their own."
```

---

## Task 3: Cache the merged mapper, and keep replicas honest

**Files:**
- Create: `hub/internal/auth/mappingcache.go` (+ test)
- Modify: `hub/cmd/hub/oidc.go`

This is the task with the real hazard. Read the "staleness across replicas" note at the top of this plan before starting.

- [ ] **Step 1: Write the failing test**

Cover:
1. `Refresh` merges config + store rows and installs a mapper that returns the merged grants.
2. After the store's rows change, the mapper still returns the OLD grants until `Refresh` runs — proving it is genuinely cached and not reading through.
3. After `Refresh`, it returns the new grants.
4. A store error leaves the previous snapshot in place (an unreachable ClickHouse must not silently strip everyone's SSO grants — that would be a lockout, and this is the failure mode most worth pinning).

Point 4 is the one that matters most. Write it explicitly:

```go
func TestRefreshErrorKeepsLastGoodSnapshot(t *testing.T) {
	// ... refresh once with a working fake, capture grants ...
	fake.Err = errors.New("clickhouse down")
	_ = cache.Refresh(context.Background())
	// grants must be unchanged, NOT empty
}
```

- [ ] **Step 2: Run it, confirm failure.**

- [ ] **Step 3: Implement the cache**

A small type holding an `atomic.Pointer` to the merged `[]EffectiveMapping`, a `Refresh(ctx) error` that loads the store rows, merges with the *current* config, and swaps — keeping the previous snapshot on error and logging at warn.

Expose `Mapper() func([]string) []Grant` reading the snapshot, to hand to `SetGroupMapper`.

- [ ] **Step 4: Wire it in `cmd/hub/oidc.go`**

Read that file first. Today it installs a mapper derived from the ConfigMap alone. Change it so:
- on startup and on every ConfigMap hot-reload, the cache's config half is replaced and `Refresh` runs;
- a goroutine calls `Refresh` every **15s**, so a write on another replica lands within that bound;
- the mapper installed via `SetGroupMapper` is the cache's.

The periodic goroutine must exit with the hub's context — follow whatever lifecycle the existing hot-reload watcher uses in that file rather than starting a bare `go func()`.

- [ ] **Step 5: Verify**

```bash
cd hub && go build ./... && go test ./... 2>&1 | tail -10 && ~/go/bin/golangci-lint run 2>&1 | tail -3
```

- [ ] **Step 6: Commit**

```bash
git add hub/internal/auth/ hub/cmd/hub/oidc.go
git commit -m "feat(hub): cache the merged OIDC mapping, refresh on a bound

SSO grants are derived on every authenticated request, so the merged
mapping cannot be read from ClickHouse per request the way the
collection overlay is. It is cached and refreshed on three triggers -
startup, ConfigMap hot-reload, and a local overlay write - plus a 15s
poll, which is what makes an admin's edit reach the OTHER hub replica:
refresh-on-write alone would leave replica B serving the old mapping
indefinitely.

A failed refresh keeps the last good snapshot rather than emptying it.
An unreachable ClickHouse must not strip every SSO user's grants."
```

---

## Task 4: The API

**Files:**
- Create: `hub/internal/api/oidc_mapping.go` (+ tests in `hub/internal/api/oidc_mapping_test.go`)
- Modify: `hub/internal/api/router.go`

Read [`hub/internal/api/service_groups.go`](hub/internal/api/service_groups.go) first — this is the same CRUD shape, admin-gated, and should look like its sibling.

- [ ] **Step 1: Write the failing tests**

- `GET /api/v1/auth/oidc/mapping` returns config + db rules with `source`, `editable`, `shadowed`.
- `PUT /api/v1/auth/oidc/mapping/{group}` upserts, admin-only (a viewer gets 403).
- `DELETE /api/v1/auth/oidc/mapping/{group}` tombstones; **deleting a config-declared group is a 400, not a silent no-op** — the chart owns it, and the error must say so.
- `POST /api/v1/auth/oidc/mapping/reset` clears all authored rules.
- An invalid role is a 400 (reuse `auth.ParseRole`, do not re-implement role parsing).
- The routes are registered only when OIDC is configured; with OIDC off they 404.

- [ ] **Step 2: Run, confirm red.**

- [ ] **Step 3: Implement**, registering routes in `router.go` beside the other `/api/v1/auth/oidc/*` routes, gated on the same condition those use, with `a.secured(auth.RoleAdmin, ...)`.

Every write handler must call the cache's `Refresh` after a successful write so the local replica reflects it immediately.

- [ ] **Step 4: Green + lint**, then commit:

```bash
git add hub/internal/api/
git commit -m "feat(hub): admin CRUD for the OIDC mapping overlay

Deleting a chart-declared rule is a 400 rather than a no-op: the chart
owns it, and an admin who tries deserves to be told where it lives
instead of watching a row reappear. Writes refresh the local snapshot
immediately; other replicas pick it up on their poll."
```

---

## Task 5: The UI

**Files:**
- Create: `ui/src/hooks/use-oidc-mapping.ts`, `ui/src/components/settings/oidc-mapping-panel.tsx`, `ui/src/components/settings/oidc-mapping-form.tsx`
- Modify: `ui/src/lib/api-types.ts`, `ui/src/lib/query-keys.ts`, `ui/src/components/settings/access-tab.tsx`

Copy the shape of [`service-groups-panel.tsx`](ui/src/components/settings/service-groups-panel.tsx) + [`service-group-form.tsx`](ui/src/components/settings/service-group-form.tsx), including **how they render config-defined rows read-only** — that is the precedent for this exact problem and the UI should feel like the same product.

- [ ] **Step 1: Types + hook.** Mirror the Go DTO exactly.

- [ ] **Step 2: Panel + form.** Requirements:
  - Config rules render read-only, visibly labelled as coming from the chart.
  - A shadowed authored rule is visibly marked, with a one-line explanation of *why* it stopped applying (the chart declares the same group) — do not just grey it out.
  - An explicit **Reset to chart defaults** action, confirmed before it fires.
  - Say that a change reaches all hub replicas **within ~15 seconds**. This is a real property of the design, and an admin who edits a mapping and sees no immediate effect on another replica should have been told, not left guessing.

- [ ] **Step 3: Mount in the Access tab, gated on SSO.** `GET /api/v1/auth/config` returns `methods` including `"oidc"` when SSO is configured ([`auth_handlers.go:28`](hub/internal/api/auth_handlers.go#L28)). With SSO off the panel must not render at all — its routes 404 there.

- [ ] **Step 4: Verify**

```bash
cd ui && npm run lint && npx tsc --noEmit && npm run build 2>&1 | tail -5
```

- [ ] **Step 5: Commit**

```bash
git add ui/src/
git commit -m "feat(ui): edit the OIDC group mapping from Settings -> Access

Chart-declared rules render read-only next to authored ones, a shadowed
rule says why it stopped applying rather than just greying out, and the
panel states the ~15s propagation bound instead of leaving an admin to
wonder why the other replica disagrees."
```

---

## Task 6: e2e, changelog, docs

- [ ] **Step 1: Playwright.** Add coverage to a settings spec: with SSO stubbed on, the panel lists rules, adding one round-trips, a config rule offers no delete, and reset is confirmed before firing. Stub `/api/v1/auth/config` the way `modules.spec.ts` stubs capabilities.

**`make e2e-ui` does not work on this machine.** Bring the stack up with a scratch compose override (never commit it) setting `AVURUOBS_AUTH_ANONYMOUS_ROLE: admin` and `AVURUOBS_AUTH_ANONYMOUS_PROJECTS: "*"` on the hub, seed as the `e2e-ui` target does, then `cd ui && npx playwright test`. Expect only the 2 by-design anonymous-mode `auth.spec.ts` failures.

- [ ] **Step 2: CHANGELOG.md.** Add an entry to `[Unreleased] / ### Added`. Match the voice of the neighbouring entries — they explain *why*, and they are honest about limits. Say what this replaces (editing `auth.oidc.mapping` in values and running `helm upgrade`), that the chart stays the base, and that propagation is bounded at ~15s. **No competitor names** — the release job extracts these notes verbatim.

- [ ] **Step 3: Docs.** Run the `docs-align` skill for this feature (EN + FR): changelog entry both locales, the API reference row, and the status matrix if it now understates access control.

- [ ] **Step 4: Full gates.**

```bash
make check && make helm-check && cd hub && ~/go/bin/golangci-lint run
```

---

## Done means

An admin with SSO configured opens Settings → Access, sees the chart's group→role rules read-only beside the ones they authored, adds a rule for a group the chart never mentioned, and has it apply to that group's next request — on every replica, within ~15 seconds — without touching `values.yaml`. Deleting a chart rule tells them where it actually lives. Reset returns the install to exactly what the chart declares. And an install that never opens the panel behaves exactly as it does today.
