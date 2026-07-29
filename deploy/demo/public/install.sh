#!/usr/bin/env bash
# Install the public Live Demo: avuru obs (anonymous read-only on the `demo`
# project) + the Astronomy Shop feeding it over OTLP.
#
# Prereqs (see docs/runbooks/public-demo.md): a cluster with an ingress
# controller + TLS, DNS for demo.avuruobs.io, and the admin Secret created:
#   kubectl -n "$NS" create secret generic avuruops-admin \
#     --from-literal=admin-password="$(openssl rand -base64 24)"
set -euo pipefail

NS="${NS:-demo-obs}"
# Pin the released chart version being demoed (never a floating tag).
VERSION="${VERSION:?set VERSION to a released chart version, e.g. VERSION=0.2.1}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Installing avuru obs $VERSION into namespace '$NS' (public-demo values)…"
helm upgrade --install avuruops oci://ghcr.io/avuruvision/charts/avuruops \
  --version "$VERSION" \
  -n "$NS" --create-namespace \
  -f "$HERE/values-public-demo.yaml"

echo "Installing the Astronomy Shop into '$NS'…"
NS="$NS" "$HERE/../astronomy/install.sh"

cat <<EOF

Done. Wait for rollout, then run the hardening checks in
docs/runbooks/public-demo.md before pointing DNS at the ingress.
EOF
