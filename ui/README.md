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

## Cluster X-Ray

Infrastructure (`/nodes`) keeps the inventory as its default. Select **Cluster
X-Ray** (`/nodes?view=xray`) for an interactive 3D view of observed Nodes and Pods.
Select a Pod in the scene or the keyboard-accessible selector; use node,
namespace and Pod filters to narrow placement, then isolate its neighbourhood.
Layer switches, spacing, transparency and camera controls change presentation.

The scene draws a logical infrastructure illustration, not a discovered physical
chassis. CPU/memory are observed usage; Pod readiness and resource limits are not
available here. Connections come from `/api/v1/infra/pod-connections`: matched
Client/Server span identities connect Pods, while an unmatched recorded address
appears as an unresolved peer. A globe does not assert public Internet location.
Only caller-side request counts, errors and p95 latency are reported. Flow speed
is illustrative. Missing span identities mean no attributed connection.

The engine loads only on opening X-Ray. A scene is limited to 9 nodes, 12 Pods
per node and 100 connections; visible counts explain truncation. The resource
read loads up to 200 Pods per node filter. Narrow filters to bring omitted Pods
into view; the inventory is always available. Reduced motion pauses particles,
and hidden/offscreen scenes suspend rendering. GPU resources are released when
the view closes. Browsers without WebGL receive an inventory fallback.
