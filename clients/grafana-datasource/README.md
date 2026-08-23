# Avuru Obs — Grafana data source

Read Avuru Obs from dashboards you already run. Service RED metrics, service
health, trace search and cross-zone traffic, through the public Hub API.

A **backend** data source, deliberately. Two consequences follow from that and
they are the reason for the choice:

- The API token is stored in Grafana's encrypted secure settings and decrypted
  only in the plugin's own process. It never reaches a browser.
- Queries leave the Grafana server, not the viewer's machine — so a hub that is
  only reachable inside the cluster still works.

## Install

```bash
# from a release
unzip avuru-obs-datasource-<version>.zip -d /var/lib/grafana/plugins/
# unsigned, so Grafana needs to be told it is allowed
GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=avuru-obs-datasource
```

The plugin is **not signed** by Grafana Labs: signing requires publishing
through their catalogue, which is a separate step from building the thing.
Until then it loads as an unsigned plugin, which is a deliberate, stated cost
rather than an oversight.

## Configure

**Hub URL** and an **API token** (Settings → Access in Avuru Obs). A token
resolves to its owner's *live* permissions, so the data source sees exactly what
that person sees — a Viewer token cannot read a project its owner cannot.

**Default project** picks the tenant panels read when they do not name one.
**Save & test** calls the same endpoint every query goes through, so a green
check means the credential works, not merely that something answered on that
host.

## Query

| Query | Frame |
|---|---|
| Service RED | one row per service: rate, error rate, p50/p95/p99, span count |
| Service health | one row per group: status, tier, environment, rate, errors, p95 (the overall rollup rides in frame metadata) |
| Traces | trace search results as a table, with `service` / `status` / `tags` filters — the same `tags` string the traces screen uses |
| Cross-zone traffic | bytes per zone pair |

The panel's time range is the query's time range.

## Build

```bash
npm ci && npm run build          # dist/module.js + plugin.json + assets
go build -o dist/gpx_avuru_obs_linux_amd64 ./pkg
```

The release workflow builds both halves for every supported platform and
attaches the zip.
