# Governance

How decisions get made in avuru-obs. This is deliberately lean — the project is
young and the team is small. It borrows the *shape* of [Kiali's
governance](https://github.com/kiali/kiali/blob/master/GOVERNANCE.md) without
the vote-counting machinery a larger community needs. As the contributor base
grows, this document grows with it (see [Changing this
document](#changing-this-document)).

## Mission

Make production observability — traces, metrics, logs, and continuous profiling
— work in five minutes with zero code changes, as open source under
[Apache 2.0](LICENSE).

## Roles

| Role | What it means |
|---|---|
| **Contributor** | Anyone who opens an issue, a discussion, or a pull request. No prior approval needed — start with [CONTRIBUTING.md](CONTRIBUTING.md). |
| **Maintainer** | Has merge rights, sets direction, and is accountable for the project's health. Listed in [MAINTAINERS.md](MAINTAINERS.md). |

There is no separate tester or leader ladder at this stage — those exist in
Kiali because of its scale. If the project grows to need them, they get added
here by the process below.

## How decisions are made

**Lazy consensus is the default.** Most changes proceed without a formal vote: a
maintainer reviews and merges, and silence on a proposal is taken as assent.

- **Routine changes** (bug fixes, docs, tests, dependency bumps, anything that
  upholds the existing [locked decisions](agent_docs/architecture.md)): one
  maintainer approval merges it.
- **Significant changes** (a new locked decision, a dependency that becomes
  load-bearing, anything touching the [enterprise
  seam](agent_docs/architecture.md#enterprise-seam-do-not-bypass) or the
  [wedge](AGENTS.md)): open an **Avuru Enhancement Proposal** first — see the
  [`design/`](design/README.md) process — so the rationale is captured in a
  durable document, not scattered across review threads.
- **Disagreement** is resolved by discussion. If maintainers can't reach
  consensus, a simple majority of maintainers decides. With a single maintainer,
  that maintainer decides — and is expected to document *why* in the relevant
  design doc or PR.

Security and Code-of-Conduct matters are handled privately — see
[SECURITY.md](SECURITY.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Becoming a maintainer

We invite maintainers; we don't run a points system. The bar is **sustained,
high-quality contribution plus good judgment** — someone the current
maintainers trust to merge without a second pair of eyes. In practice that looks
like:

- A track record of merged, non-trivial contributions over time.
- Reviews that improve other people's work, not just approvals.
- Demonstrated understanding of the [architecture](agent_docs/architecture.md),
  the [wedge](AGENTS.md), and the project's conventions.
- Reliability — follows through, communicates, respects the
  [Code of Conduct](CODE_OF_CONDUCT.md).

Any existing maintainer can propose a contributor by opening a PR that adds them
to [MAINTAINERS.md](MAINTAINERS.md). If the other maintainers don't object, the
PR merges and the invitation is extended. With a single maintainer today, this
is how the team grows past one.

## Stepping down

Maintainers who can no longer commit the time should say so and open a PR moving
themselves to an *Emeritus* section in [MAINTAINERS.md](MAINTAINERS.md). It's a
normal, no-fault part of an open-source project's life. A maintainer who goes
silent for an extended period (no contributions or communication for ~4 months)
may be moved to Emeritus by the remaining maintainers.

## Changing this document

Governance changes the same way code does: open a PR. Because this document sets
the rules everyone else plays by, give it extra review time and explicit
maintainer sign-off rather than lazy consensus. When the team is larger than a
handful of maintainers, replace the lightweight rules here with explicit voting
thresholds (Kiali uses a 2/3 majority for governance changes) — that's the
natural next milestone for this file.
