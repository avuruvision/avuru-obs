# AEP: Service groups authored in the UI

- **Date:** 2026-08-07
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

Let an admin create, edit and delete **service health groups** — name,
criticality tier, and the namespace/service selector — from the app, instead of
editing `serviceGroups` in `values.yaml` and redeploying. Chart-declared groups
keep working and render read-only; UI-authored groups live in a
`service_group` table and are editable. Auto-grouping by namespace is
unchanged, so an install that configures nothing still sees the groups it sees
today.

## Motivation

[Service health groups](2026-07-18-service-health-groups.md) shipped in v0.2
with exactly one way to define a group: `serviceGroups` in chart values,
rendered into a ConfigMap the hub hot-reloads. That is the right primitive for
a GitOps install and the wrong one for everything else. Today, deciding that
`payments` is T0 means editing values, running `helm upgrade`, and having the
cluster permissions to do it — so in practice nobody does, and the Service
Health screen shows a single `(unlabeled)` auto group with every service in it.
The feature is installed and unused.

This is the same gap [projects](2026-07-27-projects-completion.md) closed in
v0.3 and alert channels closed in v0.2: configuration that must be *operated*,
not just *declared*. Both landed on the same shape — chart-declared entries
merge in read-only, UI-authored rows are CRUD — and this reuses it rather than
inventing a third.

It ties to the wedge indirectly but really: the promise is a live service map
in five minutes with no app changes, and the first thing a new user wants after
that is to say which of those services matter. Making them redeploy to answer
is friction in the first five minutes of the product.

### Goals

- **CRUD for groups** (admin only): name, tier (T0–T3), selector
  (namespaces and/or services), persisted in ClickHouse.
- **Chart-declared groups keep working**, render read-only, and win a name
  collision — an install that manages groups in Git must not have them silently
  overridden from the UI.
- **Auto-grouping preserved**: services matched by no group still fall into
  their namespace group, exactly as now.
- **One resolution path**: the API and the alerting evaluator must agree on the
  group set, or alerts fire on a different grouping than the screen shows.
- **Validated against the existing schema**: reuse `health.Config.Validate`
  rather than re-implementing tier parsing at the API edge.

### Non-goals

- **Editing thresholds, critical edges or the default tier from the UI.** They
  are in the same config object but are a different (and more dangerous)
  surface; groups are the part people need daily. Later, on this same pattern.
- **Per-project groups.** Groups stay instance-wide in v1, as they are today.
- **Writing the ConfigMap back from the hub.** See Alternatives.
- **Importing existing chart groups into the DB.** They keep working where they
  are; a "copy to editable" affordance is a possible follow-up, not v1.

## Solution

**Storage.** A `service_group` table (migration `0016`), same
`ReplacingMergeTree(UpdatedAt)` + tombstone shape as `project` and
`alert_channel`:

```sql
CREATE TABLE IF NOT EXISTS {db}.service_group
(
    `Name`       String,
    `Tier`       String,
    `Namespaces` Array(String) DEFAULT [],
    `Services`   Array(String) DEFAULT [],
    `Deleted`    UInt8 DEFAULT 0,
    `CreatedBy`  String,
    `CreatedAt`  DateTime64(3) DEFAULT now64(3),
    `UpdatedAt`  DateTime64(3) DEFAULT now64(3)
)
ENGINE = ReplacingMergeTree(UpdatedAt)
ORDER BY (Name);
```

`Name` is the identity (it already is, in the config schema), so an edit is an
upsert and a delete is a tombstone — no surrogate id, and `FINAL` + the
`Deleted` filter reads the live set, as with the sibling tables.

**Resolution — the part that must not be got wrong.** `api.Config.GroupsConfig`
is `func() health.Config`, called per request, which is what makes the ConfigMap
hot-reload visible without a restart. The alerting evaluator receives the *same*
provider directly from `main.go` — it does not go through the API. So merging
stored groups inside `api.groupsConfig()` would give the UI one grouping and
alerting another: a T0 group created in the UI would show as critical on
`/health` and never page. Instead the merge lands in **one resolver** that wraps
the config provider and the store, and `main.go` passes that single resolver to
both the API and the evaluator.

