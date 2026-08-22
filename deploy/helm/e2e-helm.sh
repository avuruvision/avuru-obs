#!/usr/bin/env bash
# Helm install smoke + the TTV WEDGE GATE. The promise: point avuru at a
# cluster ALREADY RUNNING apps → live zero-code service map in <5 minutes.
# So: kind up → build/load images → deploy the uninstrumented demo → T0 at
# helm install (NO --wait: a user opens the UI while pods roll) → poll the
# hub API until the map lights up, <300s asserted. Then the classic seeded
# assertions. Toolchain time (docker builds, image pulls, go compiles) is
# excluded from the clock — the promise is about the platform, not the
# harness. Reduced footprint (ephemeral CH) for a laptop VM.
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-avuruobs-e2e}"
NS=avuruobs
HUB_IMG=avuru-obs-hub:local
UI_IMG=avuru-obs-ui:local
GW_IMG=avuru-obs-gateway:local
TDP_IMG=avuru-obs-tdp-estimator:local
# Local port the hub is forwarded to. Overridable for dev machines where an
# unrelated process holds 8080; the test binary follows via AVURUOBS_E2E_HUB_URL.
HUB_PORT_LOCAL="${HUB_PORT_LOCAL:-8080}"
export AVURUOBS_E2E_HUB_URL="http://localhost:${HUB_PORT_LOCAL}"
# Local ports the gateway's OTLP/HTTP (seeding) and the UI (serve check) are
# forwarded to. Overridable for dev machines where something already binds
# 4318/8081 — a failed bind would silently point the seed/assertion at the
# WRONG process.
GW_PORT_LOCAL="${GW_PORT_LOCAL:-4318}"
UI_PORT_LOCAL="${UI_PORT_LOCAL:-8081}"
# Local ports for the wider-ingest receiver port-forwards (compat leg below).
# High and away from the compose compat overlay's remapped 2xxxx ports so this
# can run next to a live compose stack on a shared machine.
JAEGER_PORT_LOCAL="${JAEGER_PORT_LOCAL:-34250}"
ZIPKIN_PORT_LOCAL="${ZIPKIN_PORT_LOCAL:-39411}"
PROMRW_PORT_LOCAL="${PROMRW_PORT_LOCAL:-39291}"
LOKI_PORT_LOCAL="${LOKI_PORT_LOCAL:-33100}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PF_PIDS=""
cleanup() {
  [ -n "$PF_PIDS" ] && kill $PF_PIDS 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> building hub + ui + gateway + tdp-estimator images"
docker build -t "$HUB_IMG" -f hub/Dockerfile .
docker build -t "$UI_IMG" -f ui/Dockerfile .
docker build -t "$GW_IMG" -f gateway/Dockerfile .
docker build -t "$TDP_IMG" -f sensor/tdp-estimator/Dockerfile .

echo "==> pre-building test + seed + compatsend binaries (toolchain time is not the product's clock)"
E2E_BIN="$(mktemp -t avuru-e2e.XXXXXX)"
SEED_BIN="$(mktemp -t avuru-seed.XXXXXX)"
COMPAT_BIN="$(mktemp -t avuru-compatsend.XXXXXX)"
( cd e2e && go test -tags=e2ehelm -c -o "$E2E_BIN" . )
( cd tools/seed && go build -o "$SEED_BIN" . )
( cd tools/compatsend && go build -o "$COMPAT_BIN" . )

echo "==> pre-pulling wedge demo + sensor images (pull time is not the product's clock)"
# Keep in sync with deploy/demo/wedge/wedge.yaml and values.yaml sensor pins.
DEMO_IMGS=(nginx:1.29-alpine busybox:1.37)
SENSOR_IMGS=(
  otel/ebpf-instrument:v0.9.0
  otel/opentelemetry-collector-contrib:0.154.0
  otel/opentelemetry-collector-ebpf-profiler:0.155.0
)
# Explicit single platform: kind's ctr import rejects multi-arch manifest
# lists whose foreign blobs aren't local ("content digest not found").
PLATFORM="linux/$(docker version -f '{{.Server.Arch}}')"
for img in "${DEMO_IMGS[@]}" "${SENSOR_IMGS[@]}"; do docker pull -q --platform "$PLATFORM" "$img" >/dev/null; done

# Kepler (green module — keep in sync with values.yaml sensor.green pins).
# Upstream publishes linux/amd64 only (no arm64 manifest as of v0.11.4), so on
# an arm64 dev host fall back to the amd64 image: kind runs it through the
# host's binfmt emulation (Rosetta/qemu). CI runners are amd64-native.
KEPLER_IMG=quay.io/sustainable_computing_io/kepler:v0.11.4
KEPLER_PLATFORM="$PLATFORM"
if ! docker pull -q --platform "$KEPLER_PLATFORM" "$KEPLER_IMG" >/dev/null 2>&1; then
  KEPLER_PLATFORM=linux/amd64
  echo "    NOTE: $KEPLER_IMG has no $PLATFORM manifest — using $KEPLER_PLATFORM via binfmt emulation"
  docker pull -q --platform "$KEPLER_PLATFORM" "$KEPLER_IMG" >/dev/null
fi

echo "==> creating kind cluster '$CLUSTER'"
kind create cluster --name "$CLUSTER" --wait 120s
kind load docker-image "$HUB_IMG" --name "$CLUSTER"
kind load docker-image "$UI_IMG" --name "$CLUSTER"
kind load docker-image "$GW_IMG" --name "$CLUSTER"
kind load docker-image "$TDP_IMG" --name "$CLUSTER"
# Public images: docker's containerd store keeps multi-arch manifest lists,
# which `kind load docker-image` can't import (foreign digests missing) —
# export a single platform explicitly instead.
for img in "${DEMO_IMGS[@]}" "${SENSOR_IMGS[@]}"; do
  tar="$(mktemp -t wedge-img.XXXXXX).tar"
  if ! docker save --platform "$PLATFORM" -o "$tar" "$img" 2>/dev/null; then
    # Stale/partial local manifest — re-fetch the platform and retry once.
    docker pull -q --platform "$PLATFORM" "$img" >/dev/null 2>&1 || true
    if ! docker save --platform "$PLATFORM" -o "$tar" "$img" 2>/dev/null; then
      echo "    NOTE: not pre-loading $img (no $PLATFORM archive) — the cluster will pull it"
      rm -f "$tar"; continue
    fi
  fi
  kind load image-archive "$tar" --name "$CLUSTER"
  rm -f "$tar"
done
# Kepler loads with ITS platform (may differ from $PLATFORM, see the pull above).
tar="$(mktemp -t wedge-img.XXXXXX).tar"
if docker save --platform "$KEPLER_PLATFORM" -o "$tar" "$KEPLER_IMG" 2>/dev/null; then
  kind load image-archive "$tar" --name "$CLUSTER"
else
  echo "    NOTE: not pre-loading $KEPLER_IMG (no $KEPLER_PLATFORM archive) — the cluster will pull it"
fi
rm -f "$tar"

# The wedge promise is about a cluster ALREADY RUNNING apps: the demo goes
# in first (it needs nothing from the platform).
echo "==> deploying the UNINSTRUMENTED wedge demo (zero OTel anywhere)"
kubectl apply -f deploy/demo/wedge/wedge.yaml
kubectl -n wedge-demo wait --for=condition=Available deploy --all --timeout=180s

echo "==> helm install (T0 for the wedge clock — no --wait, users don't wait either)"
WEDGE_T0_UNIX=$(date +%s)
export WEDGE_T0_UNIX
# pullPolicy stays IfNotPresent (default): the loaded hub/ui images are present,
# so they are never pulled; ClickHouse image pulls from the registry.
# auth is ON by default (secure-by-default). Pin a known bootstrap password so
# the test binary can log in deterministically (loginAs → e2e-admin-pw), the
# same contract as the compose suite's Makefile `e2e` target.
# Green rides the SAME install (born-off in the chart; this leg opts in): the
# pinned Kepler joins the sensor DaemonSet with its dev fake-cpu-meter so kind
# — which has no RAPL — proves do-no-harm with energy actually flowing. The
# TTV wedge gate and the probe-canary gate below run UNCHANGED against it.
# tdp-estimator ALSO joins on the same install: unlike Kepler, kind nodes
# having no powercap is exactly the estimator's real activation condition
# (probe fails -> estimate), so no fake-meter equivalent is needed on its
# side — this is the estimator's genuine "probe fails -> estimate" path
# running in CI, not a synthetic stand-in.
helm install avuruobs deploy/helm/avuruobs -n "$NS" --create-namespace \
  --set hub.repository=avuru-obs-hub --set hub.tag=local \
  --set ui.repository=avuru-obs-ui --set ui.tag=local \
  --set gateway.image.repository=avuru-obs-gateway --set gateway.image.tag=local \
  --set auth.adminPassword=e2e-admin-pw \
  --set clickhouse.persistence.enabled=false \
  --set clickhouse.resources.requests.cpu=200m \
  --set clickhouse.resources.requests.memory=512Mi \
  --set clickhouse.resources.limits.memory=1536Mi \
  --set gateway.resources.requests.memory=128Mi \
  --set hub.resources.requests.memory=64Mi \
  --set modules.green.enabled=true \
  --set sensor.green.enabled=true \
  --set sensor.green.fakeCpuMeter=true \
  --set sensor.green.estimation.enabled=true \
  --set sensor.green.estimation.image.repository=avuru-obs-tdp-estimator \
  --set sensor.green.estimation.image.tag=local \
  --set collection.runtimeControl.enabled=true \
  --set gateway.receivers.jaeger.enabled=true \
  --set gateway.receivers.zipkin.enabled=true \
  --set gateway.receivers.prometheusRemoteWrite.enabled=true \
  --set gateway.receivers.loki.enabled=true \
  --set gateway.forward.otlp.enabled=true \
  --set gateway.forward.otlp.endpoint=forward-sink:4317 \
  --set gateway.forward.otlp.insecure=true \
  --set "gateway.forward.otlp.signals={traces}"
# The last block above is WIDER INGEST riding the SAME install (born-off in
# the chart; this leg opts in, like green): all four compat receivers plus
# otlp/forward dual-write pointed at the forward-sink fixture below. The TTV
# wedge gate and every other gate run UNCHANGED against it — which is itself
# the assertion that enabling the compat surface changes nothing else.
# auth.ingest.mode stays at its `log` default, so unkeyed compat sends land;
# enforce-mode rejection is the compose suite's job (make e2e-compat).

# Stand-in legacy backend for the dual-write assertion (image is pre-pulled
# with the sensor images — same contrib pin). Applied right after install so
# it rolls while the platform does; the forward exporter's sending queue
# tolerates the window where the Service exists but the pod is still coming up.
echo "==> deploying the forward-sink fixture (dual-write target)"
kubectl -n "$NS" apply -f deploy/helm/e2e-forward-sink.yaml

echo "==> waiting for the hub to answer (inside the wedge clock)"
kubectl -n "$NS" wait --for=condition=Available deploy/avuruobs-hub --timeout=240s
kubectl -n "$NS" port-forward svc/avuruobs-hub "${HUB_PORT_LOCAL}:80" >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 2

echo "==> asserting the <5-min zero-code wedge via the hub API"
( cd e2e && "$E2E_BIN" -test.v -test.timeout 15m -test.run 'TestWedge' )

echo "==> asserting the hub sees a complete schema"
# The migrate Job is a post-* hook, so a timed-out `helm --wait` skips it and
# leaves every table missing while the pods look healthy. The hub repairs that
# itself; either way it must end up reporting the schema ready.
SCHEMA_DEADLINE=$(( $(date +%s) + 120 ))
while :; do
  # grep -c for the same reason as the forward-sink poll below: a short-circuiting
  # grep -q SIGPIPEs the producer, and pipefail turns that into a false negative
  # once the log outgrows the pipe buffer.
  ready_hits=$(kubectl -n "$NS" logs deploy/avuruobs-hub --tail=-1 2>/dev/null | grep -cF 'clickhouse schema ready' || true)
  if [ "${ready_hits:-0}" -gt 0 ]; then
    echo "    hub reports the schema ready"
    break
  fi
  if [ "$(date +%s)" -ge "$SCHEMA_DEADLINE" ]; then
    echo "hub never reported 'clickhouse schema ready' within 120s"
    kubectl -n "$NS" logs deploy/avuruobs-hub --tail=40 || true
    kubectl -n "$NS" logs job/avuruobs-migrate --tail=40 || true
    exit 1
  fi
  sleep 5
done

echo "==> waiting for the remaining deployables + migrate hook"
kubectl -n "$NS" rollout status deploy/avuruobs-ui --timeout=180s
kubectl -n "$NS" rollout status deploy/avuruobs-gateway --timeout=180s
kubectl -n "$NS" rollout status ds/avuruobs-sensor --timeout=180s
SENSOR_READY_UNIX=$(date +%s)
# Pin the canary pod's identity at sensor-ready: the gate later asserts the
# SAME pod survived — restartCount alone is blind to a kill-and-replace
# (a fresh pod restarts at 0), which is exactly the regression to catch.
CANARY_UID_T0=$(kubectl -n wedge-demo get pods -l app=probe-canary -o jsonpath='{.items[*].metadata.uid}')
case "$CANARY_UID_T0" in
  "") echo "probe-canary absent at sensor-ready — the gate lost its probe-sensitive coverage"; exit 1 ;;
  *" "*) echo "expected one probe-canary pod at sensor-ready, got uids: $CANARY_UID_T0"
         kubectl -n wedge-demo get pods -l app=probe-canary; exit 1 ;;
