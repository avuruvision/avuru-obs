# Project Management (Coroot-style) — Combined Design

**Date:** 2026-07-28
**Status:** Draft for review
**Author:** brainstorming session
**Target:** `main` (0.3.0-SNAPSHOT), next release

## 1. Context

Today avuru-obs has **no persistent notion of a project**. The project list is
*derived* on every request by [`handleProjects`](../../../hub/internal/api/projects.go):

```
projects = {default} ∪ config (AVURUOPS_PROJECTS) ∪ tenants-seen-in-data ∪ RBAC-granted-scopes
```

- `default` is the hardcoded `storage.DefaultTenant`.
- A project's **tenant id** is the partition key on every stored telemetry row
  (traces/logs/metrics/profiles all carry `Tenant`; the UI selects it via the
  `X-Avuru-Tenant` header, see [`api.ts`](../../../ui/src/lib/api.ts)).
- The gateway (an OTel Collector) stamps `avuru.tenant` via a `resource/tenant`
  or `transform/tenant` processor. **Ingest is unauthenticated** — any sender can
  claim any tenant.
- The Settings → General tab shows a read-only card explaining projects are
  "defined through deployment configuration and cannot be modified here."

Users want Coroot's model: manage projects from the UI (create / rename / delete),
per-project **API keys** for ingest, and **member projects** (multi-cluster
aggregation). This document designs all three as one combined effort, delivered
in three dependency-ordered phases.

## 2. Goals

- Admins create, rename, and delete projects from the UI; the switcher and
  General tab reflect them.
