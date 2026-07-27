# AEP: Wider ingest compatibility — Jaeger, Zipkin, Prometheus, Loki + forwarding

- **Date:** 2026-07-27
- **Author(s):** Berny ryders
- **Status:** Draft

## Summary

Extend the drop-in promise beyond OTLP by enabling additional **push receivers**
in the gateway (Jaeger, Zipkin, Prometheus remote-write, Loki push) and
**forwarding exporters** (OTLP and Kafka) so an existing backend can dual-write
to Avuru Obs during a migration. All are upstream OpenTelemetry Collector
components added to the OCB manifest, each opt-in and off by default, so the
minimal distro stays minimal.

## Motivation

The wedge is "point your exporter at us, change only the endpoint" — today that
holds for OTLP (and Sentry, via the error-tracking module). Teams mid-migration
rarely start at OTLP: they have Jaeger agents, Zipkin libraries, Prometheus
remote-write, or a Loki push path. Making them re-instrument to *try* Avuru Obs
is exactly the friction the [wedge](../AGENTS.md) exists to remove. And a careful
migration wants to **dual-write** to the old and new backends until trust is
built — hence forwarding exporters. This directly serves the roadmap's "wider
ingest compatibility" and "OTLP drop-in replacement" promises without touching
the hub or the storage schema (everything normalizes to OTLP before ClickHouse).

### Goals

- **Receivers** (opt-in, per-protocol chart flags): `jaegerreceiver`
  (thrift/gRPC), `zipkinreceiver`, `prometheusremotewritereceiver`,
  `lokireceiver` — each normalizing to OTLP into the existing pipelines.
- **Forwarding exporters** (opt-in): `otlpexporter` and `kafkaexporter` so the
  gateway can dual-write to another backend during migration.
- **Off by default**: the default render is the current minimal distro; each
  receiver/exporter is a values flag that opens exactly one port/path.
- **Semantic-convention normalization**: incoming Jaeger/Zipkin/Prom/Loki map to
  OTel attributes so they land in the same tables and UI as native OTLP.

### Non-goals

- **Pull-based Prometheus scraping** (`prometheusreceiver` service discovery) —
  v0.2 is push (remote-write); scraping is a later, larger surface.
- **StatsD / collectd / Fluentd / other long-tail receivers** — add on demand.
- **Bidirectional sync / backfill of historical data** — forwarding is
  going-forward dual-write only.
- **New storage schema** — everything normalizes to OTLP; no new tables.

## Solution

**OCB manifest.** Add the four receivers and two exporters as pinned
`v0.154.0`-line `gomod` entries (the manifest already notes `jaegerreceiver` as a
transition aid). The distro grows only when an install turns a component on; the
binary carries them but the collector config only wires the enabled ones.

**Chart.** `gateway.receivers.{jaeger,zipkin,prometheusRemoteWrite,loki}.enabled`
and `gateway.forward.{otlp,kafka}` values render the corresponding receiver/
exporter config and open the ports. Each receiver feeds the existing
traces/metrics/logs pipelines (through `k8sattributes`, `resource/tenant`,
`transform/tenant`, and — when auth Plan C lands — the ingest-auth extension), so
tenancy and enrichment apply uniformly. Forwarding exporters are added to the
pipeline `exporters` list alongside `clickhouse`.

```
Jaeger/Zipkin/Prom-RW/Loki ─▶ gateway receiver ─normalize▶ OTLP pipeline ─▶ ClickHouse
                                                                  └─(opt)─▶ otlp/kafka forward
```

**Normalization.** Upstream receivers already emit OTLP with semconv mapping;
this AEP adds thin conformance tests (a Jaeger span and a Zipkin span land with
the same service/operation/status a native OTLP span would). Prometheus
remote-write lands as `otel_metrics_*`; Loki push lands as log records.

**Docs.** Each protocol gets a one-paragraph "migrate from X" note (the endpoint
to point at, the values flag) — the drop-in promise, made concrete per source.

### Alternatives considered

- **A single "compat" mega-flag** — turning on all receivers at once needlessly
  widens the attack/port surface; per-protocol flags keep the minimal-distro
  principle.
- **Separate sidecar collectors per protocol** — more moving parts than adding
  receivers to the one gateway distro OCB already builds.
- **Translate at the SDK/agent** — defeats the point; the value is *no* client
  change beyond the endpoint.
- **Prometheus scraping now** — service discovery + relabeling is a much bigger
  surface; remote-write covers the push migration case for v0.2.

## Verification

- **Component build**: `make gateway-image` builds the distro with the new
  receivers/exporters enabled in the manifest.
- **Unit/conformance**: a Jaeger span, a Zipkin span, a Prom remote-write sample,
  and a Loki push each land in the expected table with normalized attributes
  (fixtures → gateway → ClickHouse assertions).
- **e2e (compose)**: point a Jaeger-protocol sender and a Zipkin sender at the
  gateway with the flags on; assert both show on the service map / trace search;
  enable OTLP forwarding and assert a second backend also receives them.
- **Wedge/TTV gate**: unchanged (all flags off by default).
- **Done** = a Jaeger, Zipkin, Prometheus-remote-write, or Loki sender reaches
  Avuru Obs by changing only its endpoint + one values flag, and an install can
  dual-write to another backend during migration.

## Roadmap

- [ ] AEP accepted
- [ ] Add receivers (jaeger, zipkin, prometheusremotewrite, loki) to OCB manifest
- [ ] Add forwarding exporters (otlp, kafka) to OCB manifest
- [ ] Chart flags + pipeline wiring (per protocol, off by default)
- [ ] Conformance tests per protocol + compose e2e (Jaeger + Zipkin)
- [ ] "Migrate from X" docs per source; docs-align (EN/FR)
