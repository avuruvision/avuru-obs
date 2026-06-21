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
| _(add AEPs here)_ | — | — |

## Conventions

- Filename: `YYYY-MM-DD-<topic>.md`.
- Each AEP covers: context (why), decision, alternatives considered, verification.
- Diagrams live alongside the AEP; accurate, not decorative (see
  [AI_POLICY.md](../AI_POLICY.md#documentation--generated-media)).
