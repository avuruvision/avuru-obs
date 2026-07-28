# Contributing to Avuru Obs

## Before you start

- Open an issue or [discussion](https://github.com/avuruvision/avuru-obs/discussions)
  describing non-trivial changes before doing the work. For **significant**
  designs (a new locked decision, the enterprise seam, the wedge), open an
  [Avuru Enhancement Proposal](./design/README.md) first.
- Read [AGENTS.md](./AGENTS.md) — the canonical developer **and** agent guide
  (yes, even humans). It has the repo map, branch model, and validation commands.
- By contributing you agree to the [Code of Conduct](./CODE_OF_CONDUCT.md)
  and sign the [Contributor License Agreement](./CLA.md) on your first PR
  (automated, one-time). Your work ships under [AGPL-3.0](./LICENSE).

## Contributor License Agreement (CLA)

All contributions require a one-time signature of the
[Individual CLA](./CLA.md) — a bot prompts you on your first pull request;
signing is a single comment.

Why a CLA instead of plain AGPL inbound=outbound: the Project intends to
sustain itself the way Grafana and Elastic do — a fully open AGPL
community edition, plus commercially licensed enterprise capabilities behind
the [enterprise seam](./agent_docs/architecture.md#enterprise-seam-do-not-bypass).
Offering a commercial license requires the project to hold sufficient rights
in its codebase, which is exactly what the CLA grants. In exchange, the CLA
pledges (§2.2) that everything you contribute stays available under AGPL-3.0
forever — your work can never be made closed-only.

Contributing on your employer's time or equipment? Check §4 of the CLA; a
corporate CLA is available on request. The full licensing model — what the
AGPL means for users, editions, sustainability — is in
[LICENSING.md](./LICENSING.md).

## Workflow

1. Branch from `main`: `feature/<topic>` for milestone work, `ai/<topic>` for
   ad-hoc tasks. First push sets the branch's own upstream (`git push -u`).
2. Conventional commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`…); scope
   = component (`feat(hub): …`). **Sign your commits** — signing is required (see
   [COMMIT-SIGNING-SETUP.md](./COMMIT-SIGNING-SETUP.md)). Commit as the configured
   author only — **no AI co-author trailers** (see [AI_POLICY.md](./AI_POLICY.md)).
3. Tests land alongside the implementation (see
   [agent_docs/testing.md](./agent_docs/testing.md)).
4. Run **`make check`** before every commit (per-component commands in AGENTS.md);
   never skip failing checks.
5. Open a PR to `main`. Describe the **intent** (the "why"), not just the diff.

`main` is the development trunk; releases are cut as `vX.Y.Z` tags on `vX.Y`
branches — see [RELEASING.md](./RELEASING.md).

## Code style

See [STYLE_GUIDE.md](./STYLE_GUIDE.md) → the per-language docs in `agent_docs/`.

## More

- [GOVERNANCE.md](./GOVERNANCE.md) — how decisions are made; becoming a maintainer
- [AI_POLICY.md](./AI_POLICY.md) — AI-assisted contributions
- [SECURITY.md](./SECURITY.md) — reporting vulnerabilities privately
