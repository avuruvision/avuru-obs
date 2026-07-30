#!/usr/bin/env bash
# Render-time assertions for the avuruobs chart — no cluster needed.
# Run via `make helm-check` or directly: deploy/helm/template-test.sh
set -euo pipefail
cd "$(dirname "$0")"
CHART=avuruobs

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
grep -q 'priorityClassName: test-avuruobs-sensor' <<<"$out" || fail "priorityClassName not wired to DaemonSet"
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
grep -q 'AVURUOBS_PROJECTS' <<<"$out" && fail "AVURUOBS_PROJECTS rendered without projects"
ok "default render carries no tenant plumbing"
out="$(render --set gateway.tenant=staging --set 'projects={default,staging}' --set sensor.profiler.enabled=true)"
grep -q 'value: "staging"' <<<"$out" || fail "resource/tenant value missing"
grep -q 'action: upsert' <<<"$out" || fail "resource/tenant action missing"
n=$(grep -c 'processors: \[resource/tenant, batch\]' <<<"$out")
[ "$n" = "3" ] || fail "resource/tenant not in all 3 gateway pipelines (got $n)"
grep -q 'X-Avuru-Tenant: "staging"' <<<"$out" || fail "profiler ingest header missing"
grep -q 'value: "default,staging"' <<<"$out" || fail "AVURUOBS_PROJECTS env missing"
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
grep -q 'name: test-avuruobs-hub' <<<"$out" || fail "hub backend lost from the main host"
ok "dedicated sentry host routes to the gateway, hub keeps /api"

echo "== service-health: on by default -> module, ConfigMap, env, mount"
out="$(render)"
grep -qE 'value: "core,logs,infra-metrics,profiling,error-tracking,service-health(,|")' <<<"$out" || fail "service-health missing from AVURUOBS_MODULES"
grep -q 'name: test-avuruobs-groups' <<<"$out" || fail "service-health groups ConfigMap missing"
grep -q 'groups.json:' <<<"$out" || fail "groups.json key missing from ConfigMap"
grep -q 'name: AVURUOBS_GROUPS_CONFIG' <<<"$out" || fail "AVURUOBS_GROUPS_CONFIG env missing"
grep -q 'mountPath: /etc/avuruobs' <<<"$out" || fail "groups ConfigMap not mounted"
ok "module, ConfigMap, env and mount all rendered"

echo "== service-health: disabled -> whole surface disappears"
out="$(render --set modules.serviceHealth.enabled=false)"
grep -q 'service-health' <<<"$out" && fail "service-health surface survived module off"
grep -q 'AVURUOBS_GROUPS_CONFIG' <<<"$out" && fail "groups env survived module off"
ok "no module entry, ConfigMap, env or mount when disabled"

echo "== alerting: on by default -> module, ConfigMap, env, mount"
out="$(render)"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting' <<<"$out" || fail "alerting missing from AVURUOBS_MODULES"
grep -q 'name: test-avuruobs-alerts' <<<"$out" || fail "alerting ConfigMap missing"
grep -q 'alerts.json:' <<<"$out" || fail "alerts.json key missing from ConfigMap"
grep -q 'name: AVURUOBS_ALERTS_CONFIG' <<<"$out" || fail "AVURUOBS_ALERTS_CONFIG env missing"
grep -q 'mountPath: /etc/avuruobs-alerts' <<<"$out" || fail "alerts ConfigMap not mounted"
# webhookAllow is an env knob, never in the parsed config file.
grep -q 'webhookAllow' <<<"$(grep alerts.json: <<<"$out")" && fail "webhookAllow leaked into alerts.json"
ok "module, ConfigMap, env and mount all rendered; webhookAllow excluded"

echo "== alerting: webhookAllow renders the SSRF override env"
out="$(render --set 'alerting.webhookAllow[0]=10.0.0.0/8')"
grep -q 'name: AVURUOBS_WEBHOOK_ALLOW' <<<"$out" || fail "AVURUOBS_WEBHOOK_ALLOW env missing when set"
ok "webhookAllow -> AVURUOBS_WEBHOOK_ALLOW"

