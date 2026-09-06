#!/usr/bin/env bash
# Render-time assertions for the avuruobs chart — no cluster needed.
# Run via `make helm-check` or directly: deploy/helm/template-test.sh
set -euo pipefail
cd "$(dirname "$0")"
CHART=avuruobs

fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "  ok: $*"; }
render() { helm template test "$CHART" "$@"; }

# The OBI config is a YAML document embedded in a ConfigMap literal block, so
# `helm template` happily renders one that OBI itself refuses to load. Pull the
# block out so assertions can be made against the document the sensor actually
# parses.
obi_config() {
  awk '/^  obi-config\.yml: \|/{f=1;next} f&&/^[^ ]/{f=0} f&&/^  [^ ]/{f=0} f' <<<"$1" | sed 's/^    //'
}

# OBI parses its config with strict YAML: a repeated key at the document root is
# a load error that stops the process, not a merge. Nothing in `helm lint` or a
# render sees that, so assert it here — a template whose branches each emit
# their own `attributes:` block shipped once and took the whole sensor down.
assert_obi_config_valid() {
  local body dups
  body="$(obi_config "$1")"
  [ -n "$body" ] || fail "$2: obi-config.yml rendered empty"
  dups="$(grep -E '^[a-z_][a-z0-9_.-]*:' <<<"$body" | sed 's/:.*//' | sort | uniq -d)"
  [ -z "$dups" ] || fail "$2: obi-config.yml repeats root key(s) [$(tr '\n' ' ' <<<"$dups")] — OBI will not start"
}

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
# gen_ai content redaction precedes the tenant stamp on the two signals that
# can carry message text (traces and logs); metrics has none to carry.
n=$(grep -cE 'processors: \[(transform/genai, )?resource/tenant, batch\]' <<<"$out")
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

echo "== wider ingest: all receivers off by default"
out="$(render)"
for port in 14250 14268 9411 9291 3100; do
  grep -q "$port" <<<"$out" && fail "receiver port $port rendered by default"
done
grep -q 'receivers: \[otlp\]$' <<<"$out" || fail "default pipelines gained a receiver"
ok "no wider-ingest receiver, port or pipeline entry by default"

echo "== wider ingest: each flag opens exactly its own surface"
out="$(render --set gateway.receivers.jaeger.enabled=true)"
grep -q 'endpoint: 0.0.0.0:14250' <<<"$out" || fail "jaeger grpc endpoint missing"
grep -q 'endpoint: 0.0.0.0:14268' <<<"$out" || fail "jaeger thrift_http endpoint missing"
grep -q 'receivers: \[otlp, jaeger\]' <<<"$out" || fail "jaeger not wired into traces"
grep -q 'containerPort: 14250' <<<"$out" || fail "jaeger-grpc containerPort missing"
grep -q 'targetPort: jaeger-thrift' <<<"$out" || fail "jaeger-thrift Service port missing"
grep -qE '68(31|32)' <<<"$out" && fail "jaeger UDP thrift rendered (deliberately unsupported)"
grep -q '9411' <<<"$out" && fail "zipkin surface rendered by the jaeger flag"
ok "jaeger: grpc+thrift_http, ports, traces pipeline — and nothing else"

out="$(render --set gateway.receivers.zipkin.enabled=true)"
grep -q 'endpoint: 0.0.0.0:9411' <<<"$out" || fail "zipkin endpoint missing"
grep -q 'receivers: \[otlp, zipkin\]' <<<"$out" || fail "zipkin not wired into traces"
grep -q 'targetPort: zipkin' <<<"$out" || fail "zipkin Service port missing"
ok "zipkin: endpoint, ports, traces pipeline"

out="$(render --set gateway.receivers.prometheusRemoteWrite.enabled=true)"
grep -q 'endpoint: 0.0.0.0:9291' <<<"$out" || fail "prometheusremotewrite endpoint missing"
grep -q 'receivers: \[otlp, prometheusremotewrite\]' <<<"$out" || fail "prom-rw not wired into metrics"
grep -q 'targetPort: prom-rw' <<<"$out" || fail "prom-rw Service port missing"
ok "prometheus remote-write: endpoint, ports, metrics pipeline"

out="$(render --set gateway.receivers.loki.enabled=true)"
grep -q 'endpoint: 0.0.0.0:3100' <<<"$out" || fail "loki endpoint missing"
grep -q 'use_incoming_timestamp: true' <<<"$out" || fail "loki incoming-timestamp missing"
grep -q 'receivers: \[otlp, loki\]' <<<"$out" || fail "loki not wired into logs"
grep -q 'targetPort: loki-push' <<<"$out" || fail "loki Service port missing"
ok "loki: endpoint, ports, logs pipeline"

echo "== wider ingest: module-gated receivers disappear with their module"
out="$(render --set gateway.receivers.prometheusRemoteWrite.enabled=true --set modules.infraMetrics.enabled=false)"
grep -q '9291' <<<"$out" && fail "prom-rw surface survived infraMetrics off"
out="$(render --set gateway.receivers.loki.enabled=true --set modules.logs.enabled=false)"
grep -q '3100' <<<"$out" && fail "loki surface survived logs off"
ok "prom-rw needs infra-metrics, loki needs logs"

echo "== wider ingest: every enabled receiver carries the authenticator"
out="$(render --set gateway.receivers.jaeger.enabled=true --set gateway.receivers.zipkin.enabled=true \
  --set gateway.receivers.prometheusRemoteWrite.enabled=true --set gateway.receivers.loki.enabled=true)"
# default auth.ingest.mode=log wires the authenticator; every listener (otlp
# grpc+http, jaeger grpc+thrift, zipkin, prom-rw, loki) must carry it.
n=$(grep -c 'authenticator: avuruingestauth' <<<"$out")
[ "$n" = "7" ] || fail "expected 7 authenticator refs across all listeners (got $n)"
out="$(render --set gateway.receivers.jaeger.enabled=true --set gateway.receivers.zipkin.enabled=true \
  --set gateway.receivers.prometheusRemoteWrite.enabled=true --set gateway.receivers.loki.enabled=true \
  --set auth.ingest.mode=off)"
grep -q 'authenticator' <<<"$out" && fail "authenticator rendered with auth.ingest.mode=off"
ok "ingest auth applies uniformly, and only when the mode asks for it"

echo "== dual-write forwarding: off by default, single-exporter pipelines"
out="$(render)"
grep -q 'forward' <<<"$out" && fail "forward exporter rendered by default"
n=$(grep -c 'exporters: \[clickhouse\]$' <<<"$out")
[ "$n" = "3" ] || fail "default pipelines are not single-exporter (got $n)"
ok "no forwarding surface by default"

echo "== dual-write forwarding: otlp joins selected signals only"
out="$(render --set gateway.forward.otlp.enabled=true --set gateway.forward.otlp.endpoint=legacy:4317 \
  --set 'gateway.forward.otlp.signals={traces,logs}')"
grep -q 'otlp/forward:' <<<"$out" || fail "otlp/forward exporter block missing"
grep -q 'exporters: \[clickhouse, otlp/forward\]' <<<"$out" || fail "otlp/forward not in pipelines"
grep -q 'sending_queue' <<<"$out" || fail "forward sending_queue missing (backpressure guard)"
n=$(grep -c 'exporters: \[clickhouse, otlp/forward\]' <<<"$out")
[ "$n" = "2" ] || fail "otlp/forward should be in exactly traces+logs (got $n)"
grep -q 'exporters: \[clickhouse\]$' <<<"$out" || fail "metrics pipeline should stay single-exporter"
ok "otlp/forward in traces+logs only, queue rendered"

echo "== dual-write forwarding: http protocol renders otlphttp/forward"
out="$(render --set gateway.forward.otlp.enabled=true --set gateway.forward.otlp.endpoint=https://legacy:4318 \
  --set gateway.forward.otlp.protocol=http)"