esac
kubectl -n "$NS" port-forward svc/avuruobs-gateway "${GW_PORT_LOCAL}:4318" >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
kubectl -n "$NS" port-forward svc/avuruobs-ui "${UI_PORT_LOCAL}:80" >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 4

echo "==> seeding deterministic OTLP fixtures"
"$SEED_BIN" -endpoint "http://localhost:${GW_PORT_LOCAL}" -fixtures deploy/compose/seed/fixtures
sleep 4

echo "==> asserting the UI deployable serves"
code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:${UI_PORT_LOCAL}/")
[ "$code" = "200" ] || { echo "UI pod not serving (HTTP $code)"; exit 1; }
echo "    ui / -> 200"

echo "==> asserting seeded traces + correlated logs via the hub API"
( cd e2e && "$E2E_BIN" -test.v -test.timeout 5m -test.run 'TestSeededViaHelm' )

# WIDER INGEST (design/2026-07-27-wider-ingest-compat.md): the kind mirror of
# the compose compat suite (`make e2e-compat`) — the same receiver/forward
# shapes, but rendered BY THE CHART FLAGS on a real install and reached through
# the chart's Service ports. One compatsend fixture per protocol, then rows
# asserted straight in ClickHouse (tenant defaults to `default`: no enforce,
# no gateway.tenant on this install) and the dual-write proven from the
# forward-sink's debug logs. This is what makes the README claim ("point your
# Jaeger/Zipkin/remote-write/Loki sender at us") empirically true in CI.
echo "==> WIDER INGEST: waiting for the forward-sink fixture"
kubectl -n "$NS" wait --for=condition=Available deploy/forward-sink --timeout=120s

