{{/* Expand the name of the chart. */}}
{{- define "avuruops.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Fully qualified app name. */}}
{{- define "avuruops.fullname" -}}
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
{{- define "avuruops.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "avuruops.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "avuruops.selectorLabels" -}}
app.kubernetes.io/name: {{ include "avuruops.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Image reference: prefixes the global registry when set. Call with a dict
     {registry, repo, tag}. */}}
{{- define "avuruops.image" -}}
{{- if .registry -}}{{ .registry }}/{{ end -}}{{ .repo }}:{{ .tag }}
{{- end -}}

{{/* ClickHouse native address (in-chart Service or external). */}}
{{- define "avuruops.clickhouseAddr" -}}
{{- if .Values.clickhouse.external.enabled -}}
{{- .Values.clickhouse.external.address -}}
{{- else -}}
{{- printf "%s-clickhouse:9000" (include "avuruops.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* ClickHouse database name. */}}
{{- define "avuruops.clickhouseDatabase" -}}
{{- if .Values.clickhouse.external.enabled -}}
{{- default "otel" .Values.clickhouse.external.database -}}
{{- else -}}
otel
{{- end -}}
{{- end -}}

{{/* ClickHouse username. */}}
{{- define "avuruops.clickhouseUser" -}}
{{- if .Values.clickhouse.external.enabled -}}
{{- default "avuru" .Values.clickhouse.external.username -}}
{{- else -}}
{{- .Values.clickhouse.auth.username -}}
{{- end -}}
{{- end -}}

{{/* Name of the Secret holding the ClickHouse password, and whether it is
     chart-managed. External existingSecret > in-chart existingSecret >
     chart-created Secret. */}}
{{- define "avuruops.clickhouseSecretName" -}}
{{- if and .Values.clickhouse.external.enabled .Values.clickhouse.external.existingSecret -}}
{{- .Values.clickhouse.external.existingSecret -}}
{{- else if .Values.clickhouse.auth.existingSecret -}}
{{- .Values.clickhouse.auth.existingSecret -}}
{{- else -}}
{{- printf "%s-clickhouse" (include "avuruops.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* Active module set, as the CSV the hub reads (AVURUOPS_MODULES). `core` is
     always present — it is the wedge and has no switch. */}}
{{- define "avuruops.activeModules" -}}
{{- $mods := list "core" -}}
{{- if .Values.modules.logs.enabled -}}{{- $mods = append $mods "logs" -}}{{- end -}}
{{- if .Values.modules.infraMetrics.enabled -}}{{- $mods = append $mods "infra-metrics" -}}{{- end -}}
{{- if .Values.modules.profiling.enabled -}}{{- $mods = append $mods "profiling" -}}{{- end -}}
{{- if .Values.modules.errorTracking.enabled -}}{{- $mods = append $mods "error-tracking" -}}{{- end -}}
{{- join "," $mods -}}
{{- end -}}

{{/* Module env block shared by the hub Deployment and the migrate Job. */}}
{{- define "avuruops.modulesEnv" -}}
- name: AVURUOPS_MODULES
  value: {{ include "avuruops.activeModules" . | quote }}
{{- end -}}

{{/* Effective collection switches: a module gates its own collection, so a
     disabled module never collects regardless of the sensor knob. These emit
     "true" or the empty string — a rendered "false" would be a non-empty
     string, and therefore TRUTHY in `if`. Always consume them via
     `if include "..." .`, never compare to "false". */}}
{{- define "avuruops.collectLogs" -}}
{{- if and .Values.modules.logs.enabled .Values.sensor.agent.logs.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruops.collectInfraMetrics" -}}
{{- if and .Values.modules.infraMetrics.enabled .Values.sensor.agent.kubeletstats.enabled -}}true{{- end -}}
{{- end -}}

{{- define "avuruops.collectProfiles" -}}
{{- if and .Values.modules.profiling.enabled .Values.sensor.profiler.enabled -}}true{{- end -}}
{{- end -}}

{{/* Sentry SDK ingest is live only when the flag AND both modules it leans on
     are on: events land as log records (logs) that the hub derives issues from
     (error-tracking). Gates the receiver, the container port, the Service port
     and the ingress route, so they can never drift apart. Same true/"" contract
     as the collect* helpers above. */}}
{{- define "avuruops.sentryEnabled" -}}
{{- if and .Values.modules.errorTracking.enabled .Values.modules.logs.enabled .Values.gateway.sentry.enabled -}}true{{- end -}}
{{- end -}}

{{/* ClickHouse env block shared by the hub Deployment and the migrate Job. */}}
{{- define "avuruops.clickhouseEnv" -}}
- name: AVURUOPS_CLICKHOUSE_ADDR
  value: {{ include "avuruops.clickhouseAddr" . | quote }}
- name: AVURUOPS_CLICKHOUSE_DATABASE
  value: {{ include "avuruops.clickhouseDatabase" . | quote }}
- name: AVURUOPS_CLICKHOUSE_USER
  value: {{ include "avuruops.clickhouseUser" . | quote }}
- name: AVURUOPS_CLICKHOUSE_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "avuruops.clickhouseSecretName" . }}
      key: clickhouse-password
{{- end -}}