- Config-defined and the built-in `default` project remain read-only in the UI
  (a banner, exactly like Coroot's "defined through the config").
- Per-project ingest API keys authenticate telemetry at the gateway and stamp the
  key's project as the tenant.
- A project may be a **member-project aggregate**: selecting it shows the merged
  data of its member tenants (multi-cluster view).
- A **demo project** a prospect can explore as a read-only viewer via a one-click
  "Try the demo" login, backed by live representative telemetry — to see the
  product's potential without any setup.
- Everything admin-gated; non-admins keep read-only visibility of their granted
  projects.

## 3. Non-goals

- **True tenant rename** (rewriting the `Tenant` column across ClickHouse). The
  tenant id is immutable; only a display **label** is editable (decision below).
- **Delete purges data.** Delete removes the project *entry*; its telemetry ages
  out by the existing per-signal TTL. No immediate hard-delete of rows. **Note:**
  if a deleted project's tenant is still actively receiving telemetry, it
  re-appears in the list as `source: data` (auto-discovery within the tenant
  lookback) — you cannot hide a tenant that is still sending data. This is
  intended; the delete removes the UI-managed label/members, not the live stream.
- Nested aggregates (a member that is itself an aggregate). One level only.
- Per-project retention / signal config (stays instance-wide for now).

## 4. Key decisions (locked in brainstorming)

1. **Identity = immutable id + editable label.** A project is an immutable tenant
   slug (used in data and `X-Avuru-Tenant`) plus a human label. Create declares a
   new id; rename edits only the label; delete removes the entry (data ages out).
   No data migration on any operation.
2. **Four subsystems**, one combined spec, **four phases**: CRUD → API keys →
   member projects, plus a **demo project** (Phase 4) that depends only on Phase 1
   and existing auth, so it can land right after CRUD. Each phase ships
   independently and leaves the product working.
3. **Persistence** follows the existing auth pattern: ClickHouse
   `ReplacingMergeTree(UpdatedAt)` + `FINAL`, tombstone deletes, embedded ordered
   `.sql` migrations, all SQL behind the `storage.Store` seam with a
   `storagetest` fake.
4. **RBAC:** all mutations require a wildcard admin grant, reusing the
   `securedAdmin` middleware that guards the users endpoints. Config-defined and
   `default` projects reject rename/delete/member-edit with 409/403.

## 5. Data model

One `project` table introduced in Phase 1, carrying the `Members` column from the
start so Phase 3 needs no schema change (the column is simply unused until then).

```sql
-- 0012_projects.sql  (module: Core)
-- UI-managed projects. Id is the immutable tenant slug (= partition key on all
-- telemetry); Label is the editable display name; Members is the multi-cluster
-- aggregate set (empty for a normal project). Delete is a tombstone. Same
-- ReplacingMergeTree + FINAL + tombstone pattern as auth_* / alert_channel.
CREATE TABLE IF NOT EXISTS otel.project
(
    `Id`        String,
    `Label`     String,
    `Members`   Array(String) DEFAULT [],
    `Deleted`   UInt8 DEFAULT 0,
    `CreatedBy` String,
    `CreatedAt` DateTime64(3) DEFAULT now64(3),
    `UpdatedAt` DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Id);
```

```go
// storage.Project is one UI-managed project. Id is immutable (the tenant slug);
// Label is display-only; Members is the aggregate set (empty for a leaf project).
type Project struct {
    ID        string
    Label     string
    Members   []string
    CreatedBy string
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

Store additions (near the auth methods):

```go
ListProjects(ctx context.Context) ([]Project, error)          // live only
GetProject(ctx context.Context, id string) (Project, error)   // ErrNotFound if absent/deleted
SaveProject(ctx context.Context, p Project) error             // upsert by Id
DeleteProject(ctx context.Context, id string) error           // tombstone; ErrNotFound if none live
```

Backend `clickhouse/projects.go` mirrors `clickhouse/auth.go`; the `storagetest`
fake keeps a `map[string]Project` and filters tombstones on read.

## 6. Source precedence (the merge)

`handleProjects` gains a `db` source and a `label`/`editable` field. Precedence
when the same id appears from multiple origins (first wins for `source`):

```
default  >  config  >  db  >  data  >  granted
```

- `default` and `config`: read-only (`editable:false`) — Coroot's config banner.
- `db`: UI-managed (`editable:true`) — rename/delete/members allowed.
- `data`/`granted`: still surfaced so nothing a user can see disappears; a `db`
  row for the same id just adds a label + editability.

`projectDTO`:

```go
type projectDTO struct {
    ID       string   `json:"id"`
    Label    string   `json:"label,omitempty"`
    Source   string   `json:"source"`   // default|config|db|data|granted
    Editable bool     `json:"editable"` // true only for source==db
    Members  []string `json:"members,omitempty"`
}
```

## 7. Phase 1 — Project CRUD

**Endpoints** (all under `securedAdmin` except the existing GET):

| Method | Path | Body | Result |
|---|---|---|---|
| GET | `/api/v1/projects` | — | merged list (existing, now with label/editable/members) |
| POST | `/api/v1/projects` | `{id, label}` | 201 create; 409 on reserved/duplicate |
| PATCH | `/api/v1/projects/{id}` | `{label}` | 200 rename label; 409 if not `db`-editable |
| DELETE | `/api/v1/projects/{id}` | — | 204 tombstone; 409 if not `db`-editable |

**Validation:** `id` matches `^[a-z][a-z0-9-]{0,62}$`, is not `default`, and is not
already a config project. `label` is free text (trimmed, ≤200 chars). Rename and
delete refuse any id whose effective source is `default` or `config` (409 with a
clear message), so the immutable/deployment-owned projects can't be mangled.

**UI** (`ui/src/components/settings/general-tab.tsx` → Coroot layout):
- "Project name / label" field: editable for `db` projects, read-only with the
  existing config banner for `default`/`config`.
- A **New project** affordance (id + label) — admin only. Reachable from the
  General tab; optionally a "+ New project" row in the switcher later.
- A **Danger zone** "Delete this project" for `db` projects.
- The [`project-switcher`](../../../ui/src/components/layout/project-switcher.tsx)
  renders `label || id`; selection still keys on `id` (the tenant).
- New hook `useProjectMutations` (create/rename/delete → invalidate `projects`
  query). `api-types.ts` gains `Project`/`ProjectsResponse` fields.

**Tests:** store integration (`projects_test.go`), API handler tests (create/
rename/delete happy + reserved/duplicate/permission paths), e2e additions to
`ui/e2e/projects.spec.ts` (create → appears in switcher; rename → label shows;
delete → gone; config project shows read-only banner).

## 8. Phase 2 — Per-project ingest API keys

**This phase is already designed in detail** in
[`../plans/2026-07-27-auth-ingest-keys.md`](../plans/2026-07-27-auth-ingest-keys.md)
(auth "Plan C"). It is adopted wholesale with **one reconciliation**: its
migration takes slot **`0013_auth_ingest_keys.sql`** (Phase 1 claims `0012`).

Summary of that plan (authoritative task list lives in the plan doc):
- Table `auth_ingest_key` (KeyHash, Project, Name, Prefix, CreatedBy, Revoked).
- `auth.NewIngestKey()` → `avuruk_…`; only the sha256 hash is stored; raw shown
  once.
- Admin CRUD `/api/v1/projects/{id}/keys` (create returns the secret once; list
  shows prefix+metadata only; delete revokes).
- Hub-internal `POST /internal/v1/ingest-keys/validate`, guarded by a
  chart-generated hub↔gateway token; the hub is never in the telemetry byte-path.
- Two in-repo OCB modules: `gateway/avuruingestauth` (server-auth extension:
  reads the key, validates against the hub with a 30 s cache + 5 min stale grace)
  and `gateway/tenantfromauth` (stamps `avuru.tenant` from the validated key's
  project).
- Rollout knob `auth.ingest.mode: off | log | enforce`, **default `log`**, so the
  Jaeger-style drop-in promise holds: existing unkeyed senders keep landing after
  upgrade; operators opt into `enforce`.
- UI panel `Settings → project → API keys`; Helm secrets + sensor key + mode.

**Dependency on Phase 1:** keys are scoped to a project id; the key panel mounts
per selected project and lists/creates against `{id}`. `enforce` mode makes the
key's project the authoritative tenant, superseding client-supplied `avuru.tenant`.

## 9. Phase 3 — Member projects (multi-cluster)

The largest surface: aggregation touches every read path.

**Model:** the `Members []string` column already exists (Phase 1). A project with
non-empty `Members` is an **aggregate** — selecting it queries the union of its
member tenants. One level only (a member must be a leaf; reject saving members
that are themselves aggregates, and reject self-membership → no cycles).

**Tenant resolution (new seam):** an API-layer helper turns the requested project
into the concrete tenant set to query:

```go
// resolveTenants maps the request's project to the tenant id(s) to query. A leaf
// project resolves to itself; an aggregate resolves to its members (filtered to
// the caller's granted scopes). Returns an error the API layer maps to 403 when
// the identity may see none of the resolved tenants.
func (a *API) resolveTenants(ctx context.Context, project string) ([]string, error)
```

Resolution reads the `project` table (cached like `tenantCache`). RBAC: aggregate
members are intersected with the identity's `ProjectScopes()` (wildcard passes
all); a non-admin selecting an aggregate sees only members they're granted.

**Query fan-out — `WHERE Tenant IN (…)`:** the query structs that carry a single
`Tenant string` gain a `Tenants []string` (populated by the API layer from
`resolveTenants`); backends switch `WHERE Tenant = ?` → `WHERE Tenant IN (?)`.
Because `Tenant` is the `LowCardinality` ORDER BY prefix, an `IN` over a small set
stays index-efficient. Crucially, **most endpoints aggregate for free**: the
existing `GROUP BY service/operation/fingerprint` collapses across tenants in one
query, so no app-level merge logic is needed for services, RED, traces, logs,
heatmap, errors, agents, green. Single-entity lookups
(`GetTrace`/`FindSpanTrace`/`LogsForTrace`) take the tenant set and match `Tenant
IN (…)` (a trace/span id is globally unique, so the union is correct).

**Scope of the change:** mechanical but broad — ~30 `storage.Store` methods and
their ClickHouse implementations. To keep it reviewable it is split within the
phase: (a) add `Tenants` to the query structs + `resolveTenants`, defaulting
`Tenants = [Tenant]` so behavior is unchanged; (b) convert backends signal-by-
signal (traces, then logs, then metrics/infra, then errors, then green), each with
its integration test; (c) wire the API layer to pass the resolved set; (d) UI.

**UI:** General tab gains a **Member projects** multi-select (Coroot-style),
editable only for `db` projects, listing leaf projects. The switcher shows
aggregates alongside leaves (an icon/marker distinguishes them). Also accept
chart-defined members (optional, config source) so multi-cluster can be declared
in deployment too — deferred within the phase if it adds risk.

**Tests:** `resolveTenants` unit tests (leaf, aggregate, RBAC filtering, cycle/
nested rejection); per-signal integration tests asserting an aggregate returns the
union of two seeded tenants; e2e that seeds two tenants, creates an aggregate, and
checks the merged view. The existing `projects.spec.ts` staging/default isolation
tests must still pass (leaf projects stay isolated).

## 10. Phase 4 — Demo project (viewer showcase)

A zero-setup way for a prospect to explore the product as a read-only viewer. It
reuses Phase 1 (the `demo` project entry) and the existing auth stack; it does
**not** depend on Phase 2 or 3, so it can ship right after Phase 1.

**Access — shared demo viewer login.** Auth stays fully enabled (no anonymous
path). A bootstrapped viewer user (`viewer@demo`, granted `viewer@demo`) backs a
one-click **"Try the demo"** entry on the login screen.

- **Bootstrap:** mirror the existing admin bootstrap (`hub/internal/auth`): when
  `AVURUOPS_DEMO_ENABLED=true`, ensure a viewer user exists with the configured
  email and a `viewer` grant scoped to the `demo` project. Idempotent on start.
  Credentials come from env (`AVURUOPS_DEMO_EMAIL`, `AVURUOPS_DEMO_PASSWORD`),
  chart-generated when unset.
- **One-click login endpoint:** `POST /api/v1/auth/demo` signs the request in as
  the demo viewer **server-side** (rate-limited, reusing the login limiter) so the
  shared password never ships to the browser. It is registered only when
  `AVURUOPS_DEMO_ENABLED=true`.
- **Login-page CTA:** `/api/v1/auth/config` gains `demoEnabled bool`. When true the
  login page shows a **"Try the demo"** button that calls `/auth/demo` and lands in
  the app. Hidden otherwise.
- **Scoping:** the demo viewer's only grant is `viewer@demo`, so the project
  switcher shows just `demo`, it is their landing project, and every write surface
  (triage, alert-channel edits, project/user admin, ingest keys) is already
  role-gated above viewer — the guest can look but not touch. The Users tab stays
  hidden (non-admin), consistent with the Phase-1 settings fix.

**Data — live OpenTelemetry Astronomy Shop.** The scaffolded
[`deploy/demo/astronomy`](../../../deploy/demo/astronomy) install runs the demo
workloads with the gateway tagging `gateway.tenant: demo`, so live traces, logs,
metrics, RED, errors, service map, infra, and green data flow into the `demo`
tenant through the normal ingest path — no special-casing in the hub. This suits a
**hosted public demo instance** (a running cluster), not an air-gapped install.
The `demo` project entry (Phase 1) carries a friendly label (e.g. "Demo —
Astronomy Shop").

**Interaction with Phase 2:** if ingest-key `enforce` is ever enabled on the demo
gateway, it needs a `demo` project key; under the default `log` mode nothing is
required. Independent of Phase 3.

**Deployment:** opt-in only — a normal install ships **no** demo user and no demo
tenant. A `demo.enabled` chart preset (or the astronomy `install.sh`) wires the
env flags, the demo gateway tenant, and the demo workloads together. Guardrail:
the demo viewer is a single shared low-privilege identity; the `/auth/demo`
endpoint is rate-limited; nothing about the demo widens any other install.

**Tests:** auth bootstrap unit test (demo user + grant created idempotently when
enabled, absent when disabled); API test for `/auth/demo` (issues a viewer
session; 404 when disabled; rate-limited); e2e that clicks "Try the demo" and
asserts a read-only session scoped to `demo` with write controls absent.

## 11. Cross-cutting concerns

- **RBAC:** `securedAdmin` for all mutations; `resolveTenants` enforces read
  scoping for aggregates. `default`/`config` projects are never mutable.
- **Config coexistence:** `AVURUOPS_PROJECTS` and gateway `gateway.tenant` keep
  working; `db` projects are additive. A `db` row shadowing a `config` id keeps
  `config` precedence (stays read-only) — documented, not an error.
- **Store outage:** `handleProjects` still answers 200 with default+config even if
  ClickHouse (and thus `ListProjects`) is down — the switcher must always render.
- **Docs:** `docs-align` skill run per phase (EN + FR): changelog, feature-status
  matrix, API reference for the new endpoints, and guides ("managing projects",
  "authenticating ingest", "multi-cluster views"). README + roadmap badges.

## 12. Risks & sequencing

| Risk | Mitigation |
|---|---|
| Phase 3 touches every read path | Struct-default `Tenants=[Tenant]` makes the conversion a no-op until aggregates exist; convert signal-by-signal with tests |
| Ingest-key enforce could drop telemetry | Default `mode: log`; enforce is opt-in; drop-in promise tested in e2e |
| Migration slot collision (0012) | Phase 1 = `0012_projects.sql`; ingest keys renumbered to `0013` |
| `IN`-clause perf on large tenant sets | Tenant is the LowCardinality ORDER BY prefix; aggregates are a handful of members |
| Config vs db id shadowing confusion | Fixed precedence (config wins, read-only) + doc note |
| Shared demo login abused / demo tenant on a real install | Opt-in only (`demo.enabled`); single low-privilege viewer; `/auth/demo` rate-limited; no demo user/tenant shipped by default |

**Build order:** Phase 1 (foundation) → Phase 4 (demo — needs only Phase 1, high
showcase value, land early) → Phase 2 (independent; already planned) → Phase 3
(largest). Each is independently shippable to `main`.

## 13. Open questions (resolve during planning)

1. Create-project UX: a dedicated "New project" dialog on the General tab, vs. a
   "+ New project" row in the switcher. (Lean: General tab dialog first.)
2. Should chart-defined member projects land in Phase 3 or a follow-up? (Lean:
   follow-up — UI-defined aggregates first.)
3. Aggregate marker in the switcher: icon vs. label suffix. (Cosmetic; decide in
   UI task.)
4. Demo: is the `demo` project a `db` entry seeded on demo installs, or a `config`
   project (read-only)? (Lean: `config` on the demo instance — it's deployment-
   owned, not meant to be edited/deleted from the UI.)
