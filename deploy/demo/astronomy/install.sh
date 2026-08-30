#!/usr/bin/env bash
# Install the OpenTelemetry Astronomy Shop, wired to Avuru Obs over OTLP.
#
# This is the multi-service app behind the public Live Demo. It exercises the
# STABLE OTLP ingestion path (not the eBPF sensor): every shop service exports
# traces, metrics and logs straight to the Avuru Obs gateway.
#
# Prereq: Avuru Obs already installed (Helm release `avuruobs`) in $NS. Install
# the demo into the SAME namespace so `avuruobs-gateway` resolves.
set -euo pipefail

NS="${NS:-avuruobs}"
RELEASE="${RELEASE:-astronomy}"
# Pins appVersion 2.2.0 — the version this overlay was validated against.
CHART_VERSION="${CHART_VERSION:-0.40.10}"
# Extra `helm` args appended verbatim. Use for CI/LAN overrides without forking
# this script — e.g. a Harbor image mirror or a cross-namespace gateway FQDN:
#   EXTRA_HELM_ARGS="--set default.image.repository=harbor.devops.lab/avuru/otel-demo"
#   EXTRA_HELM_ARGS="--set default.envOverrides[0].value=avuruobs-gateway.avuru-obs.svc.cluster.local"
EXTRA_HELM_ARGS="${EXTRA_HELM_ARGS:-}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

helm repo add open-telemetry \
  https://open-telemetry.github.io/opentelemetry-helm-charts >/dev/null 2>&1 || true
helm repo update open-telemetry >/dev/null

echo "Installing Astronomy Shop (release=$RELEASE) into namespace '$NS',"
echo "exporting all signals to avuruobs-gateway over OTLP…"
helm upgrade --install "$RELEASE" open-telemetry/opentelemetry-demo \
  --version "$CHART_VERSION" \
  -n "$NS" --create-namespace \
  -f "$HERE/values-avuru.yaml" \
  ${EXTRA_HELM_ARGS}

cat <<EOF

Done. The shop's built-in load generator drives traffic automatically.
Open the Avuru Obs UI and watch the service map fill in — over OTLP, no eBPF.

Tear down with:  helm uninstall $RELEASE -n $NS
EOF
