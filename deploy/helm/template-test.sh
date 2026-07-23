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

echo "== OBI discovery mode: default optOut"
out="$(render)"
grep -q '"avuru.obs/instrument": "true"' <<<"$out" && fail "opt-in selector rendered in default optOut mode"
ok "optOut default: instrument-all, no opt-in selector"

echo "== OBI discovery mode: optIn narrows uprobes, nothing else"
out="$(render --set sensor.obi.discovery.mode=optIn)"
grep -q '"avuru.obs/instrument": "true"' <<<"$out" || fail "opt-in label selector missing in optIn"
grep -q 'k8s_namespace: "kube-system"' <<<"$out" || fail "namespace excludes lost in optIn"
grep -q '"avuru.obs/instrument": "false"' <<<"$out" || fail "per-pod opt-out exclude lost in optIn"
ok "optIn: labeled-only uprobes; excludes unaffected"

echo "== OBI discovery mode: guards"
render --set sensor.obi.discovery.mode=optIn --set sensor.collection.optOutLabel="" >/dev/null 2>&1 \
  && fail "optIn rendered with empty optOutLabel (nothing to opt in with)"
render --set sensor.obi.discovery.mode=optIn --set-json 'sensor.obi.discovery.namespaces=[]' >/dev/null 2>&1 \
  && fail "optIn rendered with empty discovery.namespaces (selector would never render)"
render --set sensor.obi.discovery.mode=bogus >/dev/null 2>&1 \
  && fail "schema accepted an invalid discovery.mode"
# The guards police the OBI ConfigMap only — values that render nothing must
# never fail an install (e.g. optIn kept in shared values, sensor off here).
render --set sensor.obi.enabled=false --set sensor.obi.discovery.mode=optIn --set sensor.collection.optOutLabel="" >/dev/null 2>&1 \
  || fail "optIn guard fired with the sensor disabled (guard polices unrendered config)"
ok "optIn without label/namespaces fails; bogus mode fails schema; guards stay scoped to the rendered sensor"

echo "== default render: agent pipelines honor collection guardrails"
out="$(render)"
grep -q '/var/log/pods/kube-system_\*' <<<"$out" || fail "kube-system filelog exclude glob missing"
ok "filelog excludes shared excludeNamespaces"
grep -q 'filter/collection:' <<<"$out" || fail "filter/collection processor missing"
grep -q 'resource.attributes\["k8s.namespace.name"\] == "kube-system"' <<<"$out" || fail "namespace filter condition missing"
grep -q 'resource.attributes\["avuru.obs.collect"\] == "false"' <<<"$out" || fail "opt-out filter condition missing"
grep -q 'tag_name: avuru.obs.collect' <<<"$out" || fail "k8sattributes opt-out label extraction missing"
grep -Eq 'processors: \[memory_limiter, k8sattributes, filter/collection, transform/service_name, batch\]' <<<"$out" || fail "logs pipeline missing filter/collection"
grep -Eq 'processors: \[memory_limiter, k8sattributes, filter/collection, batch\]' <<<"$out" || fail "metrics pipeline missing filter/collection"
ok "filter/collection wired into logs + metrics pipelines"

echo "== empty guardrails -> no filter processor"
out="$(render --set-json 'sensor.collection.excludeNamespaces=[]' --set sensor.collection.optOutLabel="")"
grep -q 'filter/collection' <<<"$out" && fail "filter/collection rendered with no guardrails"
grep -q 'tag_name: avuru.obs.collect' <<<"$out" && fail "label extraction rendered with empty optOutLabel"
ok "guardrail plumbing disappears when unset"

