# build/jenkins — Avuru Obs pipelines

Two pipelines, following the avuru convention (cf. `kiwi-tcms`/`auth-service`
`build/jenkins/`). The repo is hosted on GitHub (`avuruvision/avuru-obs`); both
pipelines clone it explicitly. The chart is at `deploy/helm/avuruops` — the
Jenkinsfiles point there via `chartDir` (no move/duplication).

| Pipeline | Does |
|---|---|
| `Jenkinsfile.jenkinsfile` (CI) | clone → build the hub image → mirror gateway + ClickHouse images → package the chart. Publishes the hub image to **Harbor** and the chart `.tgz` to the **Nexus helm-hosted** repo. |
| `deploy.jenkinsfile` (CD) | clone → pull the chart from Nexus → (prod: manual approval) → `helm upgrade --install` per environment, pointing every image at Harbor (`--set image.registry=$HARBOR_URL`) plus `deploy/helm/values-<env>.yaml`. |

## Flow

```
GitHub (avuruvision/avuru-obs)
   │ Jenkins clone (github-avuruvision)
   ▼
CI  ── docker build hub/Dockerfile ─────────► Harbor  $HARBOR_URL/avuruops/hub:<appVersion>
    ── mirror collector + clickhouse ───────► Harbor  $HARBOR_URL/otel/... , /clickhouse/...
    ── helm package deploy/helm/avuruops ──────► Nexus   helm-hosted/avuruops-<version>.tgz
   ▼
CD  ── helm upgrade --install (Nexus chart) ─► k8s  ns avuru-services[-staging]
        --set image.registry=$HARBOR_URL  -f values-<env>.yaml
```

Apps migrate off Jaeger by pointing one env var at the deployed gateway:
`OTEL_EXPORTER_OTLP_ENDPOINT = http://avuruops-gateway.<ns>:4318`.

## Required Jenkins credentials

| ID | Type | Use |
|---|---|---|
| `github-avuruvision` | SSH key | clone `git@github.com:avuruvision/avuru-obs.git` |
| `harbor-registry` | username/password | docker login + push to Harbor |
| `harborUrl` | secret text | Harbor host (image registry prefix) |
| `docker-hosted` | username/password | Nexus helm-hosted upload + `helm repo add` |
| `k8s-token-dev` | kubeconfig/token | `withKubeCredentials` deploy target |

`image.registry` is injected at deploy time (`--set image.registry=$HARBOR_URL`),
so the chart and the `values-<env>.yaml` files stay registry-agnostic.

## Image tagging

The hub image is tagged with the chart `appVersion` (currently `0.1.0`). Bump
`appVersion` in `deploy/helm/avuruops/Chart.yaml` for a new immutable release; CI
tags and pushes that version, CD deploys it.