echo "== alerting: disabled -> whole surface disappears"
out="$(render --set modules.alerting.enabled=false)"
grep -q ',alerting' <<<"$out" && fail "alerting survived in the module CSV"
grep -q 'AVURUOBS_ALERTS_CONFIG' <<<"$out" && fail "alerts env survived module off"
grep -q 'test-avuruobs-alerts' <<<"$out" && fail "alerts ConfigMap survived module off"
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
grep -q 'AVURUOBS_GREEN_CONFIG' <<<"$out" && fail "green env rendered without opt-in"
grep -q 'green.json' <<<"$out" && fail "green ConfigMap rendered without opt-in"
grep -A1 'name: AVURUOBS_MODULES' <<<"$out" | grep -qE ',green[,"]' && fail "green in AVURUOBS_MODULES without opt-in"
ok "no container, receiver, pipeline, ConfigMap, env or module entry by default"

echo "== green: module + sensor on -> full surface, and NO probes on Kepler"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true)"
grep -q 'name: kepler' <<<"$out" || fail "kepler container missing"
grep -q 'prometheus/green:' <<<"$out" || fail "prometheus/green receiver missing"
grep -q 'metrics/green:' <<<"$out" || fail "metrics/green pipeline missing"
grep -q 'kepler_(node|pod)_cpu_(watts|joules_total)' <<<"$out" || fail "keep-regex missing or diverged from the AEP metric table"
grep -q 'name: test-avuruobs-green' <<<"$out" || fail "green (hub factors/budgets) ConfigMap missing"
grep -q 'name: test-avuruobs-sensor-kepler' <<<"$out" || fail "kepler config ConfigMap missing"
grep -q 'AVURUOBS_GREEN_CONFIG' <<<"$out" || fail "AVURUOBS_GREEN_CONFIG env missing"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting,green' <<<"$out" || fail "green missing from AVURUOBS_MODULES"
grep -q '127.0.0.1:28282' <<<"$out" || fail "Kepler loopback bind/scrape target missing"
# Prometheus scrape-report series (up, scrape_*) bypass metric_relabel_configs;
# the pipeline must drop them or they pollute the shared otel_metrics_gauge.
grep -qF 'IsMatch(name, "^(up|scrape_.+)$")' <<<"$out" || fail "scrape-meta drop condition (filter/green) missing"
grep -qE 'processors: \[memory_limiter, filter/green, transform/green, transform/green_quality, groupbyattrs/green, resource/green, k8sattributes, filter/collection, batch\]' <<<"$out" \
  || fail "filter/green not wired into the metrics/green pipeline"
grep -qF 'set(attributes["avuruobs_quality"], "measured") where resource.attributes["service.name"] == "kepler"' <<<"$out" \
  || fail "measured quality stamp missing from transform/green_quality"
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
grep -q 'AVURUOBS_GREEN_CONFIG' <<<"$out" || fail "green env missing with module on"
grep -q 'name: test-avuruobs-green' <<<"$out" || fail "green ConfigMap missing with module on"
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

echo "== green tdp-estimator: off by default even with green enabled"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true)"
grep -q 'name: tdp-estimator' <<<"$out" && fail "tdp-estimator container rendered without opt-in"
grep -q 'job_name: tdp-estimator' <<<"$out" && fail "tdp-estimator scrape job rendered without opt-in"
grep -q '"estimated") where resource.attributes\["service.name"\] == "tdp-estimator"' <<<"$out" \
  && fail "estimated quality statement rendered without opt-in"
ok "estimator stays off when only sensor.green.enabled is set"

echo "== green tdp-estimator: guard requires sensor.green.enabled"
render --set sensor.green.estimation.enabled=true --set sensor.green.enabled=false >/dev/null 2>&1 \
  && fail "estimation.enabled rendered without sensor.green.enabled"
ok "estimation.enabled without sensor.green.enabled fails at template time"

echo "== green tdp-estimator: opt-in -> container, scrape job, quality stamp, no probes"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true \
  --set sensor.green.estimation.enabled=true \
  --set sensor.green.estimation.image.repository=example/tdp-estimator \
  --set sensor.green.estimation.image.tag=v0.3.0)"
grep -q 'name: tdp-estimator' <<<"$out" || fail "tdp-estimator container missing"
grep -q 'image: example/tdp-estimator:v0.3.0' <<<"$out" || fail "tdp-estimator image not wired from values"
grep -q 'job_name: tdp-estimator' <<<"$out" || fail "tdp-estimator scrape job missing"
grep -q '127.0.0.1:28283' <<<"$out" || fail "tdp-estimator loopback scrape target missing"
grep -qF 'set(attributes["avuruobs_quality"], "estimated") where resource.attributes["service.name"] == "tdp-estimator"' <<<"$out" \
  || fail "estimated quality statement missing when estimation is enabled"