grep -q 'otlphttp/forward:' <<<"$out" || fail "otlphttp/forward exporter block missing"
grep -q 'exporters: \[clickhouse, otlphttp/forward\]' <<<"$out" || fail "otlphttp/forward not in pipelines"
ok "protocol=http switches the exporter component"

echo "== dual-write forwarding: kafka renders topics, secret stays out of the ConfigMap"
out="$(render --set gateway.forward.kafka.enabled=true --set 'gateway.forward.kafka.brokers={kafka:9092}' \
  --set gateway.forward.kafka.sasl.enabled=true --set gateway.forward.kafka.sasl.existingSecret=kafka-creds)"
grep -q 'kafka/forward:' <<<"$out" || fail "kafka/forward exporter block missing"
grep -q 'topic: "otlp_spans"' <<<"$out" || fail "kafka traces topic missing"
grep -q 'exporters: \[clickhouse, kafka/forward\]' <<<"$out" || fail "kafka/forward not in pipelines"
grep -q '\${env:AVURUOBS_FORWARD_KAFKA_SASL_PASSWORD}' <<<"$out" || fail "kafka sasl password not env-indirected"
grep -q 'name: kafka-creds' <<<"$out" || fail "kafka sasl secretKeyRef missing from Deployment"
# The ConfigMap must never carry credential material beyond the env reference.
cm=$(awk '/kind: ConfigMap/,/^---/' <<<"$out")
grep -q 'kafka-creds' <<<"$cm" && fail "secret name leaked into a ConfigMap"
ok "kafka forwarding rendered, credentials only as env references"

echo "== dual-write forwarding: misconfiguration fails the render"
render --set gateway.forward.otlp.enabled=true >/dev/null 2>&1 && fail "otlp forward without endpoint rendered"
render --set gateway.forward.kafka.enabled=true >/dev/null 2>&1 && fail "kafka forward without brokers rendered"
render --set gateway.forward.kafka.enabled=true --set 'gateway.forward.kafka.brokers={kafka:9092}' \
  --set gateway.forward.kafka.sasl.enabled=true >/dev/null 2>&1 && fail "kafka sasl without existingSecret rendered"
ok "empty endpoint/brokers/secret fail at template time"

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

echo "== service-health: tierOverrides reach the ConfigMap and are schema-checked"
out="$(render --set serviceGroups.tierOverrides.checkout=T0)"
grep -q 'tierOverrides.*checkout.*T0' <<<"$out" || fail "tierOverrides missing from the groups ConfigMap"
if render --set serviceGroups.tierOverrides.checkout=T9 >/dev/null 2>&1; then
  fail "schema accepted an invalid tierOverrides tier"
fi
ok "tierOverrides rendered; schema rejects a bad tier"

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
grep -q 'obi.stat.tcp.rtt' <<<"$out" && fail "OBI stats config rendered without sensor.obi.network.enabled"
grep -q 'features:' <<<"$out" && fail "OBI metric features pinned without sensor.obi.network.enabled"
assert_obi_config_valid "$out" "default render"
ok "no TCP-stats config by default; OBI's own feature defaults left alone"

echo "== OBI network stats: enabled with the network feature (+ infra-metrics)"
out="$(render --set sensor.obi.network.enabled=true)"
assert_obi_config_valid "$out" "network.enabled"
body="$(obi_config "$out")"
grep -qE '^\s+- stats_tcp_rtt$' <<<"$body" || fail "the RTT feature is not in OBI's metric feature list"
grep -qE '^\s+- network$' <<<"$body" || fail "the network feature is not in OBI's metric feature list"
# Naming any feature replaces OBI's default list, so dropping this line would
# silently turn OBI's application metrics off the moment flows are enabled.
grep -qE '^\s+- application$' <<<"$body" || fail "application feature lost when the network feature list is pinned"
grep -q 'obi.stat.tcp.rtt:' <<<"$body" || fail "obi.stat.tcp.rtt attribute selection missing"
grep -q 'obi.stat.tcp.failed.connections:' <<<"$body" || fail "obi.stat.tcp.failed.connections attribute selection missing"
grep -q 'obi.stat.tcp.retransmits:' <<<"$body" || fail "obi.stat.tcp.retransmits attribute selection missing"
# The features are named ONE BY ONE. OBI's `stats` umbrella also carries
# stats_tcp_io, which fires on every send and receive — naming the umbrella
# would have switched a per-syscall metric on for every install that already
# had stats enabled, the moment the pin moved to v0.12.
grep -qE '^\s+- stats$' <<<"$body" && fail "the stats umbrella renders — it drags in the per-syscall stats_tcp_io"
grep -qE '^\s+- stats_tcp_io$' <<<"$body" && fail "stats_tcp_io must never be rendered: per-syscall event volume"
for feat in stats_tcp_rtt stats_tcp_failed_connections stats_tcp_retransmits; do
  grep -qE "^\\s+- $feat\$" <<<"$body" || fail "$feat missing from the metric feature list"
done
# Left to OBI's defaults, flow bytes carry direction + per-interface labels and
# the stats metrics carry src/dst IP addresses — a series per address pair.
grep -q 'obi.network.flow.bytes:' <<<"$body" || fail "flow-bytes attribute selection missing (cardinality unbounded)"
grep -q 'allowed_attributes' <<<"$body" && fail "allowed_attributes is not an OBI key — use attributes.select"
# The selection has to live in the same `attributes` mapping as the kubernetes
# decorator, not a second one.
[ "$(grep -cE '^attributes:' <<<"$body")" = "1" ] || fail "attributes: is not a single root mapping"
grep -q 'enable: true' <<<"$body" || fail "kubernetes decoration lost from the attributes mapping"
ok "stats feature + k8s-owner attribute selection render into one valid document"

echo "== OBI network stats: can be turned off while keeping flow bytes"
out="$(render --set sensor.obi.network.enabled=true --set sensor.obi.network.stats=false)"
assert_obi_config_valid "$out" "network.stats=false"
body="$(obi_config "$out")"
grep -q 'obi.stat.tcp.rtt' <<<"$body" && fail "stats config survived network.stats=false"
grep -qE '^\s+- stats_tcp' <<<"$body" && fail "a stats feature survived network.stats=false"
grep -qE '^network:' <<<"$body" || fail "network flow config missing"
grep -q 'obi.network.flow.bytes:' <<<"$body" || fail "flow-bytes attribute selection lost with stats off"
ok "flow bytes without TCP stats"

# The failure this assertion prevents was found by running the stats feature on a
# real kernel for the first time: OBI attaches the sock/inet_sock_set_state
# tracepoint, cilium/ebpf resolves it through debugfs or tracefs, and inside a
# container neither is present unless mounted. OBI does not skip the feature —
# it EXITS, so an optional metric took zero-code traces and network flows down
# with it and the DaemonSet crash-looped. Same shape as the RAPL-less node in
# v0.4: the mount and the switch must move together, and nothing but a render
# assertion keeps them together.
echo "== OBI stats: the tracing filesystems are mounted with the feature"
out="$(render --set sensor.enabled=true --set sensor.obi.network.enabled=true)"
obi_mounts="$(awk '/^            - name: kernel-(debug|tracing)$/{c++} END{print c+0}' <<<"$out")"
[ "$obi_mounts" -ge 2 ] || fail "TCP stats render without the kernel tracing mounts — OBI will exit on boot"
kd_vols="$(awk '/^        - name: kernel-debug$/{c++} END{print c+0}' <<<"$out")"
[ "$kd_vols" = "1" ] || fail "kernel-debug declared $kd_vols times, want exactly 1"
ok "stats bring debugfs + tracefs with them"

echo "== OBI stats off: no host tracing mount is taken"
out="$(render --set sensor.enabled=true --set sensor.obi.network.enabled=true --set sensor.obi.network.stats=false)"
kd_vols="$(awk '/^        - name: kernel-debug$/{c++} END{print c+0}' <<<"$out")"
[ "$kd_vols" = "0" ] || fail "kernel-debug mounted with stats off — a host mount nothing needs"
ok "no stats, no host tracing mount"

