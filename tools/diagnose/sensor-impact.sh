#!/usr/bin/env bash
# Read-only evidence collector for "app pods fail probes when avuru-obs is
# installed" (docs/runbooks/app-probe-failures.md). Prints to stdout; never
# mutates the cluster.
set -uo pipefail

NS="${AVURUOPS_NAMESPACE:-avuruops}"
SINCE="${LOG_SINCE:-30m}"

section() { printf '\n===== %s =====\n' "$*"; }

command -v kubectl >/dev/null || { echo "kubectl not found" >&2; exit 1; }

section "cluster + namespace ($NS)"
kubectl version 2>/dev/null | head -3
kubectl get ns "$NS" -o wide 2>&1

section "non-ready pods across ALL namespaces"
kubectl get pods -A 2>/dev/null | awk 'NR==1; $4 != "Running" && $4 != "Completed"; $3 !~ /^([0-9]+)\/\1$/ && NR > 1' | sort -u

section "recent warning events (all namespaces)"
kubectl get events -A --field-selector type=Warning \
  --sort-by=.lastTimestamp 2>/dev/null | tail -40

section "restart counts + last termination (pods with restarts)"
kubectl get pods -A -o custom-columns='NS:.metadata.namespace,POD:.metadata.name,RESTARTS:.status.containerStatuses[*].restartCount,LAST_EXIT:.status.containerStatuses[*].lastState.terminated.exitCode,LAST_REASON:.status.containerStatuses[*].lastState.terminated.reason' 2>/dev/null |
  awk 'NR==1 || ($3 != "<none>" && $3 !~ /^(0,?)+$/)'

section "node capacity / pressure"
kubectl top nodes 2>&1 || echo "(metrics-server unavailable)"
kubectl get nodes -o custom-columns='NAME:.metadata.name,MEMPRESSURE:.status.conditions[?(@.type=="MemoryPressure")].status,DISKPRESSURE:.status.conditions[?(@.type=="DiskPressure")].status,PIDPRESSURE:.status.conditions[?(@.type=="PIDPressure")].status' 2>/dev/null

section "top pods (all namespaces, by cpu)"
kubectl top pods -A --sort-by=cpu 2>&1 | head -25 || true

section "sensor pod placement"
kubectl get pods -n "$NS" -o wide 2>&1
kubectl get ds -n "$NS" 2>&1

section "sensor container logs (last $SINCE)"
for pod in $(kubectl get pods -n "$NS" -l app.kubernetes.io/component=sensor -o name 2>/dev/null); do
  for c in obi otel-agent profiler; do
    echo "--- $pod / $c ---"
    kubectl logs -n "$NS" "$pod" -c "$c" --since="$SINCE" --tail=60 2>&1 | tail -60
  done
done

section "rendered OBI config"
kubectl get configmap -n "$NS" -o name 2>/dev/null | grep sensor-obi |
  xargs -r -I{} kubectl get -n "$NS" {} -o jsonpath='{.data.obi-config\.yml}' 2>/dev/null
echo

section "clickhouse placement + resources"
kubectl get pods -n "$NS" -l app.kubernetes.io/component=clickhouse -o wide 2>&1

section "done"
echo "Attach this output to the issue. Runbook: docs/runbooks/app-probe-failures.md"
