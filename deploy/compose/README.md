# deploy/compose — laptop sandbox

All-in-one stack for demos/evaluation/e2e: ClickHouse 26.3 (laptop-tuned) +
gateway collector (contrib 0.154.0) + hub + **HotROD** demo app.

```bash
make dev          # from repo root — up with build
make dev-clean    # down + wipe volumes (re-runs schema migrations on next up)
```

| URL | What |
|---|---|
| <http://localhost:8080> | Avuru hub (UI + API) |
| <http://localhost:8088> | HotROD demo — click buttons to generate traces |
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
  TTL comes from `AVURUOPS_RETENTION_{TRACES,LOGS}_DAYS`.
- ClickHouse is capped at 2 GB (`low-resources.xml`). Give the Docker VM
  ≥6 GB total (8 GB recommended): `colima start --memory 8`.
- `seed/` holds deterministic OTLP fixtures used by `make e2e` (M1 step 6).
