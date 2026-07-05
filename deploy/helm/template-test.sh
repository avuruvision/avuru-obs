#!/usr/bin/env bash
# Render-time assertions for the avuruops chart — no cluster needed.
# Run via `make helm-check` or directly: deploy/helm/template-test.sh
set -euo pipefail
cd "$(dirname "$0")"
CHART=avuruops

fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "  ok: $*"; }
render() { helm template test "$CHART" "$@"; }

echo "== helm lint"
helm lint "$CHART" >/dev/null || fail "helm lint"
helm lint "$CHART" -f values-staging.yaml >/dev/null || fail "helm lint (staging overlay)"
helm lint "$CHART" -f values-prod.yaml >/dev/null || fail "helm lint (prod overlay)"
ok "lint (default, staging, prod)"

echo "== default render: collection guardrails"
out="$(render)"
for ns in kube-system kube-node-lease kube-public; do
  grep -q "k8s_namespace: \"$ns\"" <<<"$out" || fail "$ns missing from OBI exclude_instrument"
done
ok "default excludeNamespaces rendered into OBI exclude_instrument"
grep -q 'k8s_namespace: "default"' <<<"$out" || fail "release namespace missing from OBI exclude_instrument"
ok "release namespace always excluded"
grep -q 'k8s_pod_labels:' <<<"$out" || fail "pod opt-out label exclude missing"
grep -q '"avuru.obs/instrument": "false"' <<<"$out" || fail "opt-out label selector wrong"
ok "per-pod opt-out label exclude rendered"
grep -q 'key: avuru.obs/collect' <<<"$out" || fail "node opt-out affinity missing"
ok "per-node opt-out affinity rendered"
grep -q 'priorityClassName' <<<"$out" && fail "priorityClassName rendered without opt-in"
grep -q 'kind: PriorityClass' <<<"$out" && fail "PriorityClass rendered without opt-in"
ok "priorityClass stays off by default"

echo "== priorityClass opt-in"
out="$(render --set sensor.priorityClass.create=true)"
grep -q 'kind: PriorityClass' <<<"$out" || fail "PriorityClass object not rendered"
grep -q 'priorityClassName: test-avuruops-sensor' <<<"$out" || fail "priorityClassName not wired to DaemonSet"
grep -q 'value: -10' <<<"$out" || fail "priority value not rendered"
ok "PriorityClass + DaemonSet wiring"

echo "== opt-outs can be disabled"
out="$(render --set sensor.collection.nodeOptOutLabel="" --set sensor.collection.optOutLabel="")"
grep -q 'nodeAffinity' <<<"$out" && fail "nodeAffinity rendered with empty nodeOptOutLabel"
grep -q 'k8s_pod_labels' <<<"$out" && fail "pod-label exclude rendered with empty optOutLabel"
ok "empty labels disable the opt-out blocks"

echo "== extra namespace excludes merge"
out="$(render --set 'sensor.obi.discovery.excludeNamespaces={payments,billing}')"
grep -q 'k8s_namespace: "payments"' <<<"$out" || fail "extra OBI exclude namespace missing"
grep -q 'k8s_namespace: "billing"' <<<"$out" || fail "extra OBI exclude namespace missing"
ok "sensor.obi.discovery.excludeNamespaces extends the shared list"

echo "== all sensor containers off -> no DaemonSet"
out="$(render --set sensor.obi.enabled=false --set sensor.agent.enabled=false --set sensor.profiler.enabled=false)"
grep -q 'kind: DaemonSet' <<<"$out" && fail "DaemonSet rendered with zero active containers"
ok "DaemonSet omitted when nothing collects"

echo "ALL TEMPLATE ASSERTIONS PASSED"