# Profiler and OBI both want these paths; a pod may declare a volume once.
echo "== OBI stats + profiler: one volume, two consumers"
out="$(render --set sensor.enabled=true --set sensor.profiler.enabled=true --set sensor.obi.network.enabled=true)"
kd_vols="$(awk '/^        - name: kernel-debug$/{c++} END{print c+0}' <<<"$out")"
kd_mounts="$(awk '/^            - name: kernel-debug$/{c++} END{print c+0}' <<<"$out")"
[ "$kd_vols" = "1" ] || fail "kernel-debug declared $kd_vols times with both consumers on — a duplicate volume name is a rejected manifest"
[ "$kd_mounts" = "2" ] || fail "kernel-debug mounted $kd_mounts times, want both the profiler and OBI"
ok "one declaration, mounted into both containers"

echo "== OBI retransmits: rides stats, and can be dropped on its own"
out="$(render --set sensor.obi.network.enabled=true --set sensor.obi.network.retransmits=false)"
assert_obi_config_valid "$out" "retransmits=false"
body="$(obi_config "$out")"
grep -qE '^\s+- stats_tcp_retransmits$' <<<"$body" && fail "retransmit feature survived retransmits=false"
grep -q 'obi.stat.tcp.retransmits:' <<<"$body" && fail "retransmit attribute selection survived retransmits=false"
# The rest of the stats surface is untouched: this switch is about one metric.
grep -qE '^\s+- stats_tcp_rtt$' <<<"$body" || fail "RTT feature lost with retransmits off"
ok "retransmits are a switch of their own"

echo "== inter-zone accounting: off by default, standalone when on"
out="$(render)"
grep -q 'network_inter_zone' <<<"$out" && fail "inter-zone feature rendered by default"
ok "no inter-zone accounting by default"

out="$(render --set sensor.obi.network.interZone.enabled=true)"
assert_obi_config_valid "$out" "interZone standalone"
body="$(obi_config "$out")"
grep -qE '^\s+- network_inter_zone$' <<<"$body" || fail "inter-zone feature missing from the metric feature list"
# The point of the standalone shape: zone accounting without buying the
# per-edge flow stream.
grep -qE '^\s+- network$' <<<"$body" && fail "the per-edge network feature came along with inter-zone"
grep -qE '^network:' <<<"$body" && fail "the per-edge network config rendered for inter-zone alone"
grep -q 'obi.network.flow.bytes:' <<<"$body" && fail "per-edge attribute selection rendered for inter-zone alone"
grep -q 'hostNetwork: true' <<<"$out" || fail "hostNetwork missing with inter-zone on"
ok "inter-zone alone: its feature, the flow pipeline's hostNetwork, nothing per-edge"

echo "== inter-zone accounting: composes with per-edge flows"
out="$(render --set sensor.obi.network.interZone.enabled=true --set sensor.obi.network.enabled=true)"
assert_obi_config_valid "$out" "interZone + network"
body="$(obi_config "$out")"
for feat in application network stats_tcp_rtt stats_tcp_failed_connections network_inter_zone; do
  grep -qE "^\\s+- $feat\$" <<<"$body" || fail "$feat missing when both network flags are on"
done
ok "both flags on: one feature list, one valid document"

echo "== inter-zone accounting: needs somewhere to store the counters"
if render --set sensor.obi.network.interZone.enabled=true --set modules.infraMetrics.enabled=false >/dev/null 2>&1; then
  fail "inter-zone without infra-metrics should fail at template time"
fi
if render --set sensor.obi.network.interZone.enabled=maybe >/dev/null 2>&1; then
  fail "non-boolean interZone.enabled should fail schema validation"
fi
ok "infra-metrics guard fires; schema rejects a non-boolean"
echo "== endpoint checks: nothing rendered when none are declared"
out="$(render)"
grep -q 'AVURUOBS_GATEWAY_OTLP_ENDPOINT' <<<"$out" && fail "the check span endpoint rendered with no checks declared"
ok "no checks, no span endpoint"

echo "== endpoint checks: declared -> config, span endpoint, ingest key"
CHECKS_VALUES="$(mktemp)"
cat > "$CHECKS_VALUES" <<'YAML'
serviceGroups:
  groups:
    - name: core
      tier: T0
      selector: { namespaces: [storefront] }
      checks:
        - id: core-login
          url: https://app.example.com/api/health
          interval: 60s
          expect: { status: 200, maxLatency: 800ms }
YAML
out="$(render -f "$CHECKS_VALUES")"
grep -q 'core-login' <<<"$out" || fail "the declared check did not reach the groups ConfigMap"
grep -q 'AVURUOBS_GATEWAY_OTLP_ENDPOINT' <<<"$out" || fail "no span endpoint for the hub's own probe traffic"
ok "check config, span endpoint"

# Under enforce, the hub sending check spans is a sender like any other and
# needs a key — otherwise the gateway rejects its own platform's traffic.
out="$(render -f "$CHECKS_VALUES" --set auth.ingest.mode=enforce)"
hub_block="$(awk '/^  name: .*-hub$/,/^---$/' <<<"$out")"
key_hits="$(grep -c 'AVURUOBS_INGEST_KEY' <<<"$hub_block" || true)"
[ "${key_hits:-0}" -gt 0 ] || fail "the hub has no ingest key in enforce mode — its check spans would be rejected"
ok "enforce mode gives the hub a key for its own spans"

# service-health owns checks: with the module off there is nothing to answer for.
out="$(render -f "$CHECKS_VALUES" --set modules.serviceHealth.enabled=false)"
grep -q 'AVURUOBS_GATEWAY_OTLP_ENDPOINT' <<<"$out" && fail "checks rendered without the service-health module that owns them"
ok "no service-health, no checks"
rm -f "$CHECKS_VALUES"

echo "== mesh: off by default, whole surface absent"
out="$(render)"
grep -q 'prometheus/mesh' <<<"$out" && fail "the mesh control-plane scrape rendered on a default install"
grep -q ',mesh' <<<"$out" && fail "the mesh module is in AVURUOBS_MODULES by default"
ok "no mesh module, no istiod scrape"

echo "== mesh: the module alone adds the surface but no collection"
out="$(render --set modules.mesh.enabled=true)"
grep -q ',mesh' <<<"$out" || fail "modules.mesh.enabled did not reach AVURUOBS_MODULES"
grep -q 'prometheus/mesh' <<<"$out" && fail "the scrape rendered without mesh.controlPlane.enabled"
ok "module on, scrape still opt-in"

echo "== mesh: the control-plane scrape lands in the GATEWAY, not the sensor"
out="$(render --set modules.mesh.enabled=true --set mesh.controlPlane.enabled=true)"
grep -q 'job_name: "istiod"' <<<"$out" || fail "istiod scrape job missing"
grep -q 'pilot_total_xds_rejects' <<<"$out" || fail "the rejected-config metric is not in the keep list"
# istiod is ONE Deployment: scraped per-node it would yield a copy of every
# control-plane series per node, and any sum over them would be wrong by the
# size of the cluster.
# Counted rather than `grep -q`: on a body this size grep -q can exit non-zero
# on a real match (it stops reading early and takes SIGPIPE), which would make
# this assertion fail on correct output.
gw_hits="$(awk '/^  name: .*-gateway$/,/^---$/' <<<"$out" | grep -c 'prometheus/mesh' || true)"
[ "${gw_hits:-0}" -gt 0 ] || fail "the scrape is not in the gateway config"
sensor_hits="$(awk '/^  name: .*-sensor-agent$/,/^---$/' <<<"$out" | grep -c 'istiod' || true)"
[ "${sensor_hits:-0}" = "0" ] || fail "the istiod scrape rendered into the sensor DaemonSet — one series per node"
pipe_hits="$(grep -cE 'receivers: \[otlp.*prometheus/mesh\]' <<<"$out" || true)"
[ "${pipe_hits:-0}" -gt 0 ] || fail "prometheus/mesh is not wired into the metrics pipeline"
ok "one scraper, in the single-writer collector"

