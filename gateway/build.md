# Building the gateway distro

The gateway is a **minimal OTel Collector distribution** built with the
[OpenTelemetry Collector Builder](https://opentelemetry.io/docs/collector/custom-collector/)
(OCB) from [`ocb-manifest.yaml`](ocb-manifest.yaml) — only the components the
pipeline actually uses (OTLP receiver, batch + k8sattributes processors,
ClickHouse exporter, healthcheck extension).

```bash
# from the repo root
make gateway-image        # -> avuru-obs-gateway:local
```

Rules:

- **All component versions and `--feature-gates` pins move together** with
  `agent_docs/tech_stack.md` (collector 0.154.0 line). Upgrading the collector
  is a deliberate MR that re-runs the ClickHouse DDL contract-freeze procedure
  (`gateway/schemas/README.md`).
- The **stock contrib image** (`otel/opentelemetry-collector-contrib:0.154.0`)
  runs the exact same config, and the Helm chart accepts it as an override
  (`gateway.image.repository`/`tag`) if pulling the distro is not an option.
  It costs the `replaces:` security floors, though: the stock image of this
  line scans with the CRITICAL and the HIGHs the manifest pins above, and no
  contrib release fixes the `x/crypto` one (0.159.0 still resolves v0.54.0,
  below the fixed v0.55.0). The sensor's node agent runs contrib because it
  needs receivers this distro does not carry; the gateway has no such reason.
- Releases publish `ghcr.io/<owner>/avuru-obs-gateway:<tag>` via
  `.github/workflows/release.yml`; the chart's default `appVersion` tag matches.
