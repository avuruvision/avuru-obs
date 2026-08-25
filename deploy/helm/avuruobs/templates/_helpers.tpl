{{/* Expand the name of the chart. */}}
{{- define "avuruobs.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified app name. */}}
{{- define "avuruobs.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Common labels. */}}
{{- define "avuruobs.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "avuruobs.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "avuruobs.selectorLabels" -}}
app.kubernetes.io/name: {{ include "avuruobs.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Image reference: prefixes the global registry when set. Call with a dict
     {registry, repo, tag}. */}}
{{- define "avuruobs.image" -}}
{{- if .registry -}}{{ .registry }}/{{ end -}}{{ .repo }}:{{ .tag }}
{{- end -}}

{{/* ClickHouse native address (in-chart Service or external). */}}
{{- define "avuruobs.clickhouseAddr" -}}
{{- if .Values.clickhouse.external.enabled -}}
{{- .Values.clickhouse.external.address -}}
{{- else -}}
{{- printf "%s-clickhouse:9000" (include "avuruobs.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* ClickHouse database name. */}}
{{- define "avuruobs.clickhouseDatabase" -}}
{{- if .Values.clickhouse.external.enabled -}}
{{- default "otel" .Values.clickhouse.external.database -}}
{{- else -}}
otel
{{- end -}}
{{- end -}}

{{/* ClickHouse username. */}}
{{- define "avuruobs.clickhouseUser" -}}
{{- if .Values.clickhouse.external.enabled -}}
{{- default "avuru" .Values.clickhouse.external.username -}}
{{- else -}}
{{- .Values.clickhouse.auth.username -}}
{{- end -}}
{{- end -}}

{{/* Name of the Secret holding the ClickHouse password, and whether it is
     chart-managed. External existingSecret > in-chart existingSecret >
     chart-created Secret. */}}
{{- define "avuruobs.clickhouseSecretName" -}}
{{- if and .Values.clickhouse.external.enabled .Values.clickhouse.external.existingSecret -}}
{{- .Values.clickhouse.external.existingSecret -}}
{{- else if .Values.clickhouse.auth.existingSecret -}}
{{- .Values.clickhouse.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-clickhouse" (include "avuruobs.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* Active module set, as the CSV the hub reads (AVURUOBS_MODULES). `core` is
     always present — it is the wedge and has no switch. */}}
{{- define "avuruobs.activeModules" -}}
{{- $mods := list "core" -}}
{{- if .Values.modules.logs.enabled -}}{{- $mods = append $mods "logs" -}}{{- end -}}
{{- if .Values.modules.infraMetrics.enabled -}}{{- $mods = append $mods "infra-metrics" -}}{{- end -}}
{{- if .Values.modules.profiling.enabled -}}{{- $mods = append $mods "profiling" -}}{{- end -}}
{{- if .Values.modules.errorTracking.enabled -}}{{- $mods = append $mods "error-tracking" -}}{{- end -}}
{{- if .Values.modules.serviceHealth.enabled -}}{{- $mods = append $mods "service-health" -}}{{- end -}}
{{- if .Values.modules.alerting.enabled -}}{{- $mods = append $mods "alerting" -}}{{- end -}}
{{- if .Values.modules.green.enabled -}}{{- $mods = append $mods "green" -}}{{- end -}}
{{- join "," $mods -}}
{{- end -}}

{{/* True when OBI's TCP-stats metrics are actually collected.

     They attach a tracepoint (sock/inet_sock_set_state), which the kernel only
     exposes through debugfs or tracefs — neither of which is in a container
     unless it is mounted in. Without it OBI does not degrade: it exits, taking
     traces and network flows down with an optional signal, and the DaemonSet
     crash-loops. So the mounts and this switch have to move together, which is
     what this helper is for. */}}
{{- define "avuruobs.obiStatsEnabled" -}}
{{- if and .Values.sensor.enabled .Values.sensor.obi.enabled .Values.sensor.obi.network.enabled .Values.sensor.obi.network.stats -}}true{{- end -}}
{{- end -}}

{{/* True when anything in the sensor pod needs the kernel tracing filesystems:
     the eBPF profiler, or OBI's tracepoint-based TCP stats. The volumes are
     declared once for the pod, so this has to be the union. */}}
{{- define "avuruobs.needsKernelTracefs" -}}
{{- if or (include "avuruobs.collectProfiles" .) (include "avuruobs.obiStatsEnabled" .) -}}true{{- end -}}
{{- end -}}

{{/* Module env block shared by the hub Deployment and the migrate Job. */}}
{{- define "avuruobs.modulesEnv" -}}
- name: AVURUOBS_MODULES
  value: {{ include "avuruobs.activeModules" . | quote }}
{{- end -}}

{{/* Where the gateway validates ingest keys. In a full install that is the hub
     Service in this namespace; on a gateway-only (secondary cluster) install
     the hub lives elsewhere and hub.external.url says where — component-toggles
     .yaml refuses the combination without it, so this never renders an empty
     host. */}}
{{- define "avuruobs.hubValidateUrl" -}}
{{- if .Values.hub.enabled -}}
http://{{ include "avuruobs.fullname" . }}-hub:80/internal/v1/ingest-keys/validate
{{- else -}}
{{ trimSuffix "/" .Values.hub.external.url }}/internal/v1/ingest-keys/validate
{{- end -}}
{{- end -}}

{{/* Global retention, in days per signal. BOTH the migrator and the hub need
     it: the migrator turns it into table TTLs, and the hub reports it (Settings
     → Storage compares configured against enforced) and uses it as the ceiling
     for a per-project window. When only the Job carried it, a hub on chart
     retention values fell back to its built-in defaults and reported drift that
     was not there. */}}
{{- define "avuruobs.retentionEnv" -}}
- name: AVURUOBS_RETENTION_TRACES_DAYS
  value: {{ .Values.retention.traces | quote }}
- name: AVURUOBS_RETENTION_LOGS_DAYS
  value: {{ .Values.retention.logs | quote }}
- name: AVURUOBS_RETENTION_METRICS_DAYS
  value: {{ .Values.retention.metrics | quote }}
- name: AVURUOBS_RETENTION_PROFILES_DAYS
  value: {{ .Values.retention.profiles | quote }}
- name: AVURUOBS_RETENTION_ERRORS_DAYS
  value: {{ .Values.retention.errors | quote }}
{{- end -}}

{{/* Service-health groups config: env pointing the hub at the mounted
     ConfigMap file, plus the volume and mount. Emitted only when the module is
     on, so a disabled install carries no config surface. The hub hot-reloads
     the file, so a `kubectl edit` of the ConfigMap applies without a restart. */}}
{{- define "avuruobs.groupsEnv" -}}
{{- if .Values.modules.serviceHealth.enabled }}
- name: AVURUOBS_GROUPS_CONFIG
  value: /etc/avuruobs/groups.json
{{- end }}
{{- end -}}

{{- define "avuruobs.groupsVolume" -}}
{{- if .Values.modules.serviceHealth.enabled }}
- name: service-groups
  configMap:
    name: {{ include "avuruobs.fullname" . }}-groups
{{- end }}
{{- end -}}

{{- define "avuruobs.groupsVolumeMount" -}}
{{- if .Values.modules.serviceHealth.enabled }}
- name: service-groups
  mountPath: /etc/avuruobs
  readOnly: true
{{- end }}
{{- end -}}

{{/* Service-map topology config: env + volume + mount, in its OWN dir so it
     never collides with the other mounted configs. Says which workload names
     are mesh/gateway TRANSPORT rather than applications, so the map stops
     drawing `app → proxy → app` hops as application dependencies.

     Ungated, unlike the module configs: the service map is core and cannot be
     turned off, so the knob that corrects it has to exist on every install.
     The default value is empty — the hub's built-in patterns apply — and an
     operator who needs to correct a misclassification can `kubectl edit` the
     ConfigMap and have it live within ~15s, without a helm upgrade. */}}
{{- define "avuruobs.topologyEnv" -}}
- name: AVURUOBS_TOPOLOGY_CONFIG
  value: /etc/avuruobs-topology/topology.json
{{- end -}}

{{- define "avuruobs.topologyVolume" -}}
- name: topology-config
  configMap:
    name: {{ include "avuruobs.fullname" . }}-topology
{{- end -}}

{{- define "avuruobs.topologyVolumeMount" -}}
- name: topology-config
  mountPath: /etc/avuruobs-topology
  readOnly: true
{{- end -}}

{{/* Alerting rules config: env + volume + mount, mounted at its OWN dir so it
     never collides with the service-groups mount. AVURUOBS_WEBHOOK_ALLOW carries
     the SSRF override CIDRs. Emitted only when the module is on. */}}
{{- define "avuruobs.alertsEnv" -}}
{{- if .Values.modules.alerting.enabled }}
- name: AVURUOBS_ALERTS_CONFIG
  value: /etc/avuruobs-alerts/alerts.json
{{- if .Values.alerting.webhookAllow }}
- name: AVURUOBS_WEBHOOK_ALLOW
  value: {{ join "," .Values.alerting.webhookAllow | quote }}
{{- end }}
{{- end }}
{{- end -}}

{{- define "avuruobs.alertsVolume" -}}
{{- if .Values.modules.alerting.enabled }}
- name: alert-rules
  configMap:
    name: {{ include "avuruobs.fullname" . }}-alerts
{{- end }}
{{- end -}}

{{- define "avuruobs.alertsVolumeMount" -}}
{{- if .Values.modules.alerting.enabled }}
- name: alert-rules
  mountPath: /etc/avuruobs-alerts
  readOnly: true
{{- end }}
{{- end -}}

{{/* Green energy/carbon config: env + volume + mount, in its OWN dir so it
     never collides with the service-groups or alerts mounts. Carries the carbon
     factors, metric-name overrides and budgets the hub reads (hot-reloaded).
     Emitted only when the green module is on. */}}
{{- define "avuruobs.greenEnv" -}}
{{- if .Values.modules.green.enabled }}
- name: AVURUOBS_GREEN_CONFIG
  value: /etc/avuruobs-green/green.json
{{- end }}
{{- end -}}

{{- define "avuruobs.greenVolume" -}}
{{- if .Values.modules.green.enabled }}
- name: green-config
  configMap:
    name: {{ include "avuruobs.fullname" . }}-green
{{- end }}
{{- end -}}

{{- define "avuruobs.greenVolumeMount" -}}
{{- if .Values.modules.green.enabled }}
- name: green-config
  mountPath: /etc/avuruobs-green
  readOnly: true
{{- end }}
{{- end -}}

{{/* OIDC SSO is live only when auth itself is on AND the oidc block opts in
     (a guard in hub-oidc-configmap.yaml fails the contradictory combination).
     Same true/"" contract as the collect* helpers — consume via `if include`,
     never compare to "false". */}}
{{- define "avuruobs.oidcEnabled" -}}
{{- if and .Values.auth.enabled .Values.auth.oidc.enabled -}}true{{- end -}}
{{- end -}}

{{/* OIDC config: env + volume + mount, in its OWN dir so it never collides
     with the groups/alerts/green mounts. The config file is hot-reloaded by
     the hub; the client secret arrives via env from a Secret (existingSecret >
     chart-created — never in the config file); AVURUOBS_PUBLIC_URL builds the
     absolute redirect_uri the IdP requires. With neither clientSecret nor
     existingSecret set the secret env is omitted (public client attempt)
     rather than referencing a Secret that does not exist. Emitted only when
     SSO is on. */}}
{{- define "avuruobs.oidcEnv" -}}
{{- if include "avuruobs.oidcEnabled" . }}
- name: AVURUOBS_AUTH_OIDC_CONFIG
  value: /etc/avuruobs-oidc/oidc.yaml
{{- if or .Values.auth.oidc.clientSecret .Values.auth.oidc.existingSecret }}
- name: AVURUOBS_AUTH_OIDC_CLIENT_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ .Values.auth.oidc.existingSecret | default (printf "%s-oidc" (include "avuruobs.fullname" .)) }}
      key: oidc-client-secret
{{- end }}
{{- if .Values.auth.oidc.publicUrl }}
- name: AVURUOBS_PUBLIC_URL
  value: {{ .Values.auth.oidc.publicUrl | quote }}
{{- end }}
{{- end }}
{{- end -}}

{{- define "avuruobs.oidcVolume" -}}
{{- if include "avuruobs.oidcEnabled" . }}
- name: oidc-config
  configMap:
    name: {{ include "avuruobs.fullname" . }}-oidc
{{- end }}
{{- end -}}

{{- define "avuruobs.oidcVolumeMount" -}}
{{- if include "avuruobs.oidcEnabled" . }}
- name: oidc-config
  mountPath: /etc/avuruobs-oidc
  readOnly: true
{{- end }}
{{- end -}}

{{/* Effective collection switches: a module gates its own collection, so a
     disabled module never collects regardless of the sensor knob. These emit
     "true" or the empty string — a rendered "false" would be a non-empty
     string, and therefore TRUTHY in `if`. Always consume them via
     `if include "..." .`, never compare to "false". */}}
{{- define "avuruobs.collectLogs" -}}
{{- if and .Values.modules.logs.enabled .Values.sensor.agent.logs.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruobs.collectInfraMetrics" -}}
{{- if and .Values.modules.infraMetrics.enabled .Values.sensor.agent.kubeletstats.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruobs.collectProfiles" -}}
{{- if and .Values.modules.profiling.enabled .Values.sensor.profiler.enabled -}}true{{- end -}}
{{- end -}}

{{/* Green energy collection is live only when BOTH the green module and the
     sensor's green surface are on — the umbrella gate for the receiver, the
     pipeline and the host mounts, whichever energy source fills them. Same
     true/"" contract as the sibling collect* helpers — consume via `if
     include`, never compare to "false". The pod→workload join it feeds also
     needs infra-metrics, but that is a hard dependency enforced by a
     {{ fail }} guard, not folded in here. */}}
{{- define "avuruobs.collectGreen" -}}
{{- if and .Values.modules.green.enabled .Values.sensor.green.enabled -}}true{{- end -}}
{{- end -}}

{{/* Whether Kepler itself — the MEASURED source — renders: green collection
     active AND the kepler sub-flag (default true). Split out from collectGreen
     because Kepler v0.11.4 EXITS when RAPL zone discovery comes up empty
     ("no RAPL zones found"), so on a node without powercap the container
     crash-loops and holds the whole sensor pod out of Ready — no probe config
     can rescue a process that terminates itself. sensor.green.kepler.enabled
     =false drops it and leaves the estimator collecting. */}}
{{- define "avuruobs.collectGreenMeasured" -}}
{{- if and (include "avuruobs.collectGreen" .) .Values.sensor.green.kepler.enabled -}}true{{- end -}}
{{- end -}}

{{/* Whether the tdp-estimator container/scrape should render: requires green
     collection active AND the estimation sub-flag. Same true/"" (never
     "false") convention as the other avuruobs.collect* gates — always
     consumed via `if include "..."`, never string-compared. See
     design/2026-07-28-green-tdp-estimation.md. */}}
{{- define "avuruobs.collectGreenEstimation" -}}
{{- if and (include "avuruobs.collectGreen" .) .Values.sensor.green.estimation.enabled -}}true{{- end -}}
{{- end -}}

{{/* Runtime collection control placeholder gate: when the hub may toggle
     signals at runtime, EVERY sensor ConfigMap must already exist. Kubernetes
     RBAC cannot restrict `create` by resourceName, so the applier's Role is
     create-free by construction (see collection-rbac.yaml) and can only
     `update` objects the chart already rendered. Signals that are off
     therefore render as placeholder ConfigMaps the applier fills in place.
     Same true/"" contract as the collect* helpers — consume via `if include`,
     never compare to "false". */}}
{{- define "avuruobs.collectionPlaceholders" -}}
{{- if and .Values.collection.runtimeControl.enabled .Values.sensor.enabled -}}true{{- end -}}
{{- end -}}

{{/* Sentry SDK ingest is live only when the flag AND both modules it leans on
     are on: events land as log records (logs) that the hub derives issues from
     (error-tracking). Gates the receiver, the container port, the Service port
     and the ingress route, so they can never drift apart. Same true/"" contract
     as the collect* helpers above. */}}
{{- define "avuruobs.sentryEnabled" -}}
{{- if and .Values.modules.errorTracking.enabled .Values.modules.logs.enabled .Values.gateway.sentry.enabled -}}true{{- end -}}
{{- end -}}

{{/* Wider-ingest receivers (design/2026-07-27-wider-ingest-compat.md). Each
     helper gates the receiver, the container port, the Service port and the
     pipeline entry together (sentryEnabled precedent). Jaeger and Zipkin feed
     the always-present traces pipeline, so their flags stand alone; the
     Prometheus remote-write and Loki receivers are silently and-gated on the
     module whose pipeline (and tables) they need, exactly like sentry. Same
     true/"" contract as the collect* helpers — consume via `if include`. */}}
{{- define "avuruobs.jaegerEnabled" -}}
{{- if .Values.gateway.receivers.jaeger.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruobs.zipkinEnabled" -}}
{{- if .Values.gateway.receivers.zipkin.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruobs.promRWEnabled" -}}
{{- if and .Values.modules.infraMetrics.enabled .Values.gateway.receivers.prometheusRemoteWrite.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruobs.lokiEnabled" -}}
{{- if and .Values.modules.logs.enabled .Values.gateway.receivers.loki.enabled -}}true{{- end -}}
{{- end -}}

{{/* ClickHouse env block shared by the hub Deployment and the migrate Job. */}}
{{- define "avuruobs.clickhouseEnv" -}}
- name: AVURUOBS_CLICKHOUSE_ADDR
  value: {{ include "avuruobs.clickhouseAddr" . | quote }}
- name: AVURUOBS_CLICKHOUSE_DATABASE
  value: {{ include "avuruobs.clickhouseDatabase" . | quote }}
- name: AVURUOBS_CLICKHOUSE_USER
  value: {{ include "avuruobs.clickhouseUser" . | quote }}
- name: AVURUOBS_CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "avuruobs.clickhouseSecretName" . }}
      key: clickhouse-password
{{- end -}}

{{/* Name of the Secret holding ingest-key material (internal token, the
     provisioned sensor key, and its hub seed record). */}}
{{- define "avuruobs.ingestSecretName" -}}
{{- printf "%s-ingest" (include "avuruobs.fullname" .) -}}
{{- end -}}

{{/* Project the chart-provisioned sensor key belongs to. Follows gateway.tenant
     so a single-project install needs no extra configuration — the sensor's key
     lands in the same project its telemetry is already stamped with. */}}
{{- define "avuruobs.ingestSensorProject" -}}
{{- .Values.auth.ingest.sensorKeyProject | default .Values.gateway.tenant | default "default" -}}
{{- end -}}

{{/* The ingest-key AUTHENTICATOR is wired for log and enforce (log validates and
     counts would-be denials without rejecting). Same true/"" contract as the
     collect* helpers — consume via `if include`, never compare to "false". */}}
{{- define "avuruobs.ingestAuthEnabled" -}}
{{- if ne .Values.auth.ingest.mode "off" -}}true{{- end -}}
{{- end -}}

{{/* The tenant-STAMPING processor is wired for enforce only. In log mode the
     extension never attaches a validated project, so tenantfromauth would be a
     no-op hop on every record — and leaving it out keeps the log-mode pipeline
     byte-identical to a pre-ingest-keys install, which is exactly the drop-in
     guarantee the default is there to protect. */}}
{{- define "avuruobs.ingestTenantStamp" -}}
{{- if eq .Values.auth.ingest.mode "enforce" -}}true{{- end -}}
{{- end -}}