echo "== mesh: the scrape job name is ONE value, reaching the scrape and the hub"
# The hub looks the scrape-report series up by job name to tell "nobody is
# scraping" from "the target is not answering" from "it answered with metrics we
# cannot read". Typed in two places, those two would drift and the hub would
# report a perfectly configured control plane as unconfigured.
out="$(render --set modules.mesh.enabled=true --set mesh.controlPlane.enabled=true --set mesh.controlPlane.jobName=pilot)"
job_hits="$(grep -c 'job_name: "pilot"' <<<"$out" || true)"
[ "${job_hits:-0}" -gt 0 ] || fail "the configured job name did not reach the scrape config"
env_hits="$(grep -c 'AVURUOBS_MESH_SCRAPE_JOB' <<<"$out" || true)"
[ "${env_hits:-0}" -gt 0 ] || fail "the job name did not reach the hub"
val_hits="$(grep -A1 'AVURUOBS_MESH_SCRAPE_JOB' <<<"$out" | grep -c '"pilot"' || true)"
[ "${val_hits:-0}" -gt 0 ] || fail "the hub was told a different job name than the gateway scrapes under"
ok "one value, both ends"

echo "== mesh: misconfiguration fails at template time rather than collecting nothing"
if render --set mesh.controlPlane.enabled=true >/dev/null 2>&1; then
  fail "controlPlane without modules.mesh should fail — otherwise it scrapes data no route can read"
fi
if render --set modules.mesh.enabled=true --set mesh.controlPlane.enabled=true --set modules.infraMetrics.enabled=false >/dev/null 2>&1; then
  fail "controlPlane without infra-metrics should fail — the series would have nowhere to land"
fi
# A values file that reaches a query-only instance must not fail its install.
render --set modules.mesh.enabled=true --set mesh.controlPlane.enabled=true --set gateway.enabled=false >/dev/null 2>&1 \
  || fail "mesh values failed an install with no gateway to scrape from"
ok "both guards fire; a gateway-less install is unaffected"

echo "== cost: off by default, nothing watched, nothing granted"
out="$(render)"
grep -q 'k8s_cluster' <<<"$out" && fail "the cluster-object receiver rendered on a default install"
grep -q 'k8s_leader_elector' <<<"$out" && fail "the leader-election extension rendered on a default install"
grep -q 'coordination.k8s.io' <<<"$out" && fail "Lease permissions granted to an install that does not run the cost module"
grep -q ',cost' <<<"$out" && fail "the cost module is in AVURUOBS_MODULES by default"
ok "no cost module, no cluster reads, no Lease"

echo "== cost: the receiver and its leader election never travel apart"
# The v0.9 sensor crash was a switch and its prerequisite rendering separately.
# A k8s_cluster naming an extension that is not registered is a collector that
# refuses to start, and one WITHOUT election is every series times node count —
# so the pair is asserted, not the parts.
out="$(render --set modules.cost.enabled=true)"
grep -q ',cost' <<<"$out" || fail "modules.cost.enabled did not reach AVURUOBS_MODULES"
agent="$(awk '/^  name: .*-sensor-agent$/,/^---$/' <<<"$out")"
recv_hits="$(grep -c 'k8s_cluster:' <<<"$agent" || true)"
[ "${recv_hits:-0}" -gt 0 ] || fail "the cluster-object receiver is missing"
elect_hits="$(grep -c 'k8s_leader_elector:' <<<"$agent" || true)"
# Twice: the receiver's reference, and the extension's own definition.
[ "${elect_hits:-0}" -ge 2 ] || fail "k8s_cluster rendered without the leader-election extension beside it"
reg_hits="$(grep -c 'extensions: \[health_check, file_storage, k8s_leader_elector\]' <<<"$agent" || true)"
[ "${reg_hits:-0}" -gt 0 ] || fail "the elector is defined but never registered in service.extensions"
alloc_hits="$(grep -c 'allocatable_types_to_report: \[cpu, memory\]' <<<"$agent" || true)"
[ "${alloc_hits:-0}" -gt 0 ] || fail "node allocatable is off — it is not a default-on metric"
pipe_hits="$(grep -c 'metrics/cluster:' <<<"$agent" || true)"
[ "${pipe_hits:-0}" -gt 0 ] || fail "the cluster receiver is not wired into a pipeline"
lease_hits="$(grep -c 'coordination.k8s.io' <<<"$out" || true)"
[ "${lease_hits:-0}" -gt 0 ] || fail "the Lease the election runs on is not granted"
ok "receiver, elector, registration, Lease — all four or none"

echo "== cost: the sensor sub-flag drops collection without dropping the module"
out="$(render --set modules.cost.enabled=true --set sensor.agent.cluster.enabled=false)"
grep -q ',cost' <<<"$out" || fail "the module left AVURUOBS_MODULES when only collection was switched off"
grep -q 'k8s_cluster' <<<"$out" && fail "the receiver rendered with sensor.agent.cluster.enabled=false"
grep -q 'coordination.k8s.io' <<<"$out" && fail "the Lease was granted with no receiver to elect for"
ok "module and collection are separable, like green"

echo "== cost: rates are the operator's to declare, and absent means absent"
out="$(render --set modules.cost.enabled=true)"
grep -q 'AVURUOBS_COST_' <<<"$out" && fail "a cost rate reached the hub with none configured — unpriced must stay unpriced"
# `helm --set x=0.0331` yields a STRING (only integers are coerced), so the
# schema takes both forms; an operator setting a rate the obvious way must not
# be told their values are invalid.
out="$(render --set modules.cost.enabled=true --set cost.rates.cpuCoreHour=0.0331 --set cost.rates.memGiBHour=0.004 --set cost.rates.currency=EUR)"
grep -q 'AVURUOBS_COST_CPU_CORE_HOUR' <<<"$out" || fail "the cpu rate did not reach the hub"
grep -q 'AVURUOBS_COST_MEM_GIB_HOUR' <<<"$out" || fail "the memory rate did not reach the hub"
grep -q 'AVURUOBS_COST_CURRENCY' <<<"$out" || fail "the currency did not reach the hub"
ok "unset stays unset; a decimal rate is accepted"

echo "== cost: an install that could only render blanks fails at template time"
if render --set modules.cost.enabled=true --set modules.infraMetrics.enabled=false >/dev/null 2>&1; then
  fail "cost without infra-metrics should fail — reserved has nothing to be compared against"
fi
ok "the guard fires"

echo "== business tags: nothing mapped by default"
out="$(render)"
grep -q 'avuru.tag.' <<<"$out" && fail "a business tag rendered with no tags.labels set"
assert_obi_config_valid "$out" "no tags"
ok "no tag mapping until an operator maps one"

echo "== transport evidence: the mesh's own labels, carried unconditionally"
# resource_labels is no longer a tags-only block: the transport labels ride it
# on every install, because an operator should not have to know their gateway
# is mis-drawn in order to fix it (design/2026-08-26-transport-from-labels.md).
out="$(render)"
body="$(obi_config "$out")"
for label in gateway.networking.k8s.io/gateway-name istio.io/gateway-name \
             operator.istio.io/component linkerd.io/control-plane-component; do
  grep -q "$label" <<<"$body" || fail "transport label $label is not carried"
done
# These mark a MESHED APPLICATION, not a proxy. Carrying them would classify
# every sidecar-injected workload as transport and empty the map.
grep -q 'service.istio.io/canonical-name' <<<"$body" && fail "canonical-name would classify applications as transport"
grep -q 'security.istio.io/tlsMode' <<<"$body" && fail "tlsMode would classify applications as transport"
# TRACES ONLY: the classifier reads otel_traces, and a trace-side decision must
# not become a dimension on every metric.
agent="$(awk '/^  name: .*-sensor-agent$/,/^---$/' <<<"$out")"
agent_hits="$(grep -c 'avuru.transport.' <<<"$agent" || true)"
[ "${agent_hits:-0}" = "0" ] || fail "transport labels reached the agent's k8sattributes — metric cardinality for a trace question"
ok "carried on traces, absent from metrics, application labels excluded"