echo "==> WIDER INGEST: port-forwarding the compat receiver ports"
kubectl -n "$NS" port-forward svc/avuruobs-gateway \
  "${JAEGER_PORT_LOCAL}:14250" "${ZIPKIN_PORT_LOCAL}:9411" \
  "${PROMRW_PORT_LOCAL}:9291" "${LOKI_PORT_LOCAL}:3100" >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 3

# Fixed ids/names for grep-ability (fresh cluster per run, so no collisions),
# distinct from the compose suite's cafe000x ids to keep origins unambiguous.
COMPAT_SVC=helm-compat-probe
COMPAT_JAEGER_TRACE=cafe1002bbbb2222cccc3333dddd4444
COMPAT_ZIPKIN_TRACE=cafe1003bbbb2222cccc3333dddd4444
COMPAT_METRIC=helm_compat_gauge
COMPAT_LINE="helm compat conformance log line"

echo "==> WIDER INGEST: one compatsend fixture per protocol"
"$COMPAT_BIN" -proto jaeger -endpoint "localhost:${JAEGER_PORT_LOCAL}" \
  -service "$COMPAT_SVC" -trace-id "$COMPAT_JAEGER_TRACE"
"$COMPAT_BIN" -proto zipkin -endpoint "http://localhost:${ZIPKIN_PORT_LOCAL}" \
  -service "$COMPAT_SVC" -trace-id "$COMPAT_ZIPKIN_TRACE"
