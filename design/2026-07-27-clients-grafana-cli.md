# AEP: Additional clients — Grafana data source + CLI (and the API-token seam)

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Draft
- **Note:** the **API-token seam** described here is narrowed and superseded by
  [2026-08-13 API tokens](2026-08-13-api-tokens.md), which ships tokens on their
  own. The Grafana data source and the CLI remain this AEP's, unbuilt.

## Summary

Prove the Hub API is a client-agnostic contract by shipping two more clients: a
**Grafana data source plugin** (query Avuru Obs from existing Grafana dashboards)
and a **CLI** (`avuruobs`) for scripts, CI and terminal debugging. Both ride the
existing REST/WS API. Because neither is a browser with a cookie session, this AEP
also introduces the **API-token seam** the auth AEP deferred: non-interactive
**personal/service tokens** that authenticate on the same session+RBAC layer.

## Motivation

The roadmap states it plainly: "the Hub API is the client-agnostic contract; the
SPA is one thin client." The best way to keep that true is to have more than one
client. Teams live in Grafana; a data source lets them pull Avuru Obs traces/
metrics/service-health into dashboards they already run — meeting them where they
are without abandoning the single storage engine. A CLI unlocks scripting, CI
gates ("fail the deploy if error rate > x"), and fast terminal checks. Both need
**non-interactive auth**, which the [auth AEP](2026-07-21-auth-oidc-rbac.md)
explicitly deferred as "additive later on the same session layer" — this is that
addition. Ties to the [enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass):
tokens reuse the auth provider + grants, no parallel authz.

### Goals

- **API tokens**: user-owned bearer tokens (hashed at rest, shown once, scoped to
  the creator's grants, revocable) accepted as `Authorization: Bearer` by the
  same middleware that validates sessions. Service tokens (owned by a synthetic
  service identity with explicit grants) for CI.
- **Grafana data source**: a plugin exposing the core reads — service list, RED
  metrics, traces (TraceQL-ish/search), service-health status — mapping Grafana
  queries to Hub API calls; token-authenticated.
- **CLI (`avuruobs`)**: `login`/token config, `services`, `traces`, `logs`,
  `health`, `status`; JSON + human output; a `--fail-on` predicate for CI gates.
- **One contract**: both clients use the public, versioned Hub API only — no
  private endpoints, no direct ClickHouse.

### Non-goals

- **OAuth device-code flow for the CLI** in v0.2 — token paste/config first;
  device flow can follow once OIDC (Plan B) is in.
- **A Grafana panel plugin / full trace-waterfall in Grafana** — the data source
  feeds Grafana-native panels; a bespoke panel is later.
- **Write operations from Grafana** — the data source is read-only.
- **Publishing to the Grafana plugin catalog / Homebrew** in v0.2 — build +
  release artifacts; catalog submission is a follow-up.

## Solution

**API-token seam.** A `auth_tokens` table (ReplacingMergeTree + FINAL + tombstone):
`hash` (hex sha256), `userId` (owner or service identity), `name`, `prefix`,
`createdAt`, `lastUsedAt`, `revoked`. `requestIdentity` (auth middleware) gains a
branch: no session cookie but an `Authorization: Bearer <token>` → resolve the
token to its owner's `Identity` (same grants, same project enforcement). Admin +
self endpoints `GET/POST/DELETE /api/v1/tokens` (secret shown once). This is the
minimal, honest extension of the auth layer — tokens are just another way to
arrive at an `Identity`; RBAC and project scoping are unchanged.

**Grafana data source.** A standard Grafana plugin (TypeScript frontend + Go
backend `grafana-plugin-sdk-go`) in a new `clients/grafana-datasource/`. It stores
the hub URL + an API token, and translates Grafana's query model to Hub API calls:
a "service RED" query → `/api/v1/services` + `/red`; a "traces" query →
`/api/v1/traces` search; a "service health" query → the health status endpoint.
Read-only; respects the token's grants (a Viewer token sees only granted projects).

**CLI.** A Go binary in `clients/cli/` (Cobra), config at `~/.avuruobs/config`
(hub URL + token), commands mapping 1:1 to API resources, `-o json|table`, and
`--fail-on 'errorRate>0.05'` for CI. Distributed as a static binary from the
release workflow (goreleaser-style), `go install`-able.

```
Grafana ──plugin──▶ Hub API (Bearer token) ─┐
CLI     ──httpc──▶ Hub API (Bearer token) ─┤─▶ same auth middleware → Identity+grants
SPA     ──cookie─▶ Hub API ─────────────────┘
```

Both clients live under a new top-level `clients/` (polyrepo-at-build-level, each
owns its toolchain — consistent with the monorepo structure), so they don't
weigh on the hub/ui build.

### Alternatives considered

- **A separate token service / API gateway** — over-engineered; tokens resolving
  to the existing `Identity` reuse all authz. No new component (the wedge).
- **ClickHouse data source for Grafana instead of a hub plugin** — bypasses the
  Hub API contract and the storage-behind-the-interface decision, and leaks SQL
  schema into dashboards; the plugin keeps the contract in one place.
- **CLI shells out to `kubectl`/`curl`** — no typed contract, no CI predicate;
  a real client is small and pays for itself.
- **Cookie/session reuse for the CLI** — awkward for non-interactive use and
  short-lived; tokens are the standard answer and were always the planned seam.

## Verification

- **Unit**: token resolution in the middleware (valid/revoked/unknown → identity
  or 401); token grants == owner grants; CLI output formatting + `--fail-on`
  predicate parse.
- **Integration (ClickHouse)**: token store FINAL/tombstone; `lastUsedAt` update.
- **e2e (compose)**: create a token in the UI, use it from the CLI to list
  services and assert a `--fail-on` gate; the Grafana plugin (headless) runs a
  service-RED query against the stack and renders a series.
- **Grafana**: the plugin loads in a real Grafana, configures a data source, and
  a dashboard panel shows Avuru Obs RED.
- **Done** = a token created in the UI authenticates both a Grafana data source
  (read-only, grant-scoped) and the `avuruobs` CLI, and a CI job can fail on a
  health predicate — all through the public Hub API.

## Roadmap

- [ ] AEP accepted
- [ ] `auth_tokens` store + Bearer-token branch in the auth middleware
- [ ] `GET/POST/DELETE /api/v1/tokens` (one-time secret) + Settings → Tokens UI
- [ ] CLI (`clients/cli/`): auth, services/traces/logs/health/status, `--fail-on`
- [ ] Grafana data source (`clients/grafana-datasource/`): RED, traces, health
- [ ] Release artifacts (CLI binaries, plugin zip); e2e; docs-align (EN/FR)
