# Contributing to Avuru Obs

## Before you start

- Open an issue or [discussion](https://github.com/avuruvision/avuru-obs/discussions)
  describing non-trivial changes before doing the work. For **significant**
  designs (a new locked decision, the enterprise seam, the wedge), open an
  [Avuru Enhancement Proposal](./design/README.md) first.
- Read [AGENTS.md](./AGENTS.md) — the canonical developer **and** agent guide
  (yes, even humans). It has the repo map, branch model, and validation commands.
- By contributing you agree to the [Code of Conduct](./CODE_OF_CONDUCT.md) and
  to license your work under [Apache 2.0](./LICENSE).

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