echo "== business tags: one mapping reaches BOTH collection paths"
out="$(render --set 'tags.labels.team=team' --set 'tags.labels.tier=app.kubernetes.io/component')"
assert_obi_config_valid "$out" "tags mapped"
body="$(obi_config "$out")"
# Traces: the eBPF tracer's own Kubernetes decoration.
grep -q 'avuru.tag.team: \["team"\]' <<<"$body" || fail "team tag missing from the tracer's resource_labels"
grep -q 'avuru.tag.tier: \["app.kubernetes.io/component"\]' <<<"$body" || fail "tier tag missing from the tracer's resource_labels"
# Logs + metrics: the agent's k8sattributes. Both must be present, or a filter
# would mean different things on different screens.
grep -q 'tag_name: avuru.tag.team' <<<"$out" || fail "team tag missing from k8sattributes labels"
grep -q 'tag_name: avuru.tag.tier' <<<"$out" || fail "tier tag missing from k8sattributes labels"
# The opt-out label must survive alongside: it shares the labels list.
grep -q 'tag_name: avuru.obs.collect' <<<"$out" || fail "collection opt-out label lost next to business tags"
ok "one mapping renders into the tracer and the agent, opt-out label intact"

echo "== business tags: refused when they would cost more than they say"
if render --set 'tags.labels.a=a' --set 'tags.labels.b=b' --set 'tags.labels.c=c' \
  --set 'tags.labels.d=d' --set 'tags.labels.e=e' --set 'tags.labels.f=f' \
  --set 'tags.labels.g=g' --set 'tags.labels.h=h' --set 'tags.labels.i=i' \
  --set 'tags.labels.j=j' --set 'tags.labels.k=k' --set 'tags.labels.l=l' \
  --set 'tags.labels.m=m' >/dev/null 2>&1; then
  fail "13 mapped tags should exceed the cardinality cap"
fi
if render --set 'tags.labels.9bad=team' >/dev/null 2>&1; then
  fail "a tag key that is not a usable attribute name should fail the render"
fi
ok "cardinality cap and key shape enforced at template time"

echo "== service-map topology: ungated, because the map cannot be turned off"
out="$(render)"
grep -q 'AVURUOBS_TOPOLOGY_CONFIG' <<<"$out" || fail "topology env missing on a default install"
grep -q 'name: test-avuruobs-topology' <<<"$out" || fail "topology ConfigMap missing on a default install"
grep -q 'topology.json: "{}"' <<<"$out" || fail "empty topology should render {} (hub built-ins)"
# Its own mount dir: sharing one with service-groups would make either config
# clobber the other's file.
grep -q '/etc/avuruobs-topology' <<<"$out" || fail "topology mounted outside its own directory"
ok "env, ConfigMap and an isolated mount on every hub"

out="$(render --set-json 'topology={"transport":["acme-mesh-*"],"applications":["waypoint-api"]}')"
grep -q 'acme-mesh' <<<"$out" || fail "configured transport patterns did not reach the ConfigMap"
grep -q 'waypoint-api' <<<"$out" || fail "configured application overrides did not reach the ConfigMap"
ok "operator patterns reach the hub"

# Query-only (secondary-cluster) installs still run the map, so they still get
# the knob; a gateway-only install has no hub and must render neither.
out="$(render --set hub.enabled=false --set ui.enabled=false \
  --set clickhouse.external.enabled=true --set clickhouse.external.address=ch.central:9000 \
  --set auth.ingest.mode=off)"
grep -q 'test-avuruobs-topology' <<<"$out" && fail "topology ConfigMap rendered on a gateway-only install"
ok "absent on a hub-less install"

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
grep -q 'obi.stat.tcp.rtt' <<<"$out" || fail "OBI TCP-stats config lost next to green"
assert_obi_config_valid "$out" "green + obi.network"
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

# Kepler v0.11.4 EXITS when RAPL zone discovery comes up empty ("failed to
# initialize service rapl: no RAPL zones found") — it does not sit idle, and no
# probe config can save a process that terminates itself. On a VM fleet the
# container crash-loops, the sensor pod never reports Ready, and `helm --wait`
# / `rollout status` fail: the whole sensor is down over an optional signal.
# So the measured source must be droppable on its own, leaving the estimator.
echo "== green: kepler.enabled=false leaves the estimator collecting alone"
out="$(render --set modules.green.enabled=true --set sensor.green.enabled=true \
  --set sensor.green.kepler.enabled=false \
  --set sensor.green.estimation.enabled=true)"
grep -q '\- name: kepler$' <<<"$out" && fail "kepler container rendered with sensor.green.kepler.enabled=false"
grep -q 'job_name: kepler' <<<"$out" && fail "kepler scrape job survived the container it scrapes"
grep -q 'name: test-avuruobs-sensor-kepler' <<<"$out" && fail "kepler ConfigMap survived the container it configures"
grep -qF 'set(attributes["avuruobs_quality"], "measured")' <<<"$out" && fail "measured quality stamp rendered with nothing measuring"
grep -q '\- name: tdp-estimator$' <<<"$out" || fail "tdp-estimator container missing as the sole source"
grep -q 'job_name: tdp-estimator' <<<"$out" || fail "tdp-estimator scrape job missing as the sole source"
grep -q 'prometheus/green:' <<<"$out" || fail "green receiver lost with kepler off"
grep -q 'metrics/green:' <<<"$out" || fail "green pipeline lost with kepler off"
grep -qF 'set(attributes["avuruobs_quality"], "estimated") where resource.attributes["service.name"] == "tdp-estimator"' <<<"$out" \
  || fail "estimated quality stamp lost with kepler off"
# The host mounts hang off the green gate, not off Kepler — the estimator's own
# RAPL probe and cgroup walk still need them.
estimator_block="$(sed -n '/- name: tdp-estimator$/,/resources:/p' <<<"$out")"
grep -q 'mountPath: /sys' <<<"$estimator_block" || fail "tdp-estimator lost its /sys mount with kepler off"
grep -q 'mountPath: /proc' <<<"$estimator_block" || fail "tdp-estimator lost its /proc mount with kepler off"
ok "estimator + scrape job + estimated stamp + host mounts survive; every Kepler surface is gone"

echo "== green: kepler.enabled=false with no estimator -> fails at template time"
render --set modules.green.enabled=true --set sensor.green.enabled=true \
  --set sensor.green.kepler.enabled=false >/dev/null 2>&1 \
  && fail "green collection rendered with no energy source (prometheus/green would have zero scrape jobs)"
ok "dropping both sources fails loudly instead of shipping an empty scrape config"

echo "== green: kepler.enabled=false no-ops when green collection is off"
# Same discipline as sensor.green-without-the-module: values that render nothing
# must never fail an install (a shared values file on a sensor-off cluster).
render --set sensor.green.kepler.enabled=false >/dev/null 2>&1 \
  || fail "kepler.enabled=false failed with the green module off instead of no-opping"
ok "no guard fires while the green surface renders nothing"

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

echo "== auth origin check: strict by default, and silent about it"
out="$(render)"
grep -q 'AVURUOBS_AUTH_TRUSTED_ORIGINS' <<<"$out" && fail "trusted-origins env rendered with an empty list"
grep -q 'AVURUOBS_AUTH_ORIGIN_CHECK' <<<"$out" && fail "origin-check env rendered while at the enforce default"
ok "an install that touches neither knob renders neither env"

echo "== auth origin check: trustedOrigins renders the allowlist"
out="$(render --set-json 'auth.trustedOrigins=["https://obs.example.com","https://obs.internal"]')"
grep -q 'AVURUOBS_AUTH_TRUSTED_ORIGINS' <<<"$out" || fail "trusted-origins env missing"
grep -q 'value: "https://obs.example.com,https://obs.internal"' <<<"$out" || fail "trusted origins not joined as CSV"
grep -q 'AVURUOBS_AUTH_ORIGIN_CHECK' <<<"$out" && fail "declaring origins must not lower the mode"
ok "list joined into one env, mode untouched"