echo "== project tagging (gateway.tenant + projects)"
out="$(render)"
grep -q 'resource/tenant' <<<"$out" && fail "resource/tenant rendered without gateway.tenant"
grep -q 'AVURUOPS_PROJECTS' <<<"$out" && fail "AVURUOPS_PROJECTS rendered without projects"
ok "default render carries no tenant plumbing"
out="$(render --set gateway.tenant=staging --set 'projects={default,staging}' --set sensor.profiler.enabled=true)"
grep -q 'value: "staging"' <<<"$out" || fail "resource/tenant value missing"
grep -q 'action: upsert' <<<"$out" || fail "resource/tenant action missing"
n=$(grep -c 'processors: \[resource/tenant, batch\]' <<<"$out")
[ "$n" = "3" ] || fail "resource/tenant not in all 3 gateway pipelines (got $n)"
grep -q 'X-Avuru-Tenant: "staging"' <<<"$out" || fail "profiler ingest header missing"
grep -q 'value: "default,staging"' <<<"$out" || fail "AVURUOPS_PROJECTS env missing"
ok "tenant stamped in gateway pipelines, profiler header, hub env"

echo "== all sensor containers off -> no DaemonSet"
out="$(render --set sensor.obi.enabled=false --set sensor.agent.enabled=false --set sensor.profiler.enabled=false)"
grep -q 'kind: DaemonSet' <<<"$out" && fail "DaemonSet rendered with zero active containers"
ok "DaemonSet omitted when nothing collects"

echo "== sentry ingest: off by default"
out="$(render --set ingress.enabled=true)"
grep -q '4319' <<<"$out" && fail "sentry port rendered without gateway.sentry.enabled"
grep -q 'sentry' <<<"$out" && fail "sentry surface rendered without gateway.sentry.enabled"
ok "no sentry receiver, port or route by default"

echo "== sentry ingest: enabled -> receiver + container port + Service port"
out="$(render --set gateway.sentry.enabled=true)"
grep -q 'endpoint: 0.0.0.0:4319' <<<"$out" || fail "sentry receiver endpoint missing"
grep -q 'receivers: \[otlp, sentry\]' <<<"$out" || fail "sentry not wired into the logs pipeline"
grep -q 'containerPort: 4319' <<<"$out" || fail "sentry containerPort missing"
# The Service port is the bit that was missing: without it the receiver is
# unreachable and the DSN promise cannot be kept.
n=$(grep -c 'targetPort: sentry-http' <<<"$out")
[ "$n" = "1" ] || fail "sentry Service port missing (got $n)"
ok "receiver, containerPort and Service port all rendered"

echo "== sentry ingest: needs its modules (a flag alone is not enough)"
for off in modules.logs.enabled modules.errorTracking.enabled; do
  out="$(render --set gateway.sentry.enabled=true --set "$off=false" --set ingress.enabled=true --set ingress.sentryHost=errors.example.com)"
  grep -q '4319' <<<"$out" && fail "sentry surface survived $off=false"
done
ok "surface disappears when either logs or error-tracking is off"

echo "== sentry ingest: ingress route only with sentryHost"
out="$(render --set gateway.sentry.enabled=true --set ingress.enabled=true)"
grep -q 'number: 4319' <<<"$out" && fail "ingress routed to 4319 without ingress.sentryHost"
ok "no ingress route until sentryHost is set"
out="$(render --set gateway.sentry.enabled=true --set ingress.enabled=true --set ingress.sentryHost=errors.example.com)"
grep -q 'host: "errors.example.com"' <<<"$out" || fail "sentry host rule missing"
grep -q 'number: 4319' <<<"$out" || fail "sentry ingress backend port missing"
# /api on the MAIN host must still reach the hub — the sentry rule is a second
# host precisely because /api/<project>/envelope/ would collide with it.
grep -q 'name: test-avuruops-hub' <<<"$out" || fail "hub backend lost from the main host"
ok "dedicated sentry host routes to the gateway, hub keeps /api"

