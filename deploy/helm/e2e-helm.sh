#!/usr/bin/env bash
# Helm install smoke: kind up → build+load hub image → helm install → seed
# deterministic OTLP → assert traces + correlated logs via the hub API.
# Reduced footprint (ephemeral CH, small limits) for a laptop Docker VM.
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-avuruops-e2e}"
NS=avuruops
HUB_IMG=avuru-obs-hub:local
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PF_PIDS=""
cleanup() {
  [ -n "$PF_PIDS" ] && kill $PF_PIDS 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> building hub image"
docker build -t "$HUB_IMG" -f hub/Dockerfile .

echo "==> creating kind cluster '$CLUSTER'"
kind create cluster --name "$CLUSTER" --wait 120s
kind load docker-image "$HUB_IMG" --name "$CLUSTER"

echo "==> helm install"
# pullPolicy stays IfNotPresent (default): the loaded hub image is present, so
# it is never pulled; ClickHouse + gateway images pull from their registries.
helm install avuruops deploy/helm/avuruops -n "$NS" --create-namespace \
  --set hub.repository=avuru-obs-hub --set hub.tag=local \
  --set clickhouse.persistence.enabled=false \
  --set clickhouse.resources.requests.cpu=200m \
  --set clickhouse.resources.requests.memory=512Mi \
  --set clickhouse.resources.limits.memory=1536Mi \
  --set gateway.resources.requests.memory=128Mi \
  --set hub.resources.requests.memory=64Mi \
  --wait --timeout 6m

echo "==> port-forwarding gateway + hub"
kubectl -n "$NS" port-forward svc/avuruops-gateway 4318:4318 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
kubectl -n "$NS" port-forward svc/avuruops-hub 8080:80 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 4

echo "==> seeding deterministic OTLP fixtures"
( cd tools/seed && go run . -endpoint http://localhost:4318 -fixtures ../../deploy/compose/seed/fixtures )
sleep 4

echo "==> asserting traces + correlated logs via the hub API"
cd e2e && go test -tags=e2ehelm -count=1 -v ./...
