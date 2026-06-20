# Tech Stack & Pinned Versions

Reuse over rewrite: upstream OSS components are consumed as-is and pinned.
Upgrades are deliberate PRs that update this file, the lockfiles, and the
relevant Dockerfile/manifest together.

## Components we REUSE (pinned)

| Component | Pin | Notes |
|---|---|---|
| OBI (OpenTelemetry eBPF Instrumentation) | `v0.9.x` (pin exact in `sensor/`) | Pre-1.0: expect breaking changes; never float the tag. GA expected late 2026 |
| OTel Collector (contrib / OCB) | **`0.154.0`** (compose image + `gateway/ocb-manifest.yaml`, M1 uses stock contrib image; OCB build lands M5) | Collector upgrade = deliberate MR that re-verifies ClickHouse DDL parity (see `gateway/schemas/README.md`) |
| OTel eBPF profiler | pin exact (Collector receiver form) | OTLP Profiles signal is ALPHA — all profile ingestion goes through `hub/internal/storage/profilesadapter` |
| ClickHouse | **`26.3` LTS** (`clickhouse/clickhouse-server:26.3`) | Single-node default with tuned config (8 GB recommended / 4 GB floor); compose uses `low-resources.xml` + 2 GB cap |
| clickhouse-go | latest stable v2 | Official Go client |
| opamp-go | pin exact | Hub's OpAMP server |
| Demo app: HotROD (`jaegertracing/example-hotrod`) | pin exact in compose | One container simulating 4 services via stock OTel SDK, configured ONLY by `OTEL_EXPORTER_OTLP_ENDPOINT` — doubles as the Jaeger drop-in proof. Astronomy shop (minimal profile) reserved for the M5 kind/TTV gate |

## Components we BUILD

| Component | Stack | Key crates/libs |
|---|---|---|
| `agent/` avuru-agent | Rust stable (see `agent/rust-toolchain.toml`) | `aya` (pure-Rust eBPF, no libbpf C dep), `opentelemetry`/`opentelemetry-otlp` (Rust), `tokio` |
| `hub/` | Go (see `hub/go.mod`) | stdlib `net/http`, `opamp-go`, `clickhouse-go`, `modernc.org/sqlite` (CGO-free SQLite) |
| `ui/` | Next.js (App Router, `output: 'export'`), TypeScript strict | Tailwind v4 CSS-first + daisyUI 5 (two custom themes, Avuru Gold), next-themes (`data-theme`, dark default), TanStack Query, lucide-react, CVA + clsx/tailwind-merge. NO chart lib in M1 (heatmap = CSS grid, waterfall = flex bars); canvas/flame-graph lib chosen in M4 |

## Hard rules

- **OBI is Go — it is reused as a container, never embedded in Rust.**
- **eBPF code compiles on Linux only.** `agent/` must keep `cargo check` green
  on macOS by gating eBPF/aya behind `#[cfg(target_os = "linux")]` and
  target-specific dependencies. Real builds happen in the Docker build image.
- **No CGO in the hub** — single static binary is the deliverable
  (`modernc.org/sqlite`, not `mattn/go-sqlite3`).
- **UI must keep `output: 'export'` working** — no SSR, server actions, API
  routes, or middleware. CI fails the build otherwise.
- Toolchain versions live in `agent/rust-toolchain.toml`, `hub/go.mod`
  (toolchain directive), and `ui/package.json` (`engines`). Update them there,
  not in CI files.
