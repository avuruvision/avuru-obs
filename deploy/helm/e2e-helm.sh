#!/usr/bin/env bash
# Helm install smoke: kind up → build+load hub & ui images → helm install →
# seed deterministic OTLP → assert traces + correlated logs via the hub API,
# and the UI pod serves. Reduced footprint (ephemeral CH) for a laptop VM.
set -euo pipefail

CLUSTER="${KIND_CLUSTER:-avuruops-e2e}"
NS=avuruops
HUB_IMG=avuru-obs-hub:local
UI_IMG=avuru-obs-ui:local
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PF_PIDS=""
cleanup() {
  [ -n "$PF_PIDS" ] && kill $PF_PIDS 2>/dev/null || true
  kind delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "==> building hub + ui images"
docker build -t "$HUB_IMG" -f hub/Dockerfile .
docker build -t "$UI_IMG" -f ui/Dockerfile .

echo "==> creating kind cluster '$CLUSTER'"
kind create cluster --name "$CLUSTER" --wait 120s
kind load docker-image "$HUB_IMG" --name "$CLUSTER"
kind load docker-image "$UI_IMG" --name "$CLUSTER"

echo "==> helm install"
# pullPolicy stays IfNotPresent (default): the loaded hub/ui images are present,
# so they are never pulled; ClickHouse + gateway images pull from registries.
helm install avuruops deploy/helm/avuruops -n "$NS" --create-namespace \
  --set hub.repository=avuru-obs-hub --set hub.tag=local \
  --set ui.repository=avuru-obs-ui --set ui.tag=local \
  --set clickhouse.persistence.enabled=false \
  --set clickhouse.resources.requests.cpu=200m \
  --set clickhouse.resources.requests.memory=512Mi \
  --set clickhouse.resources.limits.memory=1536Mi \
  --set gateway.resources.requests.memory=128Mi \
  --set hub.resources.requests.memory=64Mi \
  --wait --timeout 6m

echo "==> port-forwarding gateway + hub + ui"
kubectl -n "$NS" port-forward svc/avuruops-gateway 4318:4318 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
kubectl -n "$NS" port-forward svc/avuruops-hub 8080:80 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
kubectl -n "$NS" port-forward svc/avuruops-ui 8081:80 >/dev/null 2>&1 &
PF_PIDS="$PF_PIDS $!"
sleep 4

echo "==> seeding deterministic OTLP fixtures"
( cd tools/seed && go run . -endpoint http://localhost:4318 -fixtures ../../deploy/compose/seed/fixtures )
sleep 4

echo "==> asserting the UI deployable serves"
code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/)
[ "$code" = "200" ] || { echo "UI pod not serving (HTTP $code)"; exit 1; }
echo "    ui / -> 200"

echo "==> asserting traces + correlated logs via the hub API"
cd e2e && go test -tags=e2ehelm -count=1 -v ./...
