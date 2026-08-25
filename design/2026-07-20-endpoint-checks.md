# AEP: Endpoint checks — health when there is no traffic

- **Date:** 2026-07-20 (accepted 2026-08-25)
- **Author(s):** Berny ryders
- **Status:** Accepted

> **History.** Drafted 2026-07-16 as "Service groups and endpoint checks" (never
> merged; it lived on the `design/aep-service-groups` branch). The groups half
> was superseded by
> [2026-07-18 Service health groups](2026-07-18-service-health-groups.md)
> (shipped: tiered groups, composite status from RED, dependency propagation).
> Rescoped 2026-07-20 to the half that was never built: **active endpoint
> checks**, attached to the shipped groups.

## Summary

Add **endpoint checks** — scheduled HTTP probes against declared endpoints —
whose results feed the shipped service-health group status. Checks add the one
signal avuru-obs cannot derive from observed traffic: what happens when there is
no traffic. A group of services with zero spans in the freshness window is
either idle or dead; a probe is the only honest way to tell the two apart at
3 a.m.

## Motivation

avuru-obs observes *what happened* — spans in, RED out, service map, health
groups. It is silent on *what should be happening*:

- **Absence of traffic is indistinguishable from absence of service.** A service
  with no spans in the last 15 minutes shows a flat line whether it is idle or
  dead. `system_status.go` already had to invent an `idle` status
  (`ingestionFreshWindow`) to describe that ambiguity for our own backend; user
  services deserve the same honesty.
- The dogfooding install's run book defines six synthetic journeys and
  concludes *"the system is OK when the critical user journeys pass, not when
  the pods are green"*. A pod can be Running with a dead DB connection; a probe
  cannot.

Service health groups answered "which group is degrading, given traffic?".
Checks answer "is it serving, given silence?". No competitor joins probes with
the service map and traces in one product.

### Goals

- Declare HTTP checks (URL, interval, expectations) attached to a shipped
  service-health group; store results as a first-class signal, queryable like
  any other.
- Check results participate in the group's composite status; a group with no
  checks and no traffic reports `idle` — honest, not green.
- Config-first (the existing hot-reloadable `serviceGroups` config surface),
  UI second. Zero impact when unused: no checks declared → no scheduler, no new
  rows, byte-identical behaviour.

### Non-goals

- **Alerting.** The alerting module already fires on health transitions; checks
  only improve the state it fires on.
- **A public status page.** Downstream.
- **Replacing Kubernetes probes.** Liveness/readiness serve the orchestrator;
  checks serve humans.
- **Multi-step browser journeys** (login → navigate → assert DOM). Single-request
  checks only; scripted journeys are a later, larger question.
- **An SSRF guard on check targets.** The alerting module blocks private,
  loopback and link-local addresses on its webhooks, and copying that here would
  block the entire feature: a check exists to probe *your own* services, which
  live on exactly those networks. The two are not the same risk. A webhook URL
  is a delivery address pointed outward; a check URL is admin-authored
  configuration in the same ConfigMap that already decides what the platform
  collects. Whoever can write a check can already write the collection config.
  Checks do carry a hard timeout, refuse redirects to a different host, and are
  never populated from user input.

## Solution

A check is a scheduled HTTP request attached to a group, declared alongside the
existing `serviceGroups` config:

```yaml
    checks:
      - id: core-login
        url: https://app.example.com/api/health
        interval: 60s
        expect: { status: 200, max_latency: 800ms }
```

A scheduler goroutine in the Hub runs due checks, records outcome
(`ts, check_id, ok, status_code, latency_ms, error`) to a ClickHouse table with
the same TTL treatment as other signals, and emits **a span for its own
request**. That is the design's hinge: a check is not a side channel, it is
synthetic traffic. It appears in RED, on the service map, and in traces like any
other client — which the existing aux-span filter
(`hub/internal/storage/clickhouse/aux.go`) already knows how to hide from
user-facing RED when asked. One mechanism, reused, rather than a parallel health
system.

### The span-emission seam

A check emitting a span is the hinge of this design, and it puts the Hub in a
place the architecture is explicit about keeping it out of:
`agent_docs/architecture.md` locks **"the hub is never in the telemetry
byte-path"**.

That rule is about *relaying other people's telemetry*, and this does not break
it — but only if the mechanism is stated rather than assumed. The Hub becomes an
OTLP **client of the gateway**: it exports its own spans the way any
instrumented application does, over the same endpoint, through the same
receiver, past the same tenant stage. It never writes `otel_traces` itself,
even though it holds a ClickHouse connection and could.

That distinction is the whole safeguard. A direct insert would look simpler and
would quietly bypass ingest-key enforcement, per-project routing and every
transformation the pipeline applies — producing rows no sender could have
produced. Going through the front door means a check's span is subject to
exactly what a customer's span is subject to, including being rejected when the
key is wrong.

Consequences the implementation must carry:

- With `auth.ingest.mode=enforce`, the Hub carries an ingest key of its own,
  wired by the chart the way the sensor's key already is.
- With no gateway endpoint configured, checks still run and still record their
  results; only the span is skipped. The scheduler must not fail because an
  exporter is unset.
- Check spans are auxiliary traffic by construction, and the existing aux-span
  classification (`hub/internal/storage/clickhouse/aux.go`) is what keeps them
  out of user-facing RED — one mechanism, reused, exactly as the passive
  health-check spans already are.

API: `GET /api/v1/checks` / `GET /api/v1/checks/{id}/results`; check outcomes
feed the group evaluation in `hub/internal/health` (a failing check on a T0
group degrades it after 2 consecutive failures, not 1; a group with no checks
and no traffic in the freshness window reports `idle`).

### Alternatives considered

- **Blackbox exporter / Uptime Kuma alongside avuru-obs** — works, and is what
  most teams do. Rejected: it puts "is the product up?" in a tool that has never
  heard of the service map, so correlating a failed probe with the trace that
  explains it is a human copy-paste job. The join is the product.
- **Leave it to the workflow engine** (n8n hitting endpoints on a cron) — the
  tactical contournement the dogfooding install documents today. The results
  live nowhere queryable and every team rebuilds them.
- **Infer health from aux-spans** (the `/health` spans already recognised and
  filtered) — only works when *something else* is calling those endpoints.
  Passive recognition inherits the same silence-vs-death ambiguity.

## Verification

- **Unit**: scheduler due-time selection; config parsing with bad input; the
  2-consecutive-failures rule and `idle` in the health evaluation, table-driven.
- **Integration**: a check against a local test server — result rows land in
  ClickHouse, a span is emitted, stopping the server flips the group after
  exactly 2 failures.
- **E2E**: on the kind cluster, one check over the demo app; assert the group's
  status endpoint returns healthy, kill the demo app, assert it degrades. The
  wedge is unaffected: no checks declared → nothing scheduled.

"Done" = an operator can answer "is the core up?" at 3 a.m., with no traffic,
from one endpoint — and click through from the failing group to the trace of the
failed probe.

## Roadmap

- [x] AEP accepted
- [x] Check scheduler + config parsing on the `serviceGroups` surface
- [x] ClickHouse results table + span emission + aux-span classification
- [x] Health evaluation: check outcomes + `idle` for silent, check-less groups
- [x] `GET /api/v1/checks[/{id}/results]`
- [x] UI: check results panel on the `/health` board
- [x] kind e2e: a real probe against the wedge demo, its result row and its
      span asserted in ClickHouse
- [ ] Follow-up AEP: scripted multi-step journeys, if demand appears