"$COMPAT_BIN" -proto promrw -endpoint "http://localhost:${PROMRW_PORT_LOCAL}" \
  -service "$COMPAT_SVC" -metric "$COMPAT_METRIC"
"$COMPAT_BIN" -proto loki -endpoint "http://localhost:${LOKI_PORT_LOCAL}" \
  -label "service=${COMPAT_SVC}" -line "$COMPAT_LINE"

# Same landings the compose suite asserts: Jaeger + Zipkin spans in
# otel_traces, the remote-write v2 gauge in otel_metrics_gauge with
# ServiceName mapped from `job`, and the Loki line in otel_logs with the
# stream label as a log-RECORD attribute (never resource identity).
echo "==> WIDER INGEST: polling ClickHouse for one landing per protocol"
COMPAT_SQL="SELECT
  (SELECT count() FROM otel.otel_traces WHERE Tenant = 'default' AND ServiceName = '${COMPAT_SVC}' AND TraceId = '${COMPAT_JAEGER_TRACE}'),
  (SELECT count() FROM otel.otel_traces WHERE Tenant = 'default' AND ServiceName = '${COMPAT_SVC}' AND TraceId = '${COMPAT_ZIPKIN_TRACE}'),
  (SELECT count() FROM otel.otel_metrics_gauge WHERE Tenant = 'default' AND ServiceName = '${COMPAT_SVC}' AND MetricName = '${COMPAT_METRIC}'),
  (SELECT count() FROM otel.otel_logs WHERE Tenant = 'default' AND LogAttributes['service'] = '${COMPAT_SVC}' AND Body = '${COMPAT_LINE}')"
