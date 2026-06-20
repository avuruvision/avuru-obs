# M1 — Walking Skeleton + UI Design (Avuru Gold)

Date: 2026-06-13
Status: Approved (visual-companion brainstorm + plan agent, June 2026)

## Goal

First vertical slice: OTLP app → gateway collector → ClickHouse → Hub API →
Traces UI, all via `docker compose`. The "Jaeger drop-in" promise (migrate by
changing only the exporter endpoint env var) becomes an e2e test.

## UI design decisions (validated visually with the user)

- **Layout = Coroot architecture**: collapsible left sidebar (Services,
  Service Map, Traces, Logs, Profiling, Nodes, Settings), heatmap-first trace
  overview, underline tabs, dense tables. Non-M1 routes ship as *teaching*
  ComingSoon states.
- **Treatment = SkyWalking-modern**: rounded-xl cards, soft shadows, Inter,
  generous radii on a dense grid.
- **Dual theme from day 1** via daisyUI custom themes + next-themes
  (`data-theme`), **dark by default** (on-call convention); light at one
  click.
- **Palette "Avuru Gold"** (valife brand coherence): light → primary
  `#A8854A` on white/slate bases; dark → primary `#C9A96A` on navy bases
  `#050B17 / #0F1729 / #162038`, pearl text `#E8E5DC`. Semantic colors stay
  SATURATED and fixed (green success / red error / amber warning) — error
  rate readability beats brand subtlety in an o11y tool.
- **Stack mirrors valife-nextjs-front**: Tailwind v4 CSS-first, daisyUI 5
  (`@plugin "daisyui" { themes: false }` + two custom theme blocks),
  next-themes with mounted-guard switch, lucide-react, `cn()`
  (clsx+tailwind-merge), CVA variants, TanStack Query for all server state.
  Patterns ported: theme-provider, themeSwitch, nav-sidebar (localStorage
  collapse + tooltips), globals.css structure (~200 lines, only what M1 uses).
- **Static-export constraints**: trace detail = `?trace=<id>` query param
  (pasteable URLs, no dynamic route), `useSearchParams` under `<Suspense>`,
  QueryClient created in `useState`, `suppressHydrationWarning` on `<html>`.

## Architecture decisions (M1)

- **Own ClickHouse DDL** (`gateway/schemas/0001/0002`), exporter
  `create_schema: false`: the Tenant column (enterprise seam) and
  policy-driven TTL require owning the schema; exporter auto-DDL drift
  becomes an explicit build failure (parity = integration test). Collector
  upgrades are deliberate MRs that re-verify DDL parity.
- **Demo app = HotROD** (`jaegertracing/example-hotrod`): one small container
  simulating 4 services with realistic traces, configured ONLY via
  `OTEL_EXPORTER_OTLP_ENDPOINT` — it *is* the Jaeger drop-in story. The
  astronomy shop (heavy, ~20 containers) remains the candidate for the M5
  kind/TTV gate.
- **Deterministic seeder** (`tools/seed/`): versioned OTLP/JSON fixtures,
  timestamps rebased to now, POSTed through the gateway (real ingest path) —
  e2e asserts exact trace ids/spans/durations.
- **Hub API (5 endpoints)**: `/api/v1/services`, `/traces/overview`,
  `/traces` (keyset cursor: ns-timestamp + traceId), `/traces/{id}` (via
  ts-lookup table), `/traces/heatmap` (log2 duration × time buckets).
  Central error middleware; signal endpoints 503 when CH is down,
  `/healthz` stays 200.
- **No chart library in M1**: heatmap = CSS grid (60×24), waterfall =
  flex/grid bars. Canvas/virtualization is an M2+ concern.

## Out of scope (explicit)

Heatmap brush selection, waterfall minimap/collapse-all, free-form attribute
search (only indexed columns: service/operation/duration/status), WS live
tail, list virtualization, hub-side migration runner (initdb.d only).

## Verification

`make dev` → HotROD :8088 → hub :8080 shows Avuru Gold shell; Traces screen:
heatmap fills, RED table lists the 4 HotROD services, row→filtered traces,
trace→multi-service waterfall, `?trace=` URL shareable. `make e2e` green
(drop-in + seeded determinism). `make check` green.
