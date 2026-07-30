# deploy/compose — laptop sandbox

All-in-one stack for demos/evaluation/e2e: ClickHouse 26.3 (laptop-tuned) +
gateway collector + hub + UI + demo app. Two ways in.

**No checkout — evaluate the released images.** `docker-compose.release.yaml`
pulls the published GHCR images and inlines its own config, so one file is
enough:

```bash
curl -fsSLO https://raw.githubusercontent.com/avuruvision/avuru-obs/main/deploy/compose/docker-compose.release.yaml
docker compose -f docker-compose.release.yaml up --wait   # open http://localhost:3001
# pick a release:  AVURUOBS_VERSION=v0.2.0 docker compose -f docker-compose.release.yaml up --wait
```

**From source — the dev loop** (builds hub/ui/gateway locally):

```bash
make dev          # from repo root — up with build
make dev-clean    # down + wipe volumes (re-runs schema migrations on next up)
```

| URL | What |
|---|---|
| <http://localhost:3001> | Avuru UI |
| <http://localhost:8080> | hub API (the UI proxies `/api` here) |
| <http://localhost:8088> | demo app — click buttons to generate traces |
| <http://localhost:8123> | ClickHouse HTTP (user `avuru` / `avuru`) |
| localhost:4317 / 4318 | OTLP gRPC / HTTP ingest |

**The drop-in demo**: HotROD is Jaeger's own example app; here it points to
Avuru via `OTEL_EXPORTER_OTLP_ENDPOINT` alone — the migration story in one
env var.

Notes:
- Schema migrations are applied by the one-shot **`migrate`** service
  (`hub migrate` against the embedded `hub/internal/storage/migrations/*.sql`)
  — the same mechanism as the k8s Helm hook. `gateway` and `hub` wait for it
  to complete. Re-running is idempotent (`schema_migrations` ledger); retention
  TTL comes from `AVURUOBS_RETENTION_{TRACES,LOGS}_DAYS`.
- ClickHouse is capped at 2 GB (`low-resources.xml`). Give the Docker VM
  ≥6 GB total (8 GB recommended): `colima start --memory 8`.
- `seed/` holds deterministic OTLP fixtures used by `make e2e` (M1 step 6).
