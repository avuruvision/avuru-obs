# design/ — Avuru Enhancement Proposals (AEPs)

Design specs and RFCs for avuru-obs. An **Avuru Enhancement Proposal (AEP)** is a
short, durable document that captures the *why*, the decision, and the
alternatives for a significant change — so the rationale lives in one place
instead of scattered across review threads. The process is modeled on
[Kiali's KEPs](https://github.com/kiali/kiali/tree/master/design/KEPS), kept
lightweight.

The **living architecture** is
[`agent_docs/architecture.md`](../agent_docs/architecture.md); this directory
holds point-in-time design records that feed into it.

## When to write an AEP

Write one for **significant** changes — anything that adds or alters a
[locked decision](../agent_docs/architecture.md#locked-decisions-and-rationale),
touches the [enterprise seam](../agent_docs/architecture.md#enterprise-seam-do-not-bypass)
or the [wedge](../AGENTS.md), introduces a load-bearing dependency, or reshapes a
component. You do **not** need one for bug fixes, docs, tests, or routine
changes — see [GOVERNANCE.md](../GOVERNANCE.md#how-decisions-are-made).

Small features usually start as an issue or discussion and graduate to an AEP
only if the design needs to be pinned down before coding.

## Process

1. Copy [`TEMPLATE.md`](TEMPLATE.md) to `YYYY-MM-DD-<topic>.md` and fill it in.
2. Open a PR with the AEP (plus any diagrams/assets in the same change).
3. Maintainers review on the PR; the author addresses feedback with new commits.
4. When feedback is addressed, maintainers **accept or reject** (see
   [governance](../GOVERNANCE.md#how-decisions-are-made)). Accepted AEPs are
   broken into issues and implemented.
5. Keep the AEP as the historical record. **Supersede rather than rewrite** —
   when a later AEP changes an earlier decision, note it at the top of the old
   one and link forward.

## Index

| Date | Topic | Status |
|---|---|---|
| [2026-07-15](2026-07-15-module-framework.md) | Module framework — opt-in signals | Accepted |
| [2026-07-16](2026-07-16-error-tracking.md) | Error tracking — derived issues + Sentry ingest | Accepted |
| [2026-07-17](2026-07-17-sensor-safe-by-default.md) | Make the eBPF sensor safe to leave on | Accepted |
| [2026-07-18](2026-07-18-service-health-groups.md) | Service health groups — consolidated group health from RED | Accepted |
| [2026-07-19](2026-07-19-alerting.md) | Alerting — webhook notifications on service-health transitions | Accepted |
| [2026-07-19](2026-07-19-network-health.md) | Network health on the service map — per-edge RTT + connection failures | Accepted |
| [2026-07-20](2026-07-20-endpoint-checks.md) | Endpoint checks — health when there is no traffic | Accepted |
| [2026-07-21](2026-07-21-auth-oidc-rbac.md) | Authentication, RBAC and per-project ingest keys | Draft |
| [2026-07-22](2026-07-22-green-carbon.md) | Green — per-service energy and carbon attribution (Kepler) | Accepted |
| [2026-07-27](2026-07-27-projects-completion.md) | Projects completion — CRUD, per-project retention, status, chart toggles | Accepted |
| [2026-07-27](2026-07-27-collection-control-plane.md) | Runtime collection control plane — switch collection from the UI | Draft |
| [2026-07-27](2026-07-27-wider-ingest-compat.md) | Wider ingest compatibility — Jaeger/Zipkin/Prometheus/Loki + forwarding | Accepted |
| [2026-07-27](2026-07-27-auto-tagging.md) | Richer auto-tagging — K8s labels/annotations as business tags | Draft |
| [2026-07-27](2026-07-27-clients-grafana-cli.md) | Additional clients — Grafana data source + CLI + API tokens | Draft |
| [2026-07-28](2026-07-28-declared-service-metadata.md) | Declared service metadata — self-service tier, environment, and domain | Draft |
| [2026-07-28](2026-07-28-green-tdp-estimation.md) | Green TDP estimation — modeled energy for RAPL-less nodes | Accepted |
| [2026-08-06](2026-08-06-users-crud-password.md) | Users CRUD completion — delete, password management, role editing | Accepted |
| [2026-08-07](2026-08-07-service-groups-crud.md) | Service groups authored in the UI | Accepted |
| [2026-08-13](2026-08-13-api-tokens.md) | API tokens — non-interactive access on the same identity | Draft |
| [2026-08-18](2026-08-18-inter-zone-traffic.md) | Inter-zone traffic accounting — bytes by zone pair from kernel flows | Draft |
| [2026-08-23](2026-08-23-service-map-transport.md) | Transport workloads on the service map — stop drawing mesh hops as dependencies | Accepted |
| [2026-08-23](2026-08-23-virtual-targets.md) | Virtual targets — databases, caches and brokers on the map | Accepted |
| [2026-08-24](2026-08-24-map-encoding.md) | A map that carries more meaning — boundaries, edge volume, undetected peers | Accepted |
| [2026-08-25](2026-08-25-transport-hop-collapse.md) | Hop collapse — the dependency behind the proxy, from per-trace ancestry | Accepted |
| [2026-08-25](2026-08-25-mesh-surfaces.md) | Mesh-facing surfaces — proxy RED and control-plane health | Accepted |
| [2026-08-26](2026-08-26-cost-and-waste.md) | Cost and waste — reserved versus used | Accepted |
| [2026-08-26](2026-08-26-transport-from-labels.md) | Transport classified from Kubernetes labels | Accepted |
| [2026-08-26](2026-08-26-control-plane-diagnosis.md) | Why the control plane is silent — three states, not one | Accepted |
| [2026-08-27](2026-08-27-trace-analytics.md) | Trace analytics — grouped breakdowns over spans | Draft |
| [2026-08-27](2026-08-27-ai-observability.md) | AI observability — the model calls you are already sending | Accepted |
| [2026-08-30](2026-08-30-agents-budgets-and-rates.md) | Agents, budgets, and one rate table — the spend you can act on | Accepted |
| [2026-09-01](2026-09-01-mcp-server.md) | A Model Context Protocol server — the estate an agent can read | Accepted |

## Conventions

- Filename: `YYYY-MM-DD-<topic>.md`.
- Each AEP covers: context (why), decision, alternatives considered, verification.
- Diagrams live alongside the AEP; accurate, not decorative (see
  [AI_POLICY.md](../AI_POLICY.md#documentation--generated-media)).
