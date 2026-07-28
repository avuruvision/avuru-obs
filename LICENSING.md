# Licensing

Avuru Obs is licensed under **[AGPL-3.0](LICENSE)** — the same license as
Grafana. This page explains what that means for you in practice, why the
project chose it, and how the project sustains itself.

## The short version

- **Use it freely.** Self-hosting Avuru Obs for your team or company —
  unmodified, in production, at any scale — puts **zero obligations** on you.
- **Your apps stay yours.** Observing your services does not make them
  derivative works. Nothing about your instrumented code is affected.
- **Everything shipped stays open, forever.** The
  [CLA (§2.2)](CLA.md) legally pledges that every contribution remains
  available under AGPL-3.0 — the community edition can never be closed or
  stripped down retroactively.
- **The node agent is Apache-2.0.** The sensor deployed on your hosts is
  upstream [OpenTelemetry eBPF Instrumentation](https://github.com/open-telemetry/opentelemetry-ebpf-instrumentation)
  (OBI), configured — not forked — by this project.

## When does the AGPL ask something of you?

One case: you **modify Avuru Obs itself** and make the modified version
available to others over a network (for example, running a patched hub as a
service for third parties). Then the people using it must be able to get the
source of your modifications. For an internal deployment, "the people using
it" are your own team — in practice a no-op.

Redistributing or offering Avuru Obs as a service **unmodified** requires
only what is already public: this license and this source. If the AGPL does
not fit your case — embedding Avuru Obs or its modules into a proprietary
product, for instance — a commercial license is available:
**egilberny@lab.luxavuru.com**.

## Why AGPL-3.0

Observability platforms sit on the most sensitive telemetry an organization
produces. Avuru Obs is built as a **sovereign** alternative to per-host SaaS
billing — and sovereignty is only credible if the software can never be
enclosed. Most open-source observability vendors today pair a permissive
community core with a closed enterprise edition (Coroot, DeepFlow,
VictoriaMetrics — Apache-2.0; SigNoz — MIT). Permissive licensing leaves the
community edition free for any incumbent to absorb into a proprietary
service. The AGPL, chosen by Grafana (2021), Elastic (2024) and OpenObserve,
closes that door: whoever builds on Avuru Obs — including its
energy/carbon accounting — builds in the open.

For the people actually deploying it, the difference is invisible: AGPL
self-hosting carries the same zero obligations as Apache self-hosting.

## Components and third-party code

| Component | License | Notes |
|---|---|---|
| Hub, UI, gateway (`sentryreceiver`), charts | AGPL-3.0 | This repository |
| Sensor (node agent) | Apache-2.0 | Upstream OBI, deployed via Helm — not forked |
| Go/TS dependencies | Apache-2.0 / MIT / BSD | No copyleft code in shipped artifacts |

Apache-2.0 attribution for bundled dependencies ships as
`THIRD-PARTY-NOTICES.md` in release artifacts (`make notices` regenerates
it).

## Editions and sustainability

- **Community edition (this repository).** The full product — including
  authentication, RBAC, and OIDC SSO, which others gate behind enterprise
  paywalls. AGPL-3.0, forever ([CLA §2.2](CLA.md)).
- **Enterprise edition (planned).** A separate commercial offering built
  behind the
  [enterprise seam](agent_docs/architecture.md#enterprise-seam-do-not-bypass)
  for organization-scale needs (multi-tenancy, advanced retention and
  compliance). It adds to the community edition; it never removes from it.
- **Commercial / dual licensing.** For vendors embedding Avuru Obs where the
  AGPL doesn't fit — contact **egilberny@lab.luxavuru.com**.
- **Sponsoring.** [GitHub Sponsors](https://github.com/sponsors/avuruvision)
  and [Open Collective](https://opencollective.com/avuru-obs) fund CI, eBPF
  test hardware, and signed multi-arch releases.

## The CLA in one paragraph

Contributors sign a one-time [Individual CLA](CLA.md) on their first PR (a
bot handles it; signing is a single comment). You keep ownership of your
work and grant the project the rights that make the model above possible —
including offering commercial licenses. In exchange, §2.2 pledges your
contribution stays available under AGPL-3.0 forever: it can never become
closed-only. Details and the employer question: [CONTRIBUTING.md](CONTRIBUTING.md#contributor-license-agreement-cla).