estimator_block="$(sed -n '/- name: tdp-estimator$/,/resources:/p' <<<"$out")"
[ -n "$estimator_block" ] || fail "could not isolate the tdp-estimator container block"
grep -qE 'livenessProbe|readinessProbe|startupProbe' <<<"$estimator_block" \
  && fail "tdp-estimator container has a probe — a RAPL-less node must never flap the sensor pod (do-no-harm gate)"
grep -q 'mountPath: /sys' <<<"$estimator_block" || fail "tdp-estimator missing /sys mount (RAPL probe + cgroup walk)"
grep -q 'mountPath: /proc' <<<"$estimator_block" || fail "tdp-estimator missing /proc mount (utilization sampling)"
ok "container + scrape job + quality stamp render on opt-in; no probes; host mounts present"

echo "== auth oidc: off by default -> no SSO surface"
out="$(render)"
grep -q 'AVURUOBS_AUTH_OIDC' <<<"$out" && fail "OIDC env rendered without auth.oidc.enabled"
grep -q 'AVURUOBS_PUBLIC_URL' <<<"$out" && fail "AVURUOBS_PUBLIC_URL rendered without auth.oidc.enabled"
grep -q 'test-avuruobs-oidc' <<<"$out" && fail "oidc ConfigMap/Secret rendered without opt-in"
grep -q 'oidc.yaml' <<<"$out" && fail "oidc config file rendered without opt-in"
ok "no env, ConfigMap, Secret, volume or mount by default"

oidc_on=(--set auth.oidc.enabled=true
  --set auth.oidc.issuer=https://idp.example.com/realms/avuru
  --set auth.oidc.clientId=avuru-obs)

echo "== auth oidc: enabled -> ConfigMap + Secret + env + mount"
out="$(render "${oidc_on[@]}" --set auth.oidc.clientSecret=s3cret \
  --set auth.oidc.publicUrl=https://obs.example.com \
  --set-json 'auth.oidc.mapping=[{"group":"obs-admins","role":"admin","projects":["*"]}]')"
grep -q 'name: test-avuruobs-oidc' <<<"$out" || fail "oidc ConfigMap missing"
grep -q 'oidc.yaml: |' <<<"$out" || fail "oidc.yaml key missing from ConfigMap"
grep -q 'issuer: https://idp.example.com/realms/avuru' <<<"$out" || fail "issuer missing from rendered config"
grep -q 'group: obs-admins' <<<"$out" || fail "mapping rule missing from rendered config"
# Chart-only knobs must never leak into the file the hub parses.
cfg_block="$(sed -n '/oidc.yaml: |/,/^[^ ]/p' <<<"$out")"
[ -n "$cfg_block" ] || fail "could not isolate the oidc.yaml block"
grep -qE 'clientSecret|existingSecret|publicUrl|enabled' <<<"$cfg_block" \
  && fail "chart-only knob leaked into oidc.yaml"
grep -q 'value: /etc/avuruobs-oidc/oidc.yaml' <<<"$out" || fail "AVURUOBS_AUTH_OIDC_CONFIG env missing"
grep -q 'name: AVURUOBS_AUTH_OIDC_CLIENT_SECRET' <<<"$out" || fail "client-secret env missing"
grep -q 'key: oidc-client-secret' <<<"$out" || fail "secretKeyRef key wrong"
grep -q 'oidc-client-secret: "' <<<"$out" || fail "chart-created oidc Secret missing"
grep -q 'value: "https://obs.example.com"' <<<"$out" || fail "AVURUOBS_PUBLIC_URL env missing"
grep -q 'mountPath: /etc/avuruobs-oidc' <<<"$out" || fail "oidc ConfigMap not mounted"
ok "ConfigMap (secret-free), Secret, env, volume and mount all rendered"

echo "== auth oidc: existingSecret wins and suppresses the chart Secret"
out="$(render "${oidc_on[@]}" --set auth.oidc.clientSecret=s3cret --set auth.oidc.existingSecret=my-oidc)"
grep -q 'name: my-oidc' <<<"$out" || fail "secretKeyRef not pointed at existingSecret"
grep -q 'oidc-client-secret: "' <<<"$out" && fail "chart Secret rendered despite existingSecret"
ok "existingSecret referenced; no chart-created Secret"