echo "== auth origin check: lowered modes are explicit, and only the valid ones"
out="$(render --set auth.originCheck=log)"
grep -q 'name: AVURUOBS_AUTH_ORIGIN_CHECK' <<<"$out" || fail "origin-check env missing in log mode"
grep -q 'value: "log"' <<<"$out" || fail "log mode not rendered"
render --set auth.originCheck=allow >/dev/null 2>&1 \
  && fail "an unknown origin-check mode rendered (schema enum should reject it)"
ok "log/off render the env; a typo fails the render"

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
# The optional gen_ai redaction sits ahead of the whole tenant stage on traces
# and logs; what matters here is that tenantfromauth is still LAST.
n=$(grep -cE 'processors: \[(transform/genai, )?resource/tenant, tenantfromauth, batch\]' <<<"$out")
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
# The flag-on half of this guard can't use auth.enabled=false any more (that
# combination is refused outright — see the auth guard below), so prove the
# env is outside the auth conditional using the OIDC-off/auth-on install.
hub="$(render -s templates/hub-deploy.yaml --set collection.runtimeControl.enabled=true)"
grep -A1 'name: AVURUOBS_COLLECTION_RUNTIME_CONTROL_ENABLED' <<<"$hub" | grep -q 'value: "true"' \
  || fail "collection env missing with the flag on"
ok "env rendered independently of the auth conditional"

echo "== collection runtime control: guard refuses an unauthenticated write API"
render --set collection.runtimeControl.enabled=true --set auth.enabled=false >/dev/null 2>&1 \
  && fail "collection.runtimeControl.enabled rendered with auth.enabled=false (securedAdmin is a pass-through then — the overlay write API would be anonymous)"
ok "flag without auth.enabled fails at template time"

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

echo "== collection runtime control: placeholder ConfigMaps for disabled signals"
# Kubernetes RBAC cannot restrict `create` by resourceName, so the control Role
# is create-free (asserted above) — which only works if every sensor ConfigMap
# the applier may rewrite already exists. Off by default: a disabled signal
# renders nothing at all.
out="$(render)"
grep -q "name: test-avuruobs-sensor-profiler" <<<"$out" && fail "profiler ConfigMap rendered on a default install (profiler disabled, runtime control off)"
out="$(render --set collection.runtimeControl.enabled=true)"
for cm in sensor-obi sensor-agent sensor-profiler sensor-kepler; do
  grep -q "name: test-avuruobs-$cm" <<<"$out" || fail "runtime control on: $cm ConfigMap missing (applier cannot create, only update)"
done
grep -q "Placeholder: signal disabled" <<<"$out" || fail "disabled-signal ConfigMap did not render placeholder content"
# Every signal off: all four are placeholders, none silently dropped.
out="$(render --set collection.runtimeControl.enabled=true --set sensor.obi.enabled=false \
  --set sensor.agent.enabled=false --set modules.logs.enabled=false)"
[ "$(grep -c 'Placeholder: signal disabled' <<<"$out")" -eq 4 ] \
  || fail "expected 4 placeholder ConfigMaps with every signal off"
ok "placeholder ConfigMaps render when runtime control is on"

echo "== collection runtime control: hub identity env"
hub="$(render -s templates/hub-deploy.yaml --set collection.runtimeControl.enabled=true)"
grep -q 'AVURUOBS_RELEASE_NAMESPACE' <<<"$hub" || fail "hub missing AVURUOBS_RELEASE_NAMESPACE downward-API env"
grep -A3 'name: AVURUOBS_RELEASE_NAMESPACE' <<<"$hub" | grep -q 'fieldPath: metadata.namespace' \
  || fail "AVURUOBS_RELEASE_NAMESPACE not sourced from the downward API (must never be guessed)"
grep -q 'AVURUOBS_COLLECTION_FULLNAME' <<<"$hub" || fail "hub missing AVURUOBS_COLLECTION_FULLNAME env"
hub="$(render -s templates/hub-deploy.yaml)"
grep -q 'AVURUOBS_RELEASE_NAMESPACE' <<<"$hub" && fail "identity env rendered with runtime control off"
ok "hub identity env gated on the flag, namespace via downward API"

echo "== collection runtime control: base-values carries collection subtree"
# The hub re-renders the sensor templates from this ConfigMap; without the
# collection subtree its render would see the flag off and drop the very
# placeholders it may only update.
out="$(render --set collection.runtimeControl.enabled=true)"
base_values="$(awk '/^  values\.json: \|$/{f=1;next} /^---$/{f=0} f' <<<"$out")"
python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d.get("collection",{}).get("runtimeControl",{}).get("enabled") is True else 1)' \
  <<<"$base_values" || fail "base-values ConfigMap missing collection.runtimeControl.enabled=true"
ok "base-values includes collection"

echo "== collection runtime control: base-values carries gateway.tenant"
# gateway.tenant sits outside the sensor/modules subtrees but sensor-config.yaml
# renders it as the profiler exporter's X-Avuru-Tenant header. Absent here, the
# first overlay write would silently retag a tenanted install as `default`.
out="$(render --set gateway.tenant=acme --set collection.runtimeControl.enabled=true)"
base_values="$(awk '/^  values\.json: \|$/{f=1;next} /^---$/{f=0} f' <<<"$out")"
python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d.get("gateway",{}).get("tenant")=="acme" else 1)' \
  <<<"$base_values" || fail "base-values ConfigMap missing gateway.tenant=acme"
ok "base-values includes gateway.tenant"

echo "== collection runtime control: base-values carries image.pullSecrets + green.metrics"
# The other two subtrees the sensor templates read from outside sensor/modules.
# Miss image.pullSecrets and the hub's re-render drops imagePullSecrets from the
# sensor pod — every sensor image then fails to pull on a private registry.
# Miss green.metrics and the Kepler scrape loses the pod→workload attribute
# rename, breaking the energy join on the next overlay write.
out="$(render --set 'image.pullSecrets[0].name=regcred' \
              --set green.metrics.podNameAttr=custom_pod \
              --set collection.runtimeControl.enabled=true)"
base_values="$(awk '/^  values\.json: \|$/{f=1;next} /^---$/{f=0} f' <<<"$out")"
python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if [s.get("name") for s in d.get("image",{}).get("pullSecrets",[])]==["regcred"] else 1)' \
  <<<"$base_values" || fail "base-values ConfigMap missing image.pullSecrets"
python3 -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d.get("green",{}).get("metrics",{}).get("podNameAttr")=="custom_pod" else 1)' \
  <<<"$base_values" || fail "base-values ConfigMap missing green.metrics.podNameAttr=custom_pod"
ok "base-values includes image.pullSecrets and green.metrics"
# Helm skips post-install/post-upgrade hooks when `--wait` times out, so the
# migrate Job is not a guarantee — the hub must be told it may repair the schema
# itself, and the Job must leave evidence that it ran.
echo "== schema migration delivery"
out="$(render)"
grep -q 'name: AVURUOBS_SCHEMA_AUTOMIGRATE' <<<"$out" || fail "hub is missing AVURUOBS_SCHEMA_AUTOMIGRATE"
grep -A1 'name: AVURUOBS_SCHEMA_AUTOMIGRATE' <<<"$out" | grep -q 'value: "true"' \
  || fail "schema self-heal is not on by default"
ok "hub self-heal enabled by default"

out="$(render --set hub.autoMigrate=false)"
grep -A1 'name: AVURUOBS_SCHEMA_AUTOMIGRATE' <<<"$out" | grep -q 'value: "false"' \
  || fail "hub.autoMigrate=false did not reach the hub"
ok "hub.autoMigrate=false opts out"

render --set hub.autoMigrate=yes >/dev/null 2>&1 && fail "non-boolean hub.autoMigrate accepted by the values schema"
ok "values schema rejects a non-boolean hub.autoMigrate"

