# AEP: Declared service metadata — self-service tier, environment, and domain

- **Date:** 2026-07-28
- **Author(s):** Berny ryders
- **Status:** Accepted — implemented 2026-08-23

## Summary

Let a service declare its own grouping metadata as OTLP resource attributes —
logical domain (`service.namespace`), environment (`deployment.environment.name`),
and criticality (`avuru.tier`) — instead of requiring an operator to enumerate
every service in the hub's `serviceGroups` ConfigMap. Group identity becomes the
pair *(domain, environment)*, so one board shows `valife-financial [prod]` at T0
next to `valife-financial [staging]` at T2. Every attribute is optional and
additive: an install where nothing is declared behaves exactly as it does today.

## Motivation

[AEP 2026-07-18](2026-07-18-service-health-groups.md) shipped a hybrid model:
config groups win, unmatched services auto-group by Kubernetes namespace. That
AEP also predicted the failure mode we are now seeing in the field:

> **Unlabeled services** (pure-SDK apps setting neither namespace attribute)
> fall into an `(unlabeled)` auto-group until config names them explicitly.

A real deployment of a dozen-plus Spring Boot services hits this exactly. The
apps export OTLP **directly to the gateway**, so they never pass through the
sensor's `k8sattributes` processor and carry no `k8s.namespace.name`; they set
`service.name` and `service.version` and nothing else. Result: every service
collapses into a single `(unlabeled)` group at the default tier. The board is
technically correct and operationally useless.

The namespace axis also cannot express what operators actually group by. That
deployment runs everything in two Kubernetes namespaces (`avuru-services`,
`avuru-services-staging`), so namespace auto-grouping — even when the attribute
is present — yields one bucket per environment rather than per capability. The
grouping people want (`financial`, `identity`, `platform-edge`) cuts *across*
namespaces, and the same capability needs a different tier per environment.

Today both of those require an operator to hand-maintain a list of every service
name in the ConfigMap, and to re-edit it on every new service. The service
already knows what it is; it should be able to say so.

### Goals

- A service declares its own domain, environment, and tier via resource
  attributes, and the board picks them up with no hub config.
- Groups span Kubernetes namespaces by construction.
- The same domain carries a different tier per environment, driven by the
  deployment overlay that already differentiates environments.
- Operators keep the last word: config overrides any declaration.
- Zero behavior change for installs that declare nothing.

### Non-goals

- No change to tenants/projects. Environment becomes a *group dimension*;
  `gateway.tenantByNamespace` stays for operators wanting hard data isolation.
- No per-project `serviceGroups` config.
- No cross-environment status propagation (a failing prod group never colors a
  staging group, and vice versa).
- No new schema or migration — this reads attributes already stored on spans.

## Solution

### The declared contract

Three optional resource attributes:

| Attribute | Meaning | Source |
|---|---|---|
| `service.namespace` | logical domain → group name | OTel semconv |
| `deployment.environment.name` | environment dimension | OTel semconv |
| `avuru.tier` | criticality `T0`–`T3` | **proprietary** (see below) |

Semconv renamed `deployment.environment` to `deployment.environment.name`. SDKs
in the field emit both, so the environment resolves
`deployment.environment.name` → `deployment.environment` → `""`, matching the
existing `namespaceOf` fallback style.

Applied in a Spring Boot service, where per-environment values come from the
Kustomize overlay that already patches `SERVICE_NAME`:

```yaml
management:
  opentelemetry:
    resource-attributes:
      service.name:                ${SERVICE_NAME:valife-financial-service}
      service.namespace:           ${SERVICE_DOMAIN:valife-financial}
      deployment.environment.name: ${DEPLOY_ENV:staging}
      avuru.tier:                  ${SERVICE_TIER:T2}
```

Grouping needs **no hub change at all**: `namespaceOf` already resolves
`k8s.namespace.name` → `service.namespace` → `(unlabeled)`. Declaring
`service.namespace` as a logical domain simply uses the existing fallback for
its stated purpose. Only tier and environment are new.

### Group identity becomes (domain, environment)

`groupAndRoll` buckets by a bare group string. It becomes a composite key
rendered as the domain with an environment badge:

```
Tier 0
  valife-financial [prod]      ● Healthy   4 svc
Tier 2
  valife-financial [staging]   ● Healthy   4 svc
```

