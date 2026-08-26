# AEP: Virtual targets — databases, caches and brokers on the map

- **Date:** 2026-08-23
- **Author(s):** avuru-obs maintainers
- **Status:** Accepted

## Summary

The service map draws the things that *send* telemetry. A cluster's most
consequential dependencies usually don't: PostgreSQL, Redis and Kafka have no
OTel SDK in them and no eBPF sensor of their own, so today a service that spends
80 % of its latency in a database is drawn as a lonely circle with no
explanation. This AEP adds **virtual targets** — nodes derived entirely from the
*caller's* exit spans, which are already stored. No new agent, no new table, no
new collection: the same derived-in-database shape as error tracking.

## Motivation

Two failures the current map cannot show:

1. **"Where is the time going?"** A slow service with no outgoing edge looks
   self-inflicted. If its p95 is 300 ms and 260 ms of that is one Postgres call,
   the map should say so — the edge is the answer.
2. **"What breaks if this breaks?"** Four services sharing one Redis is a blast
   radius nobody can see when the cache isn't on the map.

Both are answerable from data already in `otel_traces`. Not answering them is a
rendering choice, not a data limitation — which is what makes this the highest
value-per-risk item on the v0.8 line.

It also strengthens the [wedge](../AGENTS.md): OBI instruments the SQL and Redis
wire protocols in the kernel, so on a zero-code install the database appears on
the map in the first five minutes with nothing to configure.

### Goals

- Databases, caches and message brokers as first-class map nodes, derived from
  exit spans, with call volume, latency and error rate on the edge.
- Both directions of a broker: `producer → broker → consumer`, so a queue is
  never drawn as a dead end.
- Zero new collection, zero schema change, zero new module.
- A non-database install renders byte-identically to before.

### Non-goals

- **Plain HTTP exits are out of scope.** An unmatched HTTP client span is
  usually a third-party API, and admitting all of them puts every CDN, auth
  provider and metrics endpoint on the map at once. The map would get less
  readable, not more. Revisit with an explicit allowlist if asked for.
- **No health verdict for a virtual target.** We observe it only through its
  callers; the service-health rollup has no members for it. Its ring stays
  neutral and the failure shows on the *edge*, which is exactly what we know:
  calls to it are failing.
- **No per-target drill-down** in this AEP. Clicking a service opens its traces;
  a virtual target has no service to open, and filtering traces by `db.system`
  today matches the trace's root span, which would silently return the wrong
  traces. Better to do nothing than to answer a different question.

## Solution

### Deriving the node

One grouped scan of `otel_traces` over the map's window:

| Span shape | Meaning | Edge |
|---|---|---|
| `SpanKind IN ('Client','Producer')` and a database/messaging system attribute | the service called out to infrastructure | service → target |
| `SpanKind = 'Consumer'` and a messaging system attribute | the service was delivered a message | target → service |

The **classification is the filter**: a span is admitted only if it names a
system in `db.system.name` / `db.system` (current and prior semconv keys) or
`messaging.system`. Nothing else qualifies, so no anti-join against child spans
is needed — a database does not emit OTLP, so an exit span naming one has no
instrumented callee to double-count. (A DB *proxy* that emits spans and declares
`db.system` would be drawn twice; documented, not defended against.)

`kind` is derived from the system name: `redis`/`valkey`/`memcached` →
**cache**, any other `db.*` system → **database**, `messaging.system` →
**queue**. An unknown system is still a database — a node named for a system we
don't recognise is more useful than no node.

### Naming — why a URI

The node id must be stable across restarts, meaningful on sight, and **unable to
collide with a `service.name`** (a collision would merge a real service and a
database into one graph node). So the identity is a URI:

```
postgresql://orders-db        redis://session-cache        kafka://broker-0
```

The peer is the first non-empty of `server.address`, `net.peer.name`,
`db.namespace`, `db.name`, and — for messaging only — `messaging.destination.name`.
With none of them the node degrades to the bare system (`postgresql`), which is
still true, just less specific. **The port is deliberately excluded**: it splits
one database into several nodes and adds nothing a reader wants.

All destinations on one broker collapse into one node on purpose. The map's
subject is *the dependency*; per-topic detail belongs on a screen that has room
for it.

### Wire shape

Virtual targets ride the existing service-map response. A node carries
`role: "virtual"` (the field the mesh classifier already added in v0.7) plus a
new `kind`; edges are ordinary trace edges with call volume, error rate and
client-side p95. Nothing else changes, so a cluster with no database gets the
same bytes it did before, and the CLI and Grafana data source — which read
`/api/v1/services`, not the map — are untouched.

Result cardinality is capped server-side (top 200 caller→target pairs by call
volume). A cap that bites is a bug in the naming rule, not a busy cluster.

### Rendering

- **Shape**, not the ring: a virtual target is a barrel in the neutral tone.
  The ring stays health's channel, and the primary-filled hexagon keeps
  meaning "application". This is the same rule the v0.7 transport diamond
  follows.
  > Amended 2026-08-26 (v0.11): shipped in v0.8 as a hexagon against circular
  > application nodes. The application shape became a hexagon, so this one moved
  > to a portrait barrel — the closest cytoscape gets to the cylinder that reads
  > as a datastore. The rule above is unchanged; only the glyph it assigns is.
- The six existing channels — ring/health, fill/identity, size/rate,
  halo/carbon, width/calls, line colour/network+errors — are untouched.
- A toolbar toggle hides them, URL state like every other map filter, and the
  toggle only appears when the cluster actually has one.
- Size is rate, as for any node, so a chatty cache reads as chatty.

## Alternatives considered

- **A new `virtual_targets` table written at ingest.** Faster reads, but it adds
  a collector processor, a migration and a second source of truth for something
  the spans already say. Rejected for the same reason error tracking derives its
  issues rather than storing them.
- **Anti-joining child spans to find every unmatched exit.** More general —
  it would catch plain HTTP too — but it costs a self-join on every map load and
  its extra yield is the third-party endpoints the non-goals exclude.
- **Naming the node after the peer alone (`orders-db`).** Shorter, but a
  service and a database can then share a node id, which silently merges two
  unrelated things into one circle.

## Verification

- **Unit (Go):** naming and classification are table-driven — semconv key
  fallbacks, cache-vs-database, direction from span kind, degradation to the
  bare system, and that a map with no virtual targets is unchanged.
- **Integration (ClickHouse):** exit spans in, expected nodes and edges out,
  including the producer/consumer pair collapsing onto one broker node.
- **E2E (Playwright):** a seeded fixture adds a Postgres call, a Redis call and
  a Kafka produce/consume pair; the map's count line and legend must move, and
  the toggle must round-trip through the URL.
- **Wedge:** unchanged — no new collection, no new install step.

## Roadmap

- [x] AEP accepted
- [x] Storage derivation + integration test
- [x] API node/edge assembly + unit tests
- [x] Map rendering, legend, toggle
- [x] Seeded fixture + e2e coverage