out="$(render)"
migrate_job="$(awk '/^# Source: avuruobs\/templates\/migrate-job.yaml$/{f=1} /^---$/{f=0} f' <<<"$out")"
grep -q 'helm.sh/hook": post-install,post-upgrade' <<<"$migrate_job" \
  || grep -q 'hook: post-install,post-upgrade' <<<"$migrate_job" \
  || fail "migrate Job lost its post-install/post-upgrade hook"
# The annotation VALUE, not any mention of the word — the template carries an
# explanatory comment naming hook-succeeded, and it renders into the manifest.
grep -E '^\s*"helm.sh/hook-delete-policy":' <<<"$migrate_job" | grep -q 'hook-succeeded' \
  && fail "migrate Job still deletes itself on success (no evidence it ran)"
grep -q 'ttlSecondsAfterFinished:' <<<"$migrate_job" || fail "migrate Job has no ttlSecondsAfterFinished"
grep -q 'name: AVURUOBS_MIGRATE_WAIT_SECONDS' <<<"$migrate_job" || fail "migrate Job is missing AVURUOBS_MIGRATE_WAIT_SECONDS"
ok "migrate Job: hook kept, survives success, ClickHouse wait configurable"

echo "== retention reaches BOTH the migrator and the hub"
out="$(render --set retention.traces=30)"
hub_deploy="$(awk '/^# Source: avuruobs\/templates\/hub-deploy.yaml$/{f=1} /^---$/{f=0} f' <<<"$out")"
migrate_job="$(awk '/^# Source: avuruobs\/templates\/migrate-job.yaml$/{f=1} /^---$/{f=0} f' <<<"$out")"
# The migrator turns retention into table TTLs; the hub REPORTS it (configured
# vs enforced on Settings -> Storage) and bounds a per-project window with it.
# When only the Job carried the values, a hub on non-default retention fell back
# to its built-in defaults and reported drift that did not exist.
grep -A1 'name: AVURUOBS_RETENTION_TRACES_DAYS' <<<"$migrate_job" | grep -q 'value: "30"' \
  || fail "retention.traces did not reach the migrate Job"
grep -A1 'name: AVURUOBS_RETENTION_TRACES_DAYS' <<<"$hub_deploy" | grep -q 'value: "30"' \
  || fail "retention.traces did not reach the hub Deployment"
for signal in LOGS METRICS PROFILES ERRORS; do
  grep -q "name: AVURUOBS_RETENTION_${signal}_DAYS" <<<"$hub_deploy" \
    || fail "hub is missing AVURUOBS_RETENTION_${signal}_DAYS"
done
ok "retention values render on the hub and the migrate Job alike"

echo "== component toggles: the secondary-cluster shape"
# A cluster that only ships telemetry installs the gateway (+sensor) and points
# at the central ClickHouse. Everything hub-owned must disappear — a hub
# Deployment or a migrate Job here would race the primary cluster's schema.
secondary=(--set hub.enabled=false --set ui.enabled=false
           --set clickhouse.external.enabled=true --set clickhouse.external.address=ch.central:9000
           --set auth.ingest.mode=off)
out="$(render "${secondary[@]}")"
for absent in "-hub$" "-ui$" "-migrate$" "kind: StatefulSet"; do
  if grep -qE "name: test-avuruobs${absent}" <<<"$out" 2>/dev/null; then
    fail "gateway-only render still contains ${absent}"
  fi
done
grep -q "kind: Job" <<<"$out" && fail "gateway-only render still runs the migrate Job"
grep -q "name: test-avuruobs-gateway" <<<"$out" || fail "gateway-only render lost the gateway"
grep -q "kind: DaemonSet" <<<"$out" || fail "gateway-only render lost the sensor"
ok "gateway-only: gateway + sensor render, hub/UI/migrate/ClickHouse do not"

# The mirror image: an instance that only queries, fed by other clusters.
out="$(render --set gateway.enabled=false --set sensor.enabled=false)"
grep -q "name: test-avuruobs-hub" <<<"$out" || fail "query-only render lost the hub"
grep -q "name: test-avuruobs-gateway" <<<"$out" && fail "query-only render still contains the gateway"
grep -q "kind: DaemonSet" <<<"$out" && fail "query-only render still contains the sensor"
ok "query-only: hub + UI render, gateway and sensor do not"

echo "== component toggles: combinations that cannot work are refused"
# Each of these installs pods that cannot reach what they need. Failing at
# template time is the whole point — the alternative is a green rollout whose
# every request 502s.
render --set ui.enabled=false --set hub.enabled=false --set gateway.enabled=false --set sensor.enabled=false >/dev/null 2>&1 \
  && fail "an install with no components at all was accepted"
render --set hub.enabled=false >/dev/null 2>&1 \
  && fail "ui.enabled without hub.enabled was accepted (the UI proxies /api to a hub that is not there)"
render --set hub.enabled=false --set ui.enabled=false >/dev/null 2>&1 \
  && fail "a hub-less install writing to the IN-CHART ClickHouse was accepted"
render "${secondary[@]}" --set auth.ingest.mode=enforce >/dev/null 2>&1 \
  && fail "ingest enforce without hub.external.url was accepted (nothing to validate keys against)"
render "${secondary[@]}" --set auth.ingest.mode=enforce --set hub.external.url=https://obs.example.com >/dev/null 2>&1 \
  && fail "ingest enforce without an explicit internalToken was accepted (a generated one the central hub never saw)"
render "${secondary[@]}" --set collection.runtimeControl.enabled=true --set sensor.enabled=true >/dev/null 2>&1 \
  && fail "runtime collection control without a hub was accepted (RBAC for a controller that is not there)"
ok "five impossible combinations refused at template time"

# With a real hub URL and the central token, the gateway validates against the
# CENTRAL hub — the one thing that makes ingest keys work across clusters.
out="$(render "${secondary[@]}" --set auth.ingest.mode=enforce \
  --set hub.external.url=https://obs.example.com/ --set auth.ingest.internalToken=shared-token)"
grep -q "hub_validate_url: https://obs.example.com/internal/v1/ingest-keys/validate" <<<"$out" \
  || fail "the gateway does not validate ingest keys against hub.external.url"
ok "ingest keys validate against the central hub"

# An Ingress with no rules is rejected by the API server, so it must not render.
render "${secondary[@]}" --set ingress.enabled=true >/dev/null 2>&1 \
  && fail "ingress.enabled on a gateway-only install was accepted (an Ingress with no rules)"
out="$(render "${secondary[@]}" --set ingress.enabled=true --set gateway.sentry.enabled=true --set ingress.sentryHost=sentry.example.com)"
# grep -q ... && fail, never `grep -c | grep`: with pipefail a no-match grep
# exits 1 and takes the whole pipeline with it, which reads as a failure here.
grep -q "test-avuruobs-hub" <<<"$out" && fail "gateway-only ingress still routes to the hub"
ok "ingress: no rules means no Ingress; the Sentry host still works alone"

echo "== ai: born opt-off -> no surface, but redaction is NOT gated on it"
out="$(render)"
grep -q 'AVURUOBS_AI_CONFIG' <<<"$out" && fail "ai env rendered without opt-in"
grep -q 'ai.json' <<<"$out" && fail "ai ConfigMap rendered without opt-in"
grep -A1 'name: AVURUOBS_MODULES' <<<"$out" | grep -qE ',ai[,"]' && fail "ai in AVURUOBS_MODULES without opt-in"
# The whole point: content arrives whether or not this install runs the screen,
# so the protection cannot depend on the screen.
grep -q 'transform/genai:' <<<"$out" || fail "gen_ai content redaction missing with the ai module OFF"
ok "no env, ConfigMap or module entry by default — and redaction still on"

echo "== ai: module on -> prices ConfigMap, env, module entry"
out="$(render --set modules.ai.enabled=true --set ai.currency=USD)"
grep -q 'name: test-avuruobs-ai' <<<"$out" || fail "ai (prices) ConfigMap missing"
grep -q 'AVURUOBS_AI_CONFIG' <<<"$out" || fail "AVURUOBS_AI_CONFIG env missing"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting,ai' <<<"$out" \
  || fail "ai missing from AVURUOBS_MODULES"