echo "== auth oidc: no secret source -> secret env omitted (public client), no dangling ref"
out="$(render "${oidc_on[@]}")"
grep -q 'AVURUOBS_AUTH_OIDC_CONFIG' <<<"$out" || fail "oidc config env lost without a secret source"
grep -q 'oidc-client-secret' <<<"$out" && fail "secret surface rendered with no secret source"
grep -q 'name: AVURUOBS_AUTH_OIDC_CLIENT_SECRET' <<<"$out" && fail "secret env references a Secret that does not exist"
ok "config still renders; nothing points at a nonexistent Secret"

echo "== auth oidc: guards"
render --set auth.oidc.enabled=true >/dev/null 2>&1 \
  && fail "oidc rendered without issuer/clientId (hub would crash-loop fail-loud)"
render "${oidc_on[@]}" --set auth.enabled=false >/dev/null 2>&1 \
  && fail "oidc rendered with auth disabled (hub would silently ignore SSO)"
ok "missing issuer/clientId fails; SSO-with-auth-off fails"

echo "== ingest keys: default (log) keeps the drop-in pipeline unchanged"
out="$(render)"
grep -q 'avuruingestauth:' <<<"$out" || fail "authenticator missing in default log mode"
grep -q 'authenticator: avuruingestauth' <<<"$out" || fail "otlp receiver not wired to the authenticator"
# The whole point of the log default: the pipeline must look exactly like a
# pre-ingest-keys install, so an upgrade cannot change what lands.
grep -q 'tenantfromauth' <<<"$out" && fail "tenantfromauth wired in log mode (pipeline is no longer byte-identical)"
grep -q 'mode: "log"' <<<"$out" || fail "log mode not rendered into the extension"
ok "log mode: authenticator on, no tenant stamping, pipeline unchanged"

echo "== ingest keys: off renders no ingest surface at all"
out="$(render --set auth.ingest.mode=off)"
grep -q 'avuruingestauth' <<<"$out" && fail "authenticator rendered with mode=off"
grep -q 'tenantfromauth' <<<"$out" && fail "tenantfromauth rendered with mode=off"
grep -q 'internal-token' <<<"$out" && fail "ingest Secret rendered with mode=off"
grep -q 'AVURUOBS_INGEST_INTERNAL_TOKEN' <<<"$out" && fail "hub/gateway ingest env rendered with mode=off"
ok "mode=off leaves no extension, processor, Secret or env behind"

echo "== ingest keys: enforce stamps the tenant LAST so the key wins"
out="$(render --set auth.ingest.mode=enforce --set gateway.tenant=staging)"
# Ordering is the correctness property: resource/tenant upserts the static
# tenant, so stamping before it would let the static value silently win.
n=$(grep -c 'processors: \[resource/tenant, tenantfromauth, batch\]' <<<"$out")
[ "$n" = "3" ] || fail "tenantfromauth not last in all 3 gateway pipelines (got $n)"
grep -q 'mode: "enforce"' <<<"$out" || fail "enforce mode not rendered into the extension"
ok "enforce: key project overrides the static tenant in all 3 pipelines"

echo "== ingest keys: the sensor can still send under enforce"
grep -q 'Authorization: "Bearer ${env:AVURUOBS_INGEST_KEY}"' <<<"$out" \
  || fail "sensor agent exporter has no ingest key header (enforce would silence it)"
grep -q 'value: "Authorization=Bearer $(AVURUOBS_INGEST_KEY)"' <<<"$out" \
  || fail "OBI container has no ingest key header (enforce would silence it)"
grep -q 'AVURUOBS_INGEST_SEED_KEYS' <<<"$out" \
  || fail "hub gets no seed keys — the sensor key would never exist in auth_ingest_key"
ok "sensor key provisioned for both sensor containers and seeded into the hub"

echo "== ingest keys: secret material never lands in a ConfigMap"
# Decode what the chart generated and prove those exact bytes appear nowhere
# outside the Secret. A ${env:...} placeholder in the ConfigMap is the point.
leakcheck="$(mktemp)"
cat > "$leakcheck" <<'PYEOF'
# Stdlib only — no PyYAML. This runs on a bare CI runner, and a missing import
# would fail the job for a reason that has nothing to do with the chart.
# Substring containment over the raw document text is the right test anyway:
# the question is "do these bytes appear anywhere they shouldn't", not "does
# this parse".
import sys, base64, re

