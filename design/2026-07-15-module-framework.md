# AEP: Module framework — opt-in signals end to end

- **Date:** 2026-07-15
- **Author(s):** Berny ryders
- **Status:** Accepted

## Summary

Introduce a first-class **module** concept so an operator can decide which
signal families their install runs — end to end. A module gates, from a single
switch, its schema migrations, its ingestion pipelines, its Hub API routes,
and its UI surface. Existing signals are reclassified as modules (`core`,
`logs`, `profiling`, `infra-metrics`) with **everything enabled by default**
(zero breaking change); future signals (error tracking, alerting) are born as
modules.

## Motivation

Modularity already exists in fragments: signal tiers in the architecture doc,
per-signal collection knobs in the Helm chart, and planned chart component
toggles. What's missing is one coherent concept — today "I don't want
profiling" cannot be expressed cleanly across schema, API, and UI at once.

Who feels it:

- **Small installs** want a lighter ClickHouse (no profiling/log volume) while
  keeping the wedge (service map + traces) intact.
- **The product** needs a stable seam for upcoming signals — error tracking is
  designed as the first born-opt-in module — and for a future edition split
  (modules are an operational choice, not a paywall line).
- **The wedge**: `core` (flows + service map + traces + RED) is never
  disableable; modules only ever *remove* optional weight, they never add
  install friction. Defaults keep today's behaviour bit-for-bit.

### Goals

- One switch per module (`modules.<name>.enabled` in Helm/compose →
  `AVURUOPS_MODULES` env) driving schema, pipelines, API, and UI together.
- A client-agnostic discovery contract: `GET /api/v1/capabilities`.
- Backward compatibility: default = all modules on; an existing install
  upgrades with no visible change.
- Enabling a module later is a config change + redeploy (`hub migrate` is
  idempotent and applies the newly-active module's DDL).

### Non-goals

- Dynamically loaded plugins (Grafana-style SDK, Wasm, `.so`): everything is
  compiled in; config activates. Revisit only if third parties need to ship
  modules without forking.
- Per-tenant module toggles — modules are per-install. The tenant column and
  the enterprise seam are orthogonal and unchanged.
- Reworking sensor collection knobs — the existing per-signal collection
  values stay; module values *drive* them (a disabled module implies its
  collection is off).

## Solution

A module is a named unit declared in one registry (`hub/internal/modules`) and
honoured at five layers:

| Layer | Mechanism |
|---|---|
| Helm/compose | `modules.<name>.enabled` value → renders `AVURUOPS_MODULES` (hub) and enables/disables the module's gateway pipeline fragments and sensor collection knobs |
| Gateway | single OCB binary contains all components; the *config* activates a module's receivers/pipelines |
| Hub schema | each migration in `migrations.Ordered` is tagged with its module; `hub migrate` applies only active modules' files (ledger unchanged — lexical order preserved; skipped files apply later if the module is enabled) |
| Hub API | module routes are registered only when active (404 otherwise); `GET /api/v1/capabilities` returns `{version, modules: [...]}` |
| UI | static SPA renders the sidebar from `/api/v1/capabilities`; direct navigation to a disabled module shows a "module disabled" page with enablement docs |

Module registry (initial):

| Module | Migrations | API routes | Notes |
|---|---|---|---|
| `core` | `0001_traces.sql`, `0005_span_index.sql` | services, service-map, traces, spans, RED, projects, status, system | **Not disableable** — this is the wedge (RED is trace-derived) |
| `logs` | `0002_logs.sql` | `/api/v1/logs`, `/api/v1/traces/{id}/logs` | |
| `infra-metrics` | `0003_metrics.sql` | `/api/v1/infra/*`, `/api/v1/agents` | agents inventory reads collector self-metrics from the metrics tables, so it belongs here |
| `profiling` | `0004_profiles.sql` | `/api/v1/profiles/*`, `POST /v1development/profiles` | |
| `error-tracking` *(future)* | `0006_errors.sql` | `/api/v1/errors/*` | first born-opt-in module — see the error-tracking AEP |

Enterprise seam: untouched. Tenant column, auth provider interface, and
retention policy objects behave identically; retention env vars for a disabled
module's tables are simply ignored (no table to alter).

### Alternatives considered

- **Always apply all migrations, gate only API/UI** — simplest, but derived
  tables with materialized views (error tracking) impose per-insert CPU cost
  even when unused; and "installed but dark" schema confuses operators.
- **Separate OCB distro per module set** — combinatorial build matrix for no
  gain; config gating of one binary is how collectors are meant to work.
- **Dynamic plugin loading** — heavy SDK/runtime investment, Go plugin
  fragility, no third-party demand yet.
- **Per-tenant modules** — conflates install topology with tenancy; the
  enterprise seam may revisit this later.

## Verification

- Hub unit/integration matrix: modules on/off → routes present/404,
  migrations applied/absent, `capabilities` exact.
- Compose/e2e: default stack behaves exactly as today (all on); a
  `profiling`-off install has no profiling tables, 404 profiling APIs, no
  sidebar entry.
- Helm e2e (kind): install with `modules.profiling.enabled=false`, assert via
  Hub API; the TTV gate stays green (modules never touch `core`).

## Roadmap

- [x] AEP accepted
- [x] Hub: module registry + conditional migrations + `/api/v1/capabilities` + conditional routes
- [x] Helm/compose: `modules.*` values wired to hub env, gateway config, sensor knobs
- [x] UI: capabilities-driven sidebar + disabled-module page
- [ ] Docs: Modules concept page (EN/FR), configuration reference
- [x] First born-opt-in module ships on top (error tracking)