ok "prices ConfigMap, env and module entry render together"

echo "== ai: content redaction runs first, on every pipeline, and can be refused"
out="$(render)"
# Before the tenant stamp and before batch, so it applies to EVERY exporter on
# the pipeline — forwarding targets included. Redacting locally and forwarding
# verbatim is the same exposure with an extra hop.
grep -qE 'processors: \[transform/genai, .*batch\]' <<<"$out" || fail "redaction is not the first processor"
# Anchored so a token COUNT under the older spelling is never deleted: it is
# the number the module exists to report.
grep -qF 'delete_matching_keys(span.attributes, "^gen_ai\\.(prompt|completion|input\\.messages|output\\.messages|system_instructions|content)")' <<<"$out" \
  || fail "the span content pattern is missing or has drifted from ai.go"
grep -q 'context: spanevent' <<<"$out" || fail "span events are not redacted (the earlier convention put content there)"
out="$(render --set gateway.genai.redactContent=false)"
grep -q 'transform/genai' <<<"$out" && fail "redactContent=false still rendered the stage"
ok "runs first on traces and logs; an operator can still refuse it"

echo "== mcp: born opt-off -> no module entry, no route"
out="$(render)"
grep -A1 'name: AVURUOBS_MODULES' <<<"$out" | grep -qE ',mcp[,"]' && fail "mcp in AVURUOBS_MODULES without opt-in"
ok "no module entry by default"

echo "== mcp: module on -> module entry, and still no new component"
out="$(render --set modules.mcp.enabled=true)"
grep -q 'core,logs,infra-metrics,profiling,error-tracking,service-health,alerting,mcp' <<<"$out" \
  || fail "mcp missing from AVURUOBS_MODULES"
# The whole claim of this feature: it is a handler on the hub, not a
# deployable. If turning it on ever renders a Deployment, the time-to-value
# gate is no longer measuring the same install.
before="$(render | grep -c '^kind: Deployment' || true)"
after="$(grep -c '^kind: Deployment' <<<"$out" || true)"
[ "$before" = "$after" ] || fail "mcp added a Deployment ($before -> $after)"
ok "module entry renders; component count unchanged"

echo "== mcp: the endpoint is actually routed"
# The regression this guards: /mcp is not under /api, so the hub-facing rules
# do not cover it and it fell through to the UI. The e2e suite could not catch
# it — compose publishes the hub directly, bypassing both the Ingress and the
# UI's nginx.
out="$(render --set ingress.enabled=true)"
grep -q 'path: /mcp' <<<"$out" && fail "/mcp routed without modules.mcp.enabled"
ok "no /mcp rule while the module is off"

out="$(render --set ingress.enabled=true --set modules.mcp.enabled=true)"
grep -q 'path: /mcp' <<<"$out" || fail "/mcp has no Ingress rule, so an agent gets the UI's 404"
# It must reach the HUB, and it must be matched before the catch-all that sends
# everything else to the UI.
grep -A4 'path: /mcp' <<<"$out" | grep -q -- '-hub' || fail "/mcp is routed somewhere other than the hub"
# Ingress rules only: the "- path:" list form. A bare "path: /" also appears in
# every httpGet probe, which is not what is being ordered here.
mcp_line="$(grep -n -- '- path: /mcp' <<<"$out" | head -1 | cut -d: -f1)"
ui_line="$(grep -n -- '- path: /$' <<<"$out" | head -1 | cut -d: -f1)"
[ -n "$ui_line" ] && [ "$mcp_line" -lt "$ui_line" ] \
  || fail "/mcp is ordered after the UI catch-all ($mcp_line vs $ui_line)"
ok "/mcp reaches the hub, ahead of the UI catch-all"

echo "== mcp oauth: off by default, and refused where it cannot work"
out="$(render --set modules.mcp.enabled=true)"
grep -q 'AVURUOBS_MCP_OAUTH_ENABLED' <<<"$out" && fail "oauth env rendered without opting in"
ok "no oauth surface with the module alone"

# Each impossible combination is refused at TEMPLATE time rather than producing
# an install that 404s or advertises a URL nobody can reach.
render --set modules.mcp.oauth.enabled=true >/dev/null 2>&1 \
  && fail "oauth without the mcp module rendered"
render --set modules.mcp.enabled=true --set modules.mcp.oauth.enabled=true >/dev/null 2>&1 \
  && fail "oauth without publicUrl rendered"
render --set modules.mcp.enabled=true --set modules.mcp.oauth.enabled=true \
  --set publicUrl=https://obs.example.com --set auth.enabled=false >/dev/null 2>&1 \
  && fail "oauth without auth rendered"
ok "three impossible combinations refused"

out="$(render --set modules.mcp.enabled=true --set modules.mcp.oauth.enabled=true --set publicUrl=https://obs.example.com)"
grep -q 'AVURUOBS_MCP_OAUTH_ENABLED' <<<"$out" || fail "oauth env missing when enabled"
grep -q 'value: "https://obs.example.com"' <<<"$out" || fail "AVURUOBS_PUBLIC_URL missing"
before="$(render | grep -c '^kind: Deployment' || true)"
after="$(grep -c '^kind: Deployment' <<<"$out" || true)"
[ "$before" = "$after" ] || fail "oauth added a Deployment ($before -> $after)"
ok "renders with a public URL; still no new component"

echo "== publicUrl: promoted out of the OIDC block, old key still honoured"
# It was only emitted inside oidcEnv, so an install without SSO never got it —
# yet the hub reads it for trusted origins regardless.
out="$(render --set publicUrl=https://obs.example.com)"
grep -q 'AVURUOBS_PUBLIC_URL' <<<"$out" || fail "publicUrl not emitted without SSO"
out="$(render --set auth.oidc.publicUrl=https://legacy.example.com)"
grep -q 'value: "https://legacy.example.com"' <<<"$out" || fail "auth.oidc.publicUrl no longer honoured"
ok "top-level key works without SSO; the old key still resolves"

echo "== mesh-config: the only cluster-wide grant, and only when asked for by name"
# The whole reason this is a separate module: an upgrade must never be the
# thing that starts reading a cluster.
out="$(render)"
grep -q 'mesh-config' <<<"$out" && fail "mesh-config resources rendered on a default install"
out="$(render --set modules.mesh.enabled=true)"
grep -q 'mesh-config' <<<"$out" && fail "the mesh module alone rendered a ClusterRole"
ok "no cluster-wide grant without the module"

render --set modules.meshConfig.enabled=true >/dev/null 2>&1 \
  && fail "meshConfig without mesh rendered"
ok "meshConfig without mesh is refused"

out="$(render --set modules.mesh.enabled=true --set modules.meshConfig.enabled=true)"
grep -q 'kind: ClusterRole' <<<"$out" || fail "no ClusterRole with the module on"
# Read-only, forever. A write verb here would be a different product.
awk '/name: test-avuruobs-mesh-config/,/^---/' <<<"$out" \
  | grep -E 'verbs:' | grep -qE '"(create|update|patch|delete|deletecollection)"' \
  && fail "the mesh-config ClusterRole carries a write verb"
ok "the grant is get/list/watch only"

# A pod has one identity: with collection control also on, both features must
# share its ServiceAccount rather than fight over serviceAccountName.
out="$(render --set modules.mesh.enabled=true --set modules.meshConfig.enabled=true \
  --set collection.runtimeControl.enabled=true --set auth.enabled=true)"
[ "$(grep -c 'serviceAccountName: test-avuruobs-collection-control' <<<"$out")" -ge 1 ] \
  || fail "the hub lost its collection-control identity"
awk '/kind: ClusterRoleBinding/,/^---/' <<<"$out" | grep -q 'name: test-avuruobs-collection-control' \
  || fail "the mesh-config binding does not target the SA the hub actually runs as"
ok "both features share one hub ServiceAccount"

echo "ALL TEMPLATE ASSERTIONS PASSED"