echo "== service-health: on by default -> module, ConfigMap, env, mount"
out="$(render)"
grep -qE 'value: "core,logs,infra-metrics,profiling,error-tracking,service-health(,|")' <<<"$out" || fail "service-health missing from AVURUOPS_MODULES"
grep -q 'name: test-avuruops-groups' <<<"$out" || fail "service-health groups ConfigMap missing"
grep -q 'groups.json:' <<<"$out" || fail "groups.json key missing from ConfigMap"
grep -q 'name: AVURUOPS_GROUPS_CONFIG' <<<"$out" || fail "AVURUOPS_GROUPS_CONFIG env missing"
grep -q 'mountPath: /etc/avuruops' <<<"$out" || fail "groups ConfigMap not mounted"
ok "module, ConfigMap, env and mount all rendered"

echo "== service-health: disabled -> whole surface disappears"
out="$(render --set modules.serviceHealth.enabled=false)"
grep -q 'service-health' <<<"$out" && fail "service-health surface survived module off"
grep -q 'AVURUOPS_GROUPS_CONFIG' <<<"$out" && fail "groups env survived module off"
ok "no module entry, ConfigMap, env or mount when disabled"

echo "== alerting: on by default -> module, ConfigMap, env, mount"
out="$(render)"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting' <<<"$out" || fail "alerting missing from AVURUOPS_MODULES"
grep -q 'name: test-avuruops-alerts' <<<"$out" || fail "alerting ConfigMap missing"
grep -q 'alerts.json:' <<<"$out" || fail "alerts.json key missing from ConfigMap"
grep -q 'name: AVURUOPS_ALERTS_CONFIG' <<<"$out" || fail "AVURUOPS_ALERTS_CONFIG env missing"
grep -q 'mountPath: /etc/avuruops-alerts' <<<"$out" || fail "alerts ConfigMap not mounted"
# webhookAllow is an env knob, never in the parsed config file.
grep -q 'webhookAllow' <<<"$(grep alerts.json: <<<"$out")" && fail "webhookAllow leaked into alerts.json"
ok "module, ConfigMap, env and mount all rendered; webhookAllow excluded"

echo "== alerting: webhookAllow renders the SSRF override env"
out="$(render --set 'alerting.webhookAllow[0]=10.0.0.0/8')"
grep -q 'name: AVURUOPS_WEBHOOK_ALLOW' <<<"$out" || fail "AVURUOPS_WEBHOOK_ALLOW env missing when set"
ok "webhookAllow -> AVURUOPS_WEBHOOK_ALLOW"

echo "== alerting: disabled -> whole surface disappears"
out="$(render --set modules.alerting.enabled=false)"
grep -q ',alerting' <<<"$out" && fail "alerting survived in the module CSV"
grep -q 'AVURUOPS_ALERTS_CONFIG' <<<"$out" && fail "alerts env survived module off"
grep -q 'test-avuruops-alerts' <<<"$out" && fail "alerts ConfigMap survived module off"
ok "no module entry, ConfigMap, env or mount when disabled"

echo "== OBI network stats: off by default"
out="$(render)"
grep -q 'obi_stat_tcp_rtt' <<<"$out" && fail "OBI stats config rendered without sensor.obi.network.enabled"
ok "no TCP-stats config by default"

echo "== OBI network stats: enabled with the network feature (+ infra-metrics)"
out="$(render --set sensor.obi.network.enabled=true)"
grep -q 'stats:' <<<"$out" || fail "OBI stats feature not enabled with network on"
grep -q 'obi_stat_tcp_rtt' <<<"$out" || fail "obi_stat_tcp_rtt attribute selection missing"
grep -q 'obi_stat_tcp_failed_connections' <<<"$out" || fail "obi_stat_tcp_failed_connections attribute selection missing"
ok "stats feature + k8s-owner attribute selection render"

echo "== OBI network stats: can be turned off while keeping flow bytes"
out="$(render --set sensor.obi.network.enabled=true --set sensor.obi.network.stats=false)"
grep -q 'obi_stat_tcp_rtt' <<<"$out" && fail "stats config survived network.stats=false"
grep -qE 'network:' <<<"$out" || fail "network flow config missing"
ok "flow bytes without TCP stats"