COMPAT_DEADLINE=$(( $(date +%s) + 120 ))
while :; do
  counts=$(kubectl -n "$NS" exec avuruobs-clickhouse-0 -- \
    clickhouse-client -u avuru --password avuru -q "$COMPAT_SQL" 2>/dev/null || echo "")
  jaeger_rows=$(awk '{print $1}' <<<"$counts")
  zipkin_rows=$(awk '{print $2}' <<<"$counts")
  promrw_rows=$(awk '{print $3}' <<<"$counts")
  loki_rows=$(awk '{print $4}' <<<"$counts")
  if [ "${jaeger_rows:-0}" -gt 0 ] 2>/dev/null && [ "${zipkin_rows:-0}" -gt 0 ] 2>/dev/null \
    && [ "${promrw_rows:-0}" -gt 0 ] 2>/dev/null && [ "${loki_rows:-0}" -gt 0 ] 2>/dev/null; then
    echo "    landings: jaeger=$jaeger_rows zipkin=$zipkin_rows promrw=$promrw_rows loki=$loki_rows"
    break
  fi
  if [ "$(date +%s)" -ge "$COMPAT_DEADLINE" ]; then
    echo "WIDER INGEST: not every protocol landed within 120s (jaeger=${jaeger_rows:-?} zipkin=${zipkin_rows:-?} promrw=${promrw_rows:-?} loki=${loki_rows:-?})"
    kubectl -n "$NS" logs deploy/avuruobs-gateway --tail=40 || true
    exit 1
  fi
  sleep 5
done

# Dual-write: the Jaeger-ingested trace id must reach the forward sink — proof
# the chart-rendered otlp/forward exporter fans out what the compat receivers
# ingest, through a real in-cluster Service.
echo "==> WIDER INGEST: asserting dual-write in the forward-sink logs"
SINK_DEADLINE=$(( $(date +%s) + 120 ))
while :; do
  # grep -c, not grep -q: the sink's detailed debug output is megabytes, so a
  # `grep -q` that exits on the first match SIGPIPEs kubectl, and under
  # `set -o pipefail` that 141 makes the whole pipeline look like "no match" —
  # the assertion would then fail on a trace that DID arrive. -c reads to EOF,
  # so the producer always exits cleanly.
  sink_hits=$(kubectl -n "$NS" logs deploy/forward-sink --tail=-1 2>/dev/null | grep -cF "$COMPAT_JAEGER_TRACE" || true)
  if [ "${sink_hits:-0}" -gt 0 ]; then
    echo "    trace $COMPAT_JAEGER_TRACE present in forward-sink logs — otlp/forward fans out"
    break
  fi
  if [ "$(date +%s)" -ge "$SINK_DEADLINE" ]; then
    echo "WIDER INGEST: trace $COMPAT_JAEGER_TRACE never reached the forward sink within 120s"
    kubectl -n "$NS" logs deploy/forward-sink --tail=20 || true
    kubectl -n "$NS" logs deploy/avuruobs-gateway --tail=40 || true
    exit 1
  fi
  sleep 5
