# Runbook: public Live Demo (demo.avuruobs.io)

Runs avuru obs as an internet-facing, read-only showcase backed by the
[Astronomy Shop](../../deploy/demo/astronomy/README.md). Anonymous visitors
get **Viewer on exactly one project** (`demo`) — the v0.2 anonymous-access
feature built for this. Everything else stays closed.

Manifests: [`deploy/demo/public/`](../../deploy/demo/public/).

## Prerequisites

- A Kubernetes cluster with an ingress controller (the values default to
  APISIX; any class works) and TLS (cert-manager or a pre-provisioned
  certificate in Secret `demo-avuruobs-tls`).
- DNS: `demo.avuruobs.io` → the ingress' public address.
- ~6 GB / 4 CPU headroom for the shop on top of the avuru obs footprint.

## Install

```bash
NS=demo-obs
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n "$NS" create secret generic avuruobs-admin \
  --from-literal=admin-password="$(openssl rand -base64 24)"

VERSION=<released chart version> NS="$NS" deploy/demo/public/install.sh
kubectl -n "$NS" rollout status deploy/avuruobs-hub --timeout=300s
```

The shop's built-in load generator drives traffic continuously; the service
map fills in over OTLP with no further action.

## Behind a reverse proxy that rewrites `Host`

If something sits in front of the ingress (an edge proxy, a tunnel, a home
router forwarding to the LAN), check what it puts in `Host`. When it forwards
the *ingress address* instead of `demo.avuruobs.io`, the browser still sends
`Origin: https://demo.avuruobs.io` while the hub sees the ingress address — the
CSRF check reads that as cross-origin and **every write 403s, starting with the
login**. Two ways out, in order of preference:

1. **Preserve the Host at the proxy** — nothing to change in the chart:
   ```nginx
   proxy_pass http://<ingress-address>;
   proxy_set_header Host $host;          # demo.avuruobs.io, not the upstream
   proxy_set_header X-Forwarded-Proto $scheme;
   ```
2. **Declare the origin to the hub** when the proxy is not yours to configure:
   ```yaml
   auth:
     trustedOrigins: ["https://demo.avuruobs.io"]
   ```
   The check stays on; only that origin is added to what it accepts.

`auth.originCheck: log` renders the observed `Origin`/`Host` pair in the hub
logs (`kubectl -n demo-obs logs deploy/avuruobs-hub | grep origin`) when you
need to see what actually arrives. Put it back to `enforce` once the origin is
declared.

## Hardening checklist (run before publishing DNS)

Every item must pass; the first three are the security boundary.

1. **Anonymous is read-only and scoped to `demo`.**
   ```bash
   H=https://demo.avuruobs.io
   curl -fs  $H/api/v1/services | head -c 200          # 200 — demo project reads
   curl -s -o /dev/null -w '%{http_code}\n' \
     -H 'X-Avuru-Tenant: other' $H/api/v1/services      # 403 — cross-project
   curl -s -o /dev/null -w '%{http_code}\n' \
     -X POST $H/api/v1/settings/users                   # 401/403 — no mutation
   ```
2. **Only the UI host is exposed.** The ingress routes `/` → ui and `/api` →
   hub, nothing else: OTLP (4317/4318) and the Sentry ingest port stay
   ClusterIP (`sentryHost: ""`). Verify no LoadBalancer/NodePort leaked:
   `kubectl -n demo-obs get svc`.
3. **No privileged surface.** `sensor.enabled=false` — confirm no DaemonSet:
   `kubectl -n demo-obs get ds` returns nothing.
4. **Rate limiting at the edge.** Uncomment the annotation block in
   `values-public-demo.yaml` for your controller (APISIX limit-req or
   ingress-nginx limit-rps). The hub additionally rate-limits login attempts
   itself (shipped v0.2).
5. **Storage is self-limiting.** Short retention TTLs + a bounded 20Gi PVC;
   anonymous users cannot write. No reset cron is required.
6. **No outbound calls.** Alerting has no rules/channels and the SSRF
   allowlist is empty — the hub makes no egress from the demo box.
7. *(Optional)* Keep the demo out of search results: serve a
   `robots.txt`/`noindex` from the ingress if you prefer link-only traffic.
8. **Uptime check.** Point your monitor at `https://demo.avuruobs.io/healthz`
   (unauthenticated by design).
9. **The CSRF check is still armed.** `auth.originCheck` must read `enforce` —
   `log` and `off` both let a cross-origin write through, and `off` leaves no
   trace that they did. A proxy that rewrites `Host` is fixed with
   `auth.trustedOrigins` (see above), not by lowering the mode:
   `kubectl -n demo-obs get deploy avuruobs-hub -o yaml | grep -c AVURUOBS_AUTH_ORIGIN_CHECK`
   returns `0` on a correctly configured demo.

## Green Obs on the demo

`modules.green` stays **off**: the demo cluster is virtualized and a public
box must not show energy numbers it cannot measure. To showcase Green Obs,
run the demo on bare metal with RAPL/powercap readable and flip
`modules.green.enabled=true` + `sensor.green.enabled=true` (which also means
re-enabling the sensor — weigh the privileged DaemonSet against the demo's
minimal-surface stance; a screenshot tour on the docs site is the safer
default).

## Day-2

- **Upgrade:** re-run the installer with the new `VERSION` (monthly, or per
  release being showcased).
- **Load generator hiccups:** the shop's loadgen occasionally wedges after
  weeks; `kubectl -n demo-obs rollout restart deploy -l app.kubernetes.io/component=load-generator`
  (or schedule it weekly as a CronJob).
- **Teardown:**
  ```bash
  helm uninstall astronomy -n demo-obs
  helm uninstall avuruobs -n demo-obs
  kubectl delete ns demo-obs
  ```

## Post-deploy

Flip the "coming soon" demo links to `https://demo.avuruobs.io` in:
- `README.md` (links line under the badges),
- the docs site (`docusaurus.config.ts` already carries the URL as the Live
  Demo target — verify it resolves).