echo "== green: born opt-off -> no energy surface at all"
out="$(render)"
grep -q 'name: kepler' <<<"$out" && fail "kepler container rendered without opt-in"
grep -q 'prometheus/green' <<<"$out" && fail "green receiver rendered without opt-in"
grep -q 'metrics/green' <<<"$out" && fail "green pipeline rendered without opt-in"
grep -q 'AVURUOPS_GREEN_CONFIG' <<<"$out" && fail "green env rendered without opt-in"
grep -q 'green.json' <<<"$out" && fail "green ConfigMap rendered without opt-in"
grep -A1 'name: AVURUOPS_MODULES' <<<"$out" | grep -qE ',green[,"]' && fail "green in AVURUOPS_MODULES without opt-in"
ok "no container, receiver, pipeline, ConfigMap, env or module entry by default"

echo "== green: module + sensor on -> full surface, and NO probes on Kepler"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true)"
grep -q 'name: kepler' <<<"$out" || fail "kepler container missing"
grep -q 'prometheus/green:' <<<"$out" || fail "prometheus/green receiver missing"
grep -q 'metrics/green:' <<<"$out" || fail "metrics/green pipeline missing"
grep -q 'kepler_(node|pod)_cpu_(watts|joules_total)' <<<"$out" || fail "keep-regex missing or diverged from the AEP metric table"
grep -q 'name: test-avuruops-green' <<<"$out" || fail "green (hub factors/budgets) ConfigMap missing"
grep -q 'name: test-avuruops-sensor-kepler' <<<"$out" || fail "kepler config ConfigMap missing"
grep -q 'AVURUOPS_GREEN_CONFIG' <<<"$out" || fail "AVURUOPS_GREEN_CONFIG env missing"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting,green' <<<"$out" || fail "green missing from AVURUOPS_MODULES"
grep -q '127.0.0.1:28282' <<<"$out" || fail "Kepler loopback bind/scrape target missing"
# Prometheus scrape-report series (up, scrape_*) bypass metric_relabel_configs;
# the pipeline must drop them or they pollute the shared otel_metrics_gauge.
grep -qF 'IsMatch(name, "^(up|scrape_.+)$")' <<<"$out" || fail "scrape-meta drop condition (filter/green) missing"
grep -qE 'processors: \[memory_limiter, filter/green, transform/green, groupbyattrs/green, resource/green, k8sattributes, filter/collection, batch\]' <<<"$out" \
  || fail "filter/green not wired into the metrics/green pipeline"
# The "do no harm" clause: a RAPL-less Kepler must never flap the sensor pod,
# so the container must carry NO liveness/readiness/startup probe.
kepler_block="$(sed -n '/- name: kepler$/,/resources:/p' <<<"$out")"
[ -n "$kepler_block" ] || fail "could not isolate the kepler container block"
grep -qE 'livenessProbe|readinessProbe|startupProbe' <<<"$kepler_block" \
  && fail "Kepler container has a probe — a RAPL-less node would flap the sensor pod (do-no-harm gate)"
ok "container + receiver + pipeline + ConfigMaps + env + module entry; Kepler carries no probes"

echo "== green: guards"
render --set modules.green.enabled=true --set modules.infraMetrics.enabled=false >/dev/null 2>&1 \
  && fail "green module rendered without infra-metrics (the pod→workload join source)"
render --set modules.green.enabled=true --set sensor.green.enabled=true --set sensor.agent.enabled=false >/dev/null 2>&1 \
  && fail "green collection rendered without the otel-agent (nothing would scrape Kepler)"
# Values that render nothing must never fail an install (obi/profiler precedent:
# the sensor flag without its module no-ops instead of failing).
render --set sensor.green.enabled=true >/dev/null 2>&1 \
  || fail "sensor.green without modules.green failed instead of no-opping"