done

# GREEN: the fake-cpu-meter Kepler leg. The seeded fixtures also carry
# kepler_* rows (ScopeName avuru-seed-kepler), so the poll EXCLUDES seeded
# scopes: only rows the sensor's prometheus receiver scraped from the real
# pinned Kepler count. This empirically pins the STORED metric names against
# the pinned image (an AEP verify item) — a Kepler rename would fail here, not
# silently return an empty /green. Runs before the soak sleep so the poll
# reuses otherwise-dead soak time; the soak/canary gates below are unchanged.
echo "==> GREEN: polling ClickHouse for Kepler energy rows scraped from the pinned image"
KEPLER_SQL="SELECT countIf(MetricName = 'kepler_node_cpu_joules_total'), countIf(MetricName = 'kepler_pod_cpu_joules_total') FROM otel.otel_metrics_sum WHERE MetricName LIKE 'kepler%' AND ScopeName NOT LIKE 'avuru-seed%'"
KEPLER_DEADLINE=$(( $(date +%s) + 240 ))
while :; do
  counts=$(kubectl -n "$NS" exec avuruobs-clickhouse-0 -- \
    clickhouse-client -u avuru --password avuru -q "$KEPLER_SQL" 2>/dev/null || echo "")
  node_rows=$(awk '{print $1}' <<<"$counts")
  pod_rows=$(awk '{print $2}' <<<"$counts")
  if [ "${node_rows:-0}" -gt 0 ] 2>/dev/null && [ "${pod_rows:-0}" -gt 0 ] 2>/dev/null; then
    echo "    kepler rows in otel_metrics_sum (scraped, non-seeded): node=$node_rows pod=$pod_rows"
    break
  fi
  if [ "$(date +%s)" -ge "$KEPLER_DEADLINE" ]; then
    echo "GREEN: no scraped kepler_(node|pod)_cpu_joules_total rows within 240s (node=${node_rows:-?} pod=${pod_rows:-?})"
    kubectl -n "$NS" logs ds/avuruobs-sensor -c kepler --tail=30 || true
    kubectl -n "$NS" logs ds/avuruobs-sensor -c otel-agent --tail=15 || true
    exit 1
  fi
  sleep 5
done

# GREEN TDP ESTIMATION: kind nodes have no powercap, so the estimator's own
# RAPL probe fails independently of Kepler's fake-cpu-meter — this is its
# genuine "probe fails -> estimate" activation path, not a synthetic stand-in
# (design/2026-07-28-green-tdp-estimation.md). Same rows Kepler produces
# (kepler_(node|pod)_cpu_joules_total) but stamped avuruobs_quality
# "estimated" by transform/green_quality, distinguishing them from Kepler's
# own "measured" rows in the SAME window.
echo "==> GREEN TDP ESTIMATION: polling ClickHouse for tdp-estimator rows stamped estimated"
TDP_SQL="SELECT count() FROM otel.otel_metrics_sum WHERE MetricName LIKE 'kepler%' AND Attributes['avuruobs_quality'] = 'estimated' AND ScopeName NOT LIKE 'avuru-seed%'"
TDP_DEADLINE=$(( $(date +%s) + 240 ))
while :; do
  estimated_rows=$(kubectl -n "$NS" exec avuruobs-clickhouse-0 -- \
    clickhouse-client -u avuru --password avuru -q "$TDP_SQL" 2>/dev/null || echo "")
  if [ "${estimated_rows:-0}" -gt 0 ] 2>/dev/null; then
    echo "    estimated-quality rows in otel_metrics_sum (scraped, non-seeded): $estimated_rows"
    break
  fi
  if [ "$(date +%s)" -ge "$TDP_DEADLINE" ]; then
    echo "GREEN TDP ESTIMATION: no estimated-quality rows within 240s (rows=${estimated_rows:-?})"
    kubectl -n "$NS" logs ds/avuruobs-sensor -c tdp-estimator --tail=30 || true
    kubectl -n "$NS" logs ds/avuruobs-sensor -c otel-agent --tail=15 || true
    exit 1
  fi
  sleep 5
