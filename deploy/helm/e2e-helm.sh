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

CLUSTER="${KIND_CLUSTER:-avuruops-e2e}"
NS=avuruops
HUB_IMG=avuru-obs-hub:local
UI_IMG=avuru-obs-ui:local
GW_IMG=avuru-obs-gateway:local
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PF_PIDS=""
cleanup() {
  [ -n "$PF_PIDS" ] && kill $PF_PIDS 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> building hub + ui + gateway images"
docker build -t "$HUB_IMG" -f hub/Dockerfile .
docker build -t "$UI_IMG" -f ui/Dockerfile .
docker build -t "$GW_IMG" -f gateway/Dockerfile .

echo "==> pre-building test + seed binaries (toolchain time is not the product's clock)"
E2E_BIN="$(mktemp -t avuru-e2e.XXXXXX)"
SEED_BIN="$(mktemp -t avuru-seed.XXXXXX)"
( cd e2e && go test -tags=e2ehelm -c -o "$E2E_BIN" . )
( cd tools/seed && go build -o "$SEED_BIN" . )

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

echo "==> creating kind cluster '$CLUSTER'"
kind create cluster --name "$CLUSTER" --wait 120s
kind load docker-image "$HUB_IMG" --name "$CLUSTER"
kind load docker-image "$UI_IMG" --name "$CLUSTER"
kind load docker-image "$GW_IMG" --name "$CLUSTER"
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
helm install avuruops deploy/helm/avuruops -n "$NS" --create-namespace \
  --set hub.repository=avuru-obs-hub --set hub.tag=local \
  --set ui.repository=avuru-obs-ui --set ui.tag=local \
  --set gateway.image.repository=avuru-obs-gateway --set gateway.image.tag=local \
  --set clickhouse.persistence.enabled=false \
  --set clickhouse.resources.requests.cpu=200m \
  --set clickhouse.resources.requests.memory=512Mi \
  --set clickhouse.resources.limits.memory=1536Mi \
  --set gateway.resources.requests.memory=128Mi \
  --set hub.resources.requests.memory=64Mi

echo "==> waiting for the hub to answer (inside the wedge clock)"
kubectl -n "$NS" wait --for=condition=Available deploy/avuruops-hub --timeout=240s
kubectl -n "$NS" port-forward svc/avuruops-hub 8080:80 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 2

echo "==> asserting the <5-min zero-code wedge via the hub API"
( cd e2e && "$E2E_BIN" -test.v -test.timeout 15m -test.run 'TestWedge' )

echo "==> waiting for the remaining deployables + migrate hook"
kubectl -n "$NS" rollout status deploy/avuruops-ui --timeout=180s
kubectl -n "$NS" rollout status deploy/avuruops-gateway --timeout=180s
kubectl -n "$NS" port-forward svc/avuruops-gateway 4318:4318 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
kubectl -n "$NS" port-forward svc/avuruops-ui 8081:80 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 4

echo "==> seeding deterministic OTLP fixtures"
"$SEED_BIN" -endpoint http://localhost:4318 -fixtures deploy/compose/seed/fixtures
sleep 4

echo "==> asserting the UI deployable serves"
code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/)
[ "$code" = "200" ] || { echo "UI pod not serving (HTTP $code)"; exit 1; }
echo "    ui / -> 200"

echo "==> asserting seeded traces + correlated logs via the hub API"
( cd e2e && "$E2E_BIN" -test.v -test.timeout 5m -test.run 'TestSeededViaHelm' )

rm -f "$E2E_BIN" "$SEED_BIN"