Precedence mirrors `handleProjects`: **config wins per name**, DB fills the
rest. A group carries a `source` (`"config"` | `"db"`) and an `editable` flag on
the wire, so the UI marks chart-declared rows read-only instead of offering an
edit that would be silently overridden on the next reload.

Reading the table on every health request is heavier than reading an in-memory
config, so the resolver memoizes for a few seconds — the `tenantCache` pattern
already used for `ListTenants` (30s). Writes invalidate it, so an admin's own
edit is never stale to them.

**API** (`hub/internal/api/groups.go`, behind the existing `securedAdmin`
middleware for writes; reads follow the existing `/health` authorization):

- `GET /api/v1/service-groups` — merged list, each row tagged with its source.
- `POST /api/v1/service-groups` — create; 409 on a name that a config group
  already owns (fail loudly rather than write a row that will never take effect).
- `PUT /api/v1/service-groups/{name}` — update; 403 on a config-declared name.
- `DELETE /api/v1/service-groups/{name}` — tombstone; 403 on config-declared.

Every write validates through `health.Config.Validate` on the *merged* result,
so an API caller cannot store a group the ConfigMap loader would reject at boot.

**UI.** A group editor (name, tier, namespace/service selectors) following the
alert-channel precedent — `channels-panel.tsx` + `channel-form.tsx` — including
how it renders config-defined rows as read-only. Reachable from Settings and
from the Service Health screen, which is where the need is felt.

```
values.yaml ──▶ ConfigMap ──hot-reload──▶ config provider ─┐
                                                           ├─▶ resolver ─▶ API + alerting evaluator
Settings UI ──▶ service_group (ClickHouse) ────────────────┘   (config wins per name)
```

### Alternatives considered

- **Have the hub write the ConfigMap.** Round-trips through Kubernetes and needs
  cluster write access on a resource outside the narrow, `resourceNames`-scoped
  Role the [collection control plane](2026-07-27-collection-control-plane.md)
  deliberately settled on. It would also fight any GitOps controller that owns
  that ConfigMap — the write would be reverted, invisibly.
- **DB wins the collision.** Tempting ("the more recent, deliberate action"),
  but it means a Git-managed install can be silently overridden from a browser
  and the drift only shows at the next `helm upgrade`. Config winning makes the
  conflict visible at write time instead.
- **Merge inside `api.groupsConfig()`.** One line smaller and quietly wrong: the
  alerting evaluator would keep the config-only view. See above.
- **A new `group_id` surrogate key.** Names are already the identity in the
  config schema and in the API; adding a second one buys renames and costs a
  join plus two sources of truth for the same thing.

## Verification

- **Unit**: precedence (config beats db on a shared name), tombstoned rows
  excluded, `Validate` rejecting a bad tier through the API, cache
  invalidated by a write.
- **Integration** (`-tags integration`, real ClickHouse): upsert-by-name,
  tombstone delete, and that `FINAL` reads the latest row — the same coverage
  `project` and `alert_channel` have.
- **The one that matters**: a group created through the API changes what the
  *alerting evaluator* resolves, not only what `/health` returns. This is the
  drift the single-resolver design exists to prevent, so it gets an explicit
  test rather than being left to inspection.
- **e2e (Playwright)**: create a group from the UI, see it on `/health` with its
  tier; a chart-declared group renders read-only with no edit control.
- **Done** = an admin creates a T0 group in the UI, the Service Health screen
  shows it in the T0 lane, and an alert rule on that group fires — with no
  `helm upgrade` anywhere in the story.

## Roadmap

- [ ] AEP accepted
- [ ] Migration `0016_service_groups.sql` + storage CRUD (list/save/delete)
- [ ] Merge resolver shared by the API and the alerting evaluator
- [ ] Admin-gated CRUD endpoints + source/editable on the wire
- [ ] Settings group editor (alert-channel precedent), read-only config rows
- [ ] Unit + integration + the evaluator-agreement test + Playwright
- [ ] `docs-align` (EN/FR)
