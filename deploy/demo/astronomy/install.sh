#!/usr/bin/env bash
# Install the OpenTelemetry Astronomy Shop, wired to avuru obs over OTLP.
#
# This is the multi-service app behind the public Live Demo. It exercises the
# STABLE OTLP ingestion path (not the eBPF sensor): every shop service exports
# traces, metrics and logs straight to the avuru obs gateway.
#
# Prereq: avuru obs already installed (Helm release `avuruops`) in $NS. Install
# the demo into the SAME namespace so `avuruops-gateway` resolves.
set -euo pipefail

NS="${NS:-avuruops}"
RELEASE="${RELEASE:-astronomy}"
# Pins appVersion 2.2.0 — the version this overlay was validated against.
CHART_VERSION="${CHART_VERSION:-0.40.10}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

helm repo add open-telemetry \
  https://open-telemetry.github.io/opentelemetry-helm-charts >/dev/null 2>&1 || true
helm repo update open-telemetry >/dev/null

echo "Installing Astronomy Shop (release=$RELEASE) into namespace '$NS',"
echo "exporting all signals to avuruops-gateway over OTLP…"
helm upgrade --install "$RELEASE" open-telemetry/opentelemetry-demo \
  --version "$CHART_VERSION" \
  -n "$NS" --create-namespace \
  -f "$HERE/values-avuru.yaml"

cat <<EOF

Done. The shop's built-in load generator drives traffic automatically.
Open the avuru obs UI and watch the service map fill in — over OTLP, no eBPF.

Tear down with:  helm uninstall $RELEASE -n $NS
EOF