done

# REGRESSION GATE (docs/runbooks/app-probe-failures.md): installing avuru-obs
# must not destabilize apps that were already running. The wedge demo predates
# the install — after a soak with the sensor attached, every one of its pods
# must still be Ready with zero restarts.
SOAK=75
elapsed=$(( $(date +%s) - SENSOR_READY_UNIX ))
if [ "$elapsed" -lt "$SOAK" ]; then
  echo "==> soaking pre-existing apps under the sensor ($((SOAK - elapsed))s remaining of ${SOAK}s)"
  sleep $((SOAK - elapsed))
fi
echo "==> REGRESSION GATE: pre-existing wedge-demo pods after ${SOAK}s under the sensor"
kubectl -n wedge-demo get pods
UNHEALTHY=$(kubectl -n wedge-demo get pods --no-headers \
  | awk '{ split($2, a, "/"); if (a[1] != a[2] || $4+0 > 0) print $1 " (" $2 " ready, " $4 " restarts)" }')
if [ -n "$UNHEALTHY" ]; then
  echo "REGRESSION: pre-existing app pods destabilized by the avuru-obs install:"
  echo "$UNHEALTHY" | sed 's/^/    /'
  exit 1
fi
echo "    all wedge-demo pods Ready with 0 restarts — install did no harm"

# The generic sweep above covers every wedge pod, but the probe-sensitive
# canary is the load-bearing subject (AEP 2026-07-17): assert it BY NAME (so
# renaming or dropping it can't silently gut the gate) and BY UID (so a
# kill-and-replace can't slip past restartCount, which resets on a new pod).
echo "==> REGRESSION GATE: the probe-sensitive canary specifically (AEP 2026-07-17)"
CANARY_STATE=$(kubectl -n wedge-demo get pods -l app=probe-canary \
  -o jsonpath='{range .items[*]}{.metadata.uid} {.status.containerStatuses[0].restartCount} {.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}')
CANARY_ROW=$(printf '%s\n' "$CANARY_STATE" | grep "^$CANARY_UID_T0 " || true)
if [ -z "$CANARY_ROW" ]; then
  echo "REGRESSION: the probe-canary pod was REPLACED during the soak (uid $CANARY_UID_T0 is gone) — a kill/eviction, not a survival"
  kubectl -n wedge-demo get pods -l app=probe-canary
  exit 1
fi
read -r _ CANARY_RESTARTS CANARY_READY <<EOF
$CANARY_ROW
EOF
if [ "$CANARY_READY" != "True" ] || [ "$CANARY_RESTARTS" != "0" ]; then
  echo "REGRESSION: probe-canary Ready=$CANARY_READY restarts=$CANARY_RESTARTS after the ${SOAK}s soak since the platform install"
  kubectl -n wedge-demo describe pod -l app=probe-canary | tail -30
  exit 1
fi
echo "    probe-canary survived: same pod, Ready, 0 restarts through the soak with the sensor attached"

