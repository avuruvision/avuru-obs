# ui/ — Next.js SPA

The UI is a **static-export** single-page app (`output: 'export'`) — one thin
client of the Hub API, served by its own nginx image in a separate pod,
single-origin with the hub (`/` → UI, `/api` → hub). Because it's a static
export, it has **no server-only features**; the build fails fast if any creep
in, which is exactly the guard CI relies on.

See [`agent_docs/ui_patterns.md`](../agent_docs/ui_patterns.md) before writing UI
code.

## Stack

- **Next.js** (App Router, `output: 'export'`), **React 19**, TypeScript strict.
- Tailwind CSS v4 + daisyUI, next-themes, TanStack Query, lucide-react.
- **Cytoscape** for the service-map graph.
- Node ≥22, ESLint 9, Playwright for e2e.

## Layout

| Path | What |
|---|---|
| `src/app/` | App Router pages/layouts |
| `src/components/` | React components |
| `src/lib/` | API client + types (`api-types.ts`) |
| `e2e/` | Playwright specs (maintained via `.claude/agents/`) |
| `next.config.ts` | Static export + dev `/api` proxy to the hub |
| `nginx.conf` / `Dockerfile` | Production image (nginx serving `out/`) |

## Develop, build, test

```bash
npm install
npm run dev         # HMR dev server on :3000, proxies /api to the hub on :8080
npm run lint        # ESLint (the gate)
npm run typecheck   # tsc --noEmit
npm run build       # static export -> out/  (MUST succeed; CI relies on it)
npx playwright test # e2e against a seeded stack (see root `make e2e-ui`)
```

From the repo root: `make ui` (build), `make ui-image` (container), `make check`.

## Conventions

- Functional components with hooks; type API responses against `src/lib`.
- Use the design tokens/components already in the codebase before adding new
  ones; keep files focused (aim < 300 lines).
- The hub binary serves the **last built** export — when iterating on UI, use
  the dev server; changes don't appear in a built hub/UI image until `make ui`.