out="$(render --set sensor.green.enabled=true)"
grep -q 'name: kepler' <<<"$out" && fail "kepler rendered with the green module off"
ok "module-without-infra-metrics and collect-without-agent fail; sensor-without-module no-ops"

echo "== green: module on without the sensor -> hub config only"
out="$(render --set modules.green.enabled=true)"
grep -q 'AVURUOPS_GREEN_CONFIG' <<<"$out" || fail "green env missing with module on"
grep -q 'name: test-avuruops-green' <<<"$out" || fail "green ConfigMap missing with module on"
grep -q 'name: kepler' <<<"$out" && fail "kepler rendered without sensor.green.enabled"
ok "hub reads factors/budgets; no collection until sensor.green opts in"

echo "== green: privileged=false renders the capability path"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true --set sensor.green.privileged=false)"
kepler_block="$(sed -n '/- name: kepler$/,/resources:/p' <<<"$out")"
[ -n "$kepler_block" ] || fail "could not isolate the kepler container block"
grep -q 'privileged: true' <<<"$kepler_block" && fail "privileged rendered with sensor.green.privileged=false"
for cap in DAC_READ_SEARCH SYS_PTRACE PERFMON; do
  grep -q "$cap" <<<"$kepler_block" || fail "capability $cap missing from the non-privileged path"
done
grep -qE '^ +- ALL' <<<"$kepler_block" || fail "drop: ALL missing from the non-privileged path"
ok "cap set DAC_READ_SEARCH/SYS_PTRACE/PERFMON with drop ALL (the verify-on-hardware path)"

echo "== green: fake-cpu-meter is opt-in for CI only"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true)"
grep -q 'fake-cpu-meter' <<<"$out" && fail "fake-cpu-meter rendered without opt-in"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true --set sensor.green.fakeCpuMeter=true)"
grep -q 'fake-cpu-meter' <<<"$out" || fail "fake-cpu-meter missing when set"
ok "fake energy only when explicitly requested"

echo "== green: existing agent pipelines unchanged by the green opt-in"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true)"
# metrics/green is deliberately its OWN pipeline: turning green on must leave
# the kubeletstats metrics pipeline and the logs pipeline byte-identical.
grep -Eq 'processors: \[memory_limiter, k8sattributes, filter/collection, batch\]' <<<"$out" \
  || fail "kubeletstats metrics pipeline changed by the green opt-in"
grep -Eq 'processors: \[memory_limiter, k8sattributes, filter/collection, transform/service_name, batch\]' <<<"$out" \
  || fail "logs pipeline changed by the green opt-in"
grep -q 'receivers: \[kubeletstats\]' <<<"$out" || fail "kubeletstats receiver lost with green on"
ok "kubeletstats + logs pipelines identical with green on"

echo "== green: coexists with obi.network (hostNetwork) — both surfaces render"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true --set sensor.obi.network.enabled=true)"
grep -q 'hostNetwork: true' <<<"$out" || fail "hostNetwork missing with obi.network on"
grep -q 'name: kepler' <<<"$out" || fail "kepler container lost next to obi.network"
grep -q 'prometheus/green:' <<<"$out" || fail "green receiver lost next to obi.network"
grep -q 'metrics/green:' <<<"$out" || fail "green pipeline lost next to obi.network"
grep -q 'obi_stat_tcp_rtt' <<<"$out" || fail "OBI TCP-stats config lost next to green"
# Under hostNetwork the Kepler bind hits the HOST loopback (values.yaml
# caveat) — it must still be 127.0.0.1, never a pod/host-wide address.
grep -q '127.0.0.1:28282' <<<"$out" || fail "Kepler loopback bind lost under hostNetwork"
grep -q '0.0.0.0:28282' <<<"$out" && fail "Kepler bound beyond loopback under hostNetwork"
ok "green + obi.network render together; Kepler stays loopback-bound"

echo "ALL TEMPLATE ASSERTIONS PASSED"