# COLLECTION RUNTIME CONTROL (design/2026-07-27-collection-control-plane.md).
# The applier is the one half of this feature that cannot be tested without a
# cluster: it Updates the sensor ConfigMaps through client-go under a Role
# scoped by resourceNames, and patches a HUB-OWNED annotation onto the
# DaemonSet pod template to force the rollout. Unit tests use a fake clientset,
# so until here nothing had proven the RBAC is sufficient or that the patch
# shape Kubernetes accepts is the one we send.
#
# Runs LAST on purpose: a successful apply deliberately rolls the sensor
# DaemonSet, which would invalidate the soak and canary gates above. Everything
# before this point ran with the flag ON and the overlay EMPTY, which is itself
# the assertion that enabling runtime control changes nothing until used.
echo "==> COLLECTION RUNTIME CONTROL: applying an overlay through the hub API"
COOKIES="$(mktemp -t avuru-e2e-cookies.XXXXXX)"
login_code=$(curl -s -o /dev/null -w "%{http_code}" -c "$COOKIES" \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin","password":"e2e-admin-pw"}' \
  "${AVURUOBS_E2E_HUB_URL}/api/v1/auth/login")
[ "$login_code" = "200" ] || { echo "admin login for the overlay write failed (HTTP $login_code)"; exit 1; }

# excludeNamespaces rather than a signal toggle: it changes OBI's ConfigMap
# content observably without switching a signal off, so a failure here is
# unambiguously the applier and not a knock-on from losing collection.
PROBE_NS=e2e-overlay-probe
CHECKSUM_BEFORE=$(kubectl -n "$NS" get ds avuruobs-sensor \
  -o jsonpath='{.spec.template.metadata.annotations.avuru\.obs/overlay-checksum}' 2>/dev/null || true)
put_code=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIES" -X PUT \
  -H 'Content-Type: application/json' \
  -d "{\"excludeNamespaces\":[\"kube-system\",\"kube-node-lease\",\"kube-public\",\"${NS}\",\"${PROBE_NS}\"]}" \
  "${AVURUOBS_E2E_HUB_URL}/api/v1/collection/overlay")
[ "$put_code" = "200" ] || { echo "overlay PUT failed (HTTP $put_code)"; kubectl -n "$NS" logs deploy/avuruobs-hub --tail=40; exit 1; }

# The write is synchronous with the apply (collectionMu serializes save+apply),
# so the ConfigMap is expected to be current the moment the PUT returns. Poll
# briefly anyway rather than racing the API server's read-after-write.
APPLY_DEADLINE=$(( $(date +%s) + 60 ))
while :; do
  obi_cm=$(kubectl -n "$NS" get cm avuruobs-sensor-obi -o jsonpath='{.data}' 2>/dev/null || true)
  checksum_after=$(kubectl -n "$NS" get ds avuruobs-sensor \
    -o jsonpath='{.spec.template.metadata.annotations.avuru\.obs/overlay-checksum}' 2>/dev/null || true)
  if grep -q "$PROBE_NS" <<<"$obi_cm" && [ -n "$checksum_after" ] && [ "$checksum_after" != "$CHECKSUM_BEFORE" ]; then
    echo "    overlay reached the cluster: OBI ConfigMap excludes $PROBE_NS; DaemonSet checksum ${checksum_after:0:12}…"
    break
  fi
  if [ "$(date +%s)" -ge "$APPLY_DEADLINE" ]; then
    echo "COLLECTION RUNTIME CONTROL: overlay saved but never reached the cluster within 60s"
    echo "  (a 200 from the API with no ConfigMap change is the NoopApplier fallback —"
    echo "   missing release-identity env, or RBAC the Role does not actually grant)"
    kubectl -n "$NS" logs deploy/avuruobs-hub --tail=40 || true
    kubectl -n "$NS" get cm avuruobs-sensor-obi -o yaml | head -40 || true
    exit 1
  fi
  sleep 3
done

# Reset must reconcile back, not merely forget: the ConfigMap has to lose the
# probe namespace again. A DELETE that only clears the stored row would leave
# the cluster diverged from what the UI then reports.
del_code=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIES" -X DELETE \
  "${AVURUOBS_E2E_HUB_URL}/api/v1/collection/overlay")
[ "$del_code" = "200" ] || [ "$del_code" = "204" ] || { echo "overlay DELETE failed (HTTP $del_code)"; exit 1; }
RESET_DEADLINE=$(( $(date +%s) + 60 ))
while :; do
  obi_cm=$(kubectl -n "$NS" get cm avuruobs-sensor-obi -o jsonpath='{.data}' 2>/dev/null || true)
  if ! grep -q "$PROBE_NS" <<<"$obi_cm"; then
    echo "    reset reconciled: OBI ConfigMap back to chart defaults"
    break
  fi
  if [ "$(date +%s)" -ge "$RESET_DEADLINE" ]; then
    echo "COLLECTION RUNTIME CONTROL: reset did not reconcile the cluster within 60s"
    kubectl -n "$NS" logs deploy/avuruobs-hub --tail=40 || true
    exit 1
  fi
  sleep 3
done
rm -f "$COOKIES"

rm -f "$E2E_BIN" "$SEED_BIN" "$COMPAT_BIN"
