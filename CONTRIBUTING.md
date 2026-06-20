# Contributing to Avuru Obs

## Before you start

- Open an issue or discussion describing non-trivial changes before doing the work.
- Read [AGENTS.md](./AGENTS.md) — the canonical developer **and** agent guide
  (yes, even humans). It has the repo map, branch model, and validation commands.

## Workflow

1. Branch from `develop`: `feature/<milestone>` for milestone work, `ai/<topic>`
   for ad-hoc tasks. First push sets the branch's own upstream (`git push -u`).
2. Conventional commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`…); scope
   = component (`feat(hub): …`). Commit as the configured author only — **no AI
   co-author trailers** (see [AI_POLICY.md](./AI_POLICY.md)).
3. Tests land alongside the implementation (see
   [agent_docs/testing.md](./agent_docs/testing.md)).
4. Run **`make check`** before every commit (per-component commands in AGENTS.md);
   never skip failing checks.
5. Open a PR to `develop`. Describe the **intent** (the "why"), not just the diff.

## Code style

See [STYLE_GUIDE.md](./STYLE_GUIDE.md) → the per-language docs in `agent_docs/`.

## AI-assisted contributions

See [AI_POLICY.md](./AI_POLICY.md).
