# UI Patterns (ui/)

Read when actively writing UI code.

## The one hard constraint

`next.config.ts` has `output: 'export'`. The UI is a **static SPA** served by
its own nginx image (separate deployable), single-origin with the hub. The
export constraint stays (a thin static client — no server-side treatment).
Therefore **forbidden**: SSR, React Server Components
with server data, server actions, API routes (`app/api/`), middleware,
`next/image` optimization (use `unoptimized`), runtime env vars. CI enforces
this — `npm run build` fails if violated. All data comes from the Hub REST/WS
API at runtime.

## Stack

- Next.js App Router, TypeScript `strict`
- **TanStack Query** for all server state (no fetch-in-useEffect);
  QueryClient created in `useState` inside the client Providers component
- **Tailwind v4 CSS-first + daisyUI 5**: config lives in `app/globals.css` —
  `@plugin "daisyui" { themes: false }` + two custom `@plugin "daisyui/theme"`
  blocks (`light`, `dark`). Components use daisyUI semantic tokens
  (`bg-base-100`, `text-base-content`, `text-primary`...) — never hardcoded
  hex in components
- **Theming**: next-themes, `attribute="data-theme"`, `defaultTheme="dark"`,
  themes `["light","dark"]`; `suppressHydrationWarning` on `<html>`; any
  component reading theme/localStorage uses the mounted-guard pattern
  (valife `themeSwitch.tsx` reference)
- **Avuru Gold palette** (do not improvise): light primary `#A8854A` on
  white/slate bases · dark primary `#C9A96A` on navy `#050B17/#0F1729/#162038`,
  pearl text `#E8E5DC` · semantics SATURATED and fixed: green success, red
  error, amber warning (error-rate readability beats brand subtlety)
- **Visual treatment**: Coroot layout density (collapsible sidebar, dense
  tables, heatmap-first) with SkyWalking-modern skin (rounded-xl cards, soft
  shadows, Inter via `next/font`)
- lucide-react icons; `cn()` = clsx + tailwind-merge (`src/lib/cn.ts`); CVA
  for component variants
- URL params hold filter/search state (shareable links are a product feature
  for an o11y tool — a trace search URL must be pasteable into Slack).
  **Detail views use query params, never dynamic routes** (static export):
  e.g. `/traces?trace=<id>`. `useSearchParams` always under `<Suspense>`
- Generated API types from `proto/` (`ui/src/generated/`) — never hand-write
  a type the API already defines (M1 exception: `src/lib/api-types.ts`
  hand-written until buf codegen lands)

## Patterns

1. **Golden screens** (service map, trace waterfall, log search, flame graph)
   each own a route directory under `app/`; shared components in
   `src/components/`.
2. **Client-side data discipline**: every query has a query key convention
   `[signal, scope, filters]`; live views use WS subscriptions, not polling
   loops.
3. **Empty states teach** — a screen with no data yet must show what to do
   (install checklist, kernel preflight result), not a blank panel. The
   "first 5 minutes" onboarding is a first-class surface, not an afterthought.
4. **Time is global**: one time-range picker in the shell; all screens honor
   it; all timestamps render in the user's locale with UTC on hover.
5. Keep components under ~300 lines; extract hooks into `src/hooks/`.
6. Dev mode: `npm run dev` proxies `/api` to the local hub (see
   `next.config.ts` rewrites — dev-only; production is same-origin).