text = sys.stdin.read()
docs = [d for d in re.split(r"(?m)^---\s*$", text) if d.strip()]

def kind_of(doc):
    m = re.search(r"(?m)^kind:\s*(\S+)", doc)
    return m.group(1) if m else ""

def meta_name(doc):
    m = re.search(r"(?ms)^metadata:\s*\n(.*?)(?=^\S|\Z)", doc)
    n = re.search(r"(?m)^\s+name:\s*(\S+)", m.group(1)) if m else None
    return n.group(1) if n else ""

secret = next((d for d in docs
               if kind_of(d) == "Secret" and meta_name(d).endswith("-ingest")), None)
assert secret, "no ingest Secret rendered"

for label in ("internal-token", "sensor-key"):
    m = re.search(r'(?m)^\s+%s:\s*"?([A-Za-z0-9+/=]+)"?\s*$' % re.escape(label), secret)
    assert m, f"{label} missing from the ingest Secret"
    raw = base64.b64decode(m.group(1)).decode()
    b64 = m.group(1)
    for d in docs:
        if (raw in d or b64 in d) and kind_of(d) != "Secret":
            raise AssertionError(
                f"{label} leaked into {kind_of(d) or 'an unknown document'}/{meta_name(d)}")
PYEOF
printf '%s' "$out" | python3 "$leakcheck" || fail "ingest secret material leaked outside a Secret"
rm -f "$leakcheck"
ok "internal token and sensor key appear only in Secret objects"

echo "== collection runtime control: off by default -> no RBAC, but the env still renders"
out="$(render)"
grep -q 'collection-control' <<<"$out" && fail "collection-control RBAC rendered without the opt-in"
grep -q 'collection-base-values' <<<"$out" && fail "base-values ConfigMap rendered without the opt-in"
# Scoped to the hub Deployment on purpose: the sensor DaemonSet always carries
# its own serviceAccountName, so a whole-render grep would never be meaningful.
hub="$(render -s templates/hub-deploy.yaml)"
grep -q 'serviceAccountName' <<<"$hub" && fail "hub pod bound to a ServiceAccount without the opt-in"
# The env is NOT conditional. The hub reads it on every start and the Go side
# defaults the routes off — a missing env would be silent drift between chart
# and binary, not a safe default.
grep -A1 'name: AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED' <<<"$hub" | grep -q 'value: "false"' \
  || fail "AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED missing or not \"false\" in the default render"
ok "no ServiceAccount/Role/RoleBinding/ConfigMap; env present and \"false\""

echo "== collection runtime control: the env is emitted with auth off too"
# Regression guard: the env sits next to AVURUOBS_AUTH_ENABLED, immediately
# before the `if .Values.auth.enabled` block. Slipping inside that block would
# make the collection API silently unreachable on an auth-disabled install.
hub="$(render -s templates/hub-deploy.yaml --set auth.enabled=false)"
grep -A1 'name: AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED' <<<"$hub" | grep -q 'value: "false"' \
  || fail "collection env lost with auth.enabled=false (it landed inside the auth conditional)"
hub="$(render -s templates/hub-deploy.yaml --set auth.enabled=false --set collection.runtimeControl.enabled=true)"
grep -A1 'name: AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED' <<<"$hub" | grep -q 'value: "true"' \
  || fail "collection env lost with auth.enabled=false and the flag on"
ok "env independent of auth.enabled, both off and on"

echo "== collection runtime control: enabled -> SA + Role + RoleBinding + base-values ConfigMap"
out="$(render --set collection.runtimeControl.enabled=true)"
for kv in "ServiceAccount:test-avuruobs-collection-control" \
          "ConfigMap:test-avuruobs-collection-base-values" \
          "Role:test-avuruobs-collection-control" \
          "RoleBinding:test-avuruobs-collection-control"; do
  grep -q "name: ${kv#*:}" <<<"$out" || fail "${kv%%:*} ${kv#*:} not rendered"
