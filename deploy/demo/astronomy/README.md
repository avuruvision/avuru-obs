# Astronomy Shop demo (OTLP)

A rich, multi-service demo app that feeds **Avuru Obs** over the **stable OTLP
path** — the app behind the public **Live Demo**. It runs the upstream
[OpenTelemetry Community Demo](https://opentelemetry.io/docs/demo/) (the
"Astronomy Shop") and points every service's OTLP exporter at the Avuru Obs
gateway instead of the demo's own bundled collector and backends.

This is the counterpart to the other two demos in this repo:

| Demo | Path | Proves |
|---|---|---|
| [`wedge`](../wedge/wedge.yaml) | eBPF sensor | zero-code service map from un-instrumented pods |
| HotROD (in [`compose`](../../compose/docker-compose.yaml)) | OTLP | Jaeger-style drop-in, one service |
| **`astronomy`** (here) | **OTLP** | a **full multi-service app** on the stable path |

The Astronomy Shop is deliberately **instrumented** (OpenTelemetry SDKs), so it
demonstrates the production OTLP story: bring your own instrumentation, point it
at Avuru Obs, get every signal in one ClickHouse.

The overlay tags all of its telemetry `avuru.tenant=demo`, so it lands in the
**`demo` project**. Enable the read-only demo login on the Avuru Obs release
(`--set auth.demo.enabled=true`) and visitors can explore it with one click via
**"Try the demo"** on the login page — no account, no setup.

## Prerequisites

- A Kubernetes cluster (kind is fine) with **Avuru Obs installed** — Helm
  release `avuruobs`. See [Setup › Kubernetes](../../helm/README.md).
- `helm` and `kubectl`. The demo needs roughly **6 GB RAM / 4 CPU** free; it is
  heavier than the wedge/HotROD demos.

## Run

Install into the **same namespace** as Avuru Obs so `avuruobs-gateway` resolves:

```bash
NS=avuruobs ./install.sh
```

or manually:

```bash
helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm upgrade --install astronomy open-telemetry/opentelemetry-demo \
  --version 0.40.10 -n avuruobs --create-namespace \
  -f values-avuru.yaml
```

The shop's built-in load generator drives traffic on its own. Open the Avuru Obs
UI and the service map fills in — over OTLP, no eBPF sensor involved.

## How the wiring works

The demo chart derives every component's `OTEL_EXPORTER_OTLP_ENDPOINT` from a
single `OTEL_COLLECTOR_NAME` env var. [`values-avuru.yaml`](./values-avuru.yaml)
overrides it to `avuruobs-gateway` and disables the demo's bundled collector,
Jaeger, Prometheus, Grafana and OpenSearch — so Avuru Obs is the only backend.

Different namespace? Set the value to the gateway FQDN, e.g.
`avuruobs-gateway.avuruobs.svc.cluster.local`.

## CI / pipeline (avuru-ops)

The LAN deploy pipeline clones this repo into `src/`, so the script is available
at `src/deploy/demo/astronomy/install.sh`. Run it inside the pipeline's
`withKubeCredentials` block, targeting the Avuru Obs namespace:

```bash
NS=avuru-obs src/deploy/demo/astronomy/install.sh
```

Overrides go through `EXTRA_HELM_ARGS`, so CI never has to fork the script:

- **Harbor-mirrored images** (nodes can't reach `ghcr.io`): mirror the demo
  images into Harbor, then point the chart at them —
  `EXTRA_HELM_ARGS="--set default.image.repository=<harbor>/avuru/otel-demo"`.
- **Separate namespace** (not the Avuru Obs one): pass the gateway FQDN —
  `EXTRA_HELM_ARGS="--set default.envOverrides[0].value=avuruobs-gateway.avuru-obs.svc.cluster.local"`.

Note: the Astronomy Shop pulls ~15 service images from `ghcr.io` plus a few
dependency images. On nodes without internet egress, mirror them (as above) or
provide a pull-through cache — otherwise the demo pods `ImagePullBackOff`.

## Tear down

```bash
helm uninstall astronomy -n avuruobs
```
