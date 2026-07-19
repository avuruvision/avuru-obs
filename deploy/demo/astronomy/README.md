# Astronomy Shop demo (OTLP)

A rich, multi-service demo app that feeds **avuru obs** over the **stable OTLP
path** — the app behind the public **Live Demo**. It runs the upstream
[OpenTelemetry Community Demo](https://opentelemetry.io/docs/demo/) (the
"Astronomy Shop") and points every service's OTLP exporter at the avuru obs
gateway instead of the demo's own bundled collector and backends.

This is the counterpart to the other two demos in this repo:

| Demo | Path | Proves |
|---|---|---|
| [`wedge`](../wedge/wedge.yaml) | eBPF sensor | zero-code service map from un-instrumented pods |
| HotROD (in [`compose`](../../compose/docker-compose.yaml)) | OTLP | Jaeger-style drop-in, one service |
| **`astronomy`** (here) | **OTLP** | a **full multi-service app** on the stable path |

The Astronomy Shop is deliberately **instrumented** (OpenTelemetry SDKs), so it
demonstrates the production OTLP story: bring your own instrumentation, point it
at avuru obs, get every signal in one ClickHouse.

## Prerequisites

- A Kubernetes cluster (kind is fine) with **avuru obs installed** — Helm
  release `avuruops`. See [Setup › Kubernetes](../../helm/README.md).
- `helm` and `kubectl`. The demo needs roughly **6 GB RAM / 4 CPU** free; it is
  heavier than the wedge/HotROD demos.

## Run

Install into the **same namespace** as avuru obs so `avuruops-gateway` resolves:

```bash
NS=avuruops ./install.sh
```

or manually:

```bash
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm upgrade --install astronomy open-telemetry/opentelemetry-demo \
  --version 0.40.10 -n avuruops --create-namespace \
  -f values-avuru.yaml
```

The shop's built-in load generator drives traffic on its own. Open the avuru obs
UI and the service map fills in — over OTLP, no eBPF sensor involved.

## How the wiring works

The demo chart derives every component's `OTEL_EXPORTER_OTLP_ENDPOINT` from a
single `OTEL_COLLECTOR_NAME` env var. [`values-avuru.yaml`](./values-avuru.yaml)
overrides it to `avuruops-gateway` and disables the demo's bundled collector,
Jaeger, Prometheus, Grafana and OpenSearch — so avuru obs is the only backend.

Different namespace? Set the value to the gateway FQDN, e.g.
`avuruops-gateway.avuruops.svc.cluster.local`.

## Tear down

```bash
helm uninstall astronomy -n avuruops
```