done
grep -q 'kind: RoleBinding' <<<"$out" || fail "RoleBinding kind missing"
# Least privilege is the whole point (AEP: nothing cluster-wide, nothing on
# another namespace's resources): the Role must be namespaced and name every
# object it may touch. Scoped to this one template — the sensor's own RBAC
# legitimately renders a ClusterRole, so a full-render grep proves nothing.
crbac="$(render --set collection.runtimeControl.enabled=true -s templates/collection-rbac.yaml)"
grep -q 'ClusterRole' <<<"$crbac" && fail "collection control granted a ClusterRole (must stay namespaced)"
role="$(sed -n '/^kind: Role$/,/^---/p' <<<"$crbac")"
[ -n "$role" ] || fail "could not isolate the collection-control Role"
for n in test-avuruobs-collection-base-values test-avuruobs-sensor-obi test-avuruobs-sensor-agent \
         test-avuruobs-sensor-profiler test-avuruobs-sensor-kepler test-avuruobs-sensor; do
  grep -qE "^ +- $n\$" <<<"$role" || fail "Role does not name $n in resourceNames"
done
grep -q 'resources: \["daemonsets"\]' <<<"$role" || fail "Role missing the daemonsets rule"
grep -q 'verbs: \["get", "patch"\]' <<<"$role" || fail "daemonsets verbs wider/narrower than get+patch"
grep -q 'verbs: \["get", "update", "patch"\]' <<<"$role" || fail "configmaps verbs wider/narrower than get+update+patch"
grep -q 'delete\|create\|"\*"' <<<"$role" && fail "collection-control Role grants create/delete/wildcard"
hub="$(render -s templates/hub-deploy.yaml --set collection.runtimeControl.enabled=true)"
grep -q 'serviceAccountName: test-avuruobs-collection-control' <<<"$hub" \
  || fail "hub Deployment not bound to the collection-control ServiceAccount"
grep -A1 'name: AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED' <<<"$hub" | grep -q 'value: "true"' \
  || fail "collection env not \"true\" with the flag on"
ok "4 objects render; namespaced Role names the 4 sensor ConfigMaps + the DaemonSet; hub bound + env \"true\""

echo "== collection runtime control: guard requires a sensor to control"
render --set collection.runtimeControl.enabled=true --set sensor.enabled=false >/dev/null 2>&1 \
  && fail "runtime control rendered with sensor.enabled=false (nothing to control)"
ok "flag without sensor.enabled fails at template time"

echo "== collection runtime control: base-values ConfigMap is valid JSON and secret-free"
out="$(render --set collection.runtimeControl.enabled=true)"
base_values="$(awk '/^  values\.json: \|$/{f=1;next} /^---$/{f=0} f' <<<"$out")"
[ -n "$base_values" ] || fail "could not isolate the base-values values.json block"
# The dict|toJson|nindent construct is fiddly — prove the key holds ONE valid
# JSON document, not a mis-indented fragment.
python3 -c 'import json,sys; d=json.load(sys.stdin); print("  ok: values.json parses ("+",".join(sorted(d))+")")' \
  <<<"$base_values" || fail "values.json is not a single valid JSON document"
# Curated, not wholesale: clickhouse.auth.password and auth.adminPassword are
# plaintext secrets by default, and Helm's own release storage is a Secret, not
# a ConfigMap. `"avuru"` is the default ClickHouse user AND password — as a
# complete JSON string it can only come from a leaked clickhouse subtree
# (a bare `avuru` substring is meaningless here, every label says avuruobs).
grep -q '"clickhouse"' <<<"$base_values" && fail "clickhouse subtree copied into the base-values ConfigMap"
grep -q '"avuru"' <<<"$base_values" && fail "default ClickHouse credential leaked into the base-values ConfigMap"
grep -qE '"(password|adminPassword|clientSecret|internalToken|existingSecret)"' <<<"$base_values" \
  && fail "a secret-bearing key leaked into the base-values ConfigMap"
# And with the secrets actually set to something greppable, so the assertion
# above cannot pass merely because the defaults are empty strings.
out="$(render --set collection.runtimeControl.enabled=true \
  --set clickhouse.auth.password=leaky-ch-password \
  --set auth.adminPassword=leaky-admin-password \
  --set auth.ingest.internalToken=leaky-internal-token)"
base_values="$(awk '/^  values\.json: \|$/{f=1;next} /^---$/{f=0} f' <<<"$out")"
for s in leaky-ch-password leaky-admin-password leaky-internal-token; do
  grep -q "$s" <<<"$base_values" && fail "$s leaked into the base-values ConfigMap"
done
ok "single valid JSON document; no clickhouse/admin/ingest secret material"

echo "ALL TEMPLATE ASSERTIONS PASSED"