A service declaring no environment attribute gets env `""`, the key collapses to
the bare domain, and no badge renders — today's behavior, unchanged.

### Tier resolution

Most specific wins:

1. `serviceGroups.tierOverrides[<service>]` — **new**, a service→tier map
2. config group selector match → that group's `tier` — existing
3. declared `avuru.tier`
4. `defaultTier`

`tierOverrides` exists because today the only way to override a tier is to
define a config group, which also forces group membership: an operator cannot
correct one service's tier without renaming its group.

**Conflict rule.** Members of one *(domain, environment)* may declare different
tiers. **Most critical wins** — a group containing a T0 service is a T0 group.
Understating criticality is the dangerous direction.

Tier provenance gets its **own** field rather than extending `Source`.
`Source` describes how *group membership* resolved (`"config"` = matched a
selector, `"auto"` = derived from a namespace/domain), and a group formed from a
declared `service.namespace` is still `"auto"`. Overloading it with
`"declared"` would leave `source: "declared"` ambiguous between a declared group
and a declared tier. So a new `TierSource` carries
`"override" | "config" | "declared" | "default"`, and `Source` keeps its
existing two values unchanged.

### Validation: config fails loud, declarations fail soft

`ParseConfig` deliberately crashes the hub on an invalid tier — config is
operator-controlled, and a typo must not silently misgroup.

Declarations get the **opposite** treatment. They arrive from application
telemetry, over which the operator has no review gate. One team shipping
`avuru.tier: T9` must never take down the health board for everyone. An
unrecognised declared tier falls back to `defaultTier` and is surfaced as a
warning on the response — never an error, never a crash. This mirrors the
defensive posture the storage layer already takes toward ingested data.

### Components touched

- **Storage** — `ServiceLabel` gains `Environment` and `DeclaredTier` (raw
  string; the health package validates it). `ServiceLabels`
  already resolves dominant values via `argMax(value, count())`; this is two
  more columns in the same subquery. No new query, no migration; the
  `ResourceAttributes` bloom indexes from migration 0001 already cover it. All
  SQL stays inside the ClickHouse package, behind `storage.Store` (locked
  decision 3).
- **Health** — `resolve` reads declared tier; `groupAndRoll` keys on the pair
  and applies the conflict rule; `Config` gains `tierOverrides`.
- **API** — `HealthGroup` gains `environment` and `tierSource`; response gains
  `warnings`.
- **UI** — env badge on group cards, tier-provenance affordance from
  `tierSource`, warnings affordance. Tier lanes unchanged.
- **Chart** — `serviceGroups.tierOverrides` in values and `values.schema.json`.

### Name-keyed consumers of group identity

Three call sites address a group **by name**, and all three must agree once
identity becomes a pair:

| Consumer | Today | With environments |
|---|---|---|
| `GET /api/v1/health/groups/{name}` | first `g.Name == name` | matches all envs; optional `?environment=` narrows |
| `alerting.rules[].selector.groups` | name match | matches all envs; optional `environments: [prod]` |
| `green.budgets[].group` (via `health.Assign` → `usedKgByGroup`) | name-keyed map | matches all envs; optional `environment:` |

The uniform rule: **`GroupHealth.Name` stays the domain** and a new
`Environment` field carries the dimension. Uniqueness and sort order use a
derived key (`name` when env is empty, `name[env]` otherwise), but *matching*
is by name. A selector that names no environment therefore matches every
environment — existing rules and budgets keep firing on the same set, which is
what backward compatibility demands.

Called out because a rule's or budget's blast radius widens the moment services
start declaring an environment: `valife-financial` becomes two groups, and an
unnarrowed rule now watches both. This is the correct default (silently
dropping the new group would be worse), but it is a behavior change worth
noting in the changelog.

**Alerting needs more than a match rule.** `buildTargets` keys addressable
targets by `"group:"+Name` into a **map**, so two environments of one domain
would collide and one environment's alerts would vanish silently. Group targets
therefore key on the composite identity — `group:payments[prod]` — while an
environment-less group keeps its bare `group:payments` key so existing rules and
stored `alert_state` rows stay valid. Selector matching splits the name back out
and applies the environment narrowing to it.

### Tension with locked decisions

Two deviations, both deliberate:

**"The wedge is law" — zero app changes.** This asks apps to set attributes,
which the wedge exists to avoid. Justification: the wedge measures
*time-to-first-data* — fresh cluster to live service map in under five minutes —
and this changes none of it. Every attribute is optional; declaring nothing
gives exactly today's zero-config board. This is a **day-2 refinement** for
operators who have outgrown namespace auto-grouping, not an install step. It
adds no install friction and delays no first data. eBPF-instrumented services
remain fully zero-config and are unaffected.

**"OTel semantic conventions everywhere — no proprietary formats."**
`service.namespace` and `deployment.environment.name` are semconv. `avuru.tier` is
not, because semconv defines no criticality attribute — the concept is a
business judgement OTel does not model. It is namespaced under `avuru.` so it
never collides with a future semconv key, and it is the *only* proprietary
attribute introduced. If OTel later standardises criticality, the resolution
chain gains a layer and `avuru.tier` becomes the fallback.

### Alternatives considered

- **Operator config only, with richer selectors** (match on arbitrary resource
  attributes, per-project group blocks). Keeps T0 a governed decision and needs
  no app changes — but leaves membership hand-maintained, which is the problem
  we are solving, and per-environment tiers would need a per-project config
  layer that does not exist.
- **A dedicated `avuru.group` attribute** instead of reusing
  `service.namespace`. Unambiguous and self-documenting, but adds a third layer
  to `namespaceOf`, introduces a second proprietary attribute, and abandons a
  semconv field that already means precisely this.
- **Service declares domain only; operator maps (domain, env) → tier.** Cleanest
  separation of identity from criticality, and keeps T0 governed. Rejected
  because per-environment tiering then needs new per-project hub config, whereas
  declaration gets it free from overlays that already differentiate environments
  — and `tierOverrides` preserves operator authority anyway.
- **Environments as separate projects/tenants.** Reuses the existing isolation
  axis, but requires fixing tenant stamping first (`tenantByNamespace` keys on
  the same `k8s.namespace.name` these apps do not emit), and makes cross-
  environment comparison impossible — you would view one environment at a time.

## Verification

- **Unit** (`hub/internal/health`): the four-step precedence chain;
  most-critical-wins across disagreeing members; invalid declared tier →
  `defaultTier` + warning, never an error; missing environment attribute →
  collapsed key (backward compatibility); config group still beats a
  declaration.
- **Integration** (testcontainers ClickHouse): `ServiceLabels` returns the
  dominant environment and tier per service, weighted by span count, over the
  same entry-span population as `ListServices`.
- **e2e**: seeded two-environment stack; assert two groups for one domain, in
  the correct tier lanes, with env badges; assert a declaration-free stack
  renders identically to today.
- **Chart**: `template-test.sh` covers `tierOverrides` rendering and schema
  rejection of a bad tier.
- **Manual**: `kubectl edit` the groups ConfigMap, add a `tierOverrides` entry,
  confirm the board re-tiers within ~15s without a restart.

Done means: a service that declares nothing behaves exactly as before; a service
that declares domain + environment + tier lands in the right group and lane with
no hub config; an operator can override any declaration.

## Roadmap

- [x] AEP accepted
- [x] Storage: `ServiceLabel` gains `Environment` + `Tier`; `ServiceLabels` SQL
- [x] Health: declared-tier resolution, composite group key, conflict rule,
      `tierOverrides`, soft validation + warnings
- [x] API: `environment` on `HealthGroup`, `warnings` on the response
- [x] UI: env badge, `declared` tier-source badge, warnings banner above the
      lanes — a declaration that failed soft has to be findable, or a team
      never learns theirs was ignored
- [x] Chart: `serviceGroups.tierOverrides` + `values.schema.json`
- [x] Name-keyed consumers: `?environment=` on the group-detail endpoint,
      optional `environments` in the alerting rule selector, optional
      `environment` on green budgets — **and the UI's React keys**, which were
      still name-only and therefore collided the moment one domain appeared in
      two environments (found on the live board, fixed here)
- [x] Docs: declared-attribute contract in `deploy/helm/README.md`; supersession
      note on [AEP 2026-07-18](2026-07-18-service-health-groups.md)
- [x] Seeded two-environment fixture + Playwright coverage of the split, the
      declared badge and the fail-soft warning
- [ ] docs-align (EN/FR)
