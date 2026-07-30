#!/usr/bin/env sh
# Avuru Obs installer — installs the released Helm chart into your CURRENT
# kubectl context. It talks only to helm and kubectl against the cluster you
# have already selected: no sudo, no changes to your machine.
#
#   curl -fsSL https://raw.githubusercontent.com/avuruvision/avuru-obs/main/deploy/install.sh | sh
#
# Prefer to read before you run (always sound advice for a piped script):
#   curl -fsSLO https://raw.githubusercontent.com/avuruvision/avuru-obs/main/deploy/install.sh
#   less install.sh && sh install.sh --dry-run   # prints the exact helm command
#
# Flags:
#   --version X.Y.Z   chart version (default: the latest GitHub release)
#   --namespace NS    target namespace (default: avuruobs)
#   --values FILE     extra -f values file (repeatable)
#   --set K=V         extra --set override (repeatable)
#   --dry-run         print what would run, change nothing
#   --open            after install, port-forward and print the UI URL
#   --yes             do not pause for confirmation
#   --help
#
# POSIX sh on purpose — it runs under whatever /bin/sh a piped shell provides.
set -eu

REPO="avuruvision/avuru-obs"
CHART="oci://ghcr.io/avuruvision/charts/avuruobs"
RELEASE="avuruobs"

VERSION="${AVURUOBS_VERSION:-}"
NAMESPACE="avuruobs"
DRY_RUN=0
OPEN=0
ASSUME_YES=0
EXTRA=""

log()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mwarning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version)   VERSION="${2:?--version needs a value}"; shift 2 ;;
    --namespace) NAMESPACE="${2:?--namespace needs a value}"; shift 2 ;;
    --values)    EXTRA="$EXTRA -f ${2:?--values needs a file}"; shift 2 ;;
    --set)       EXTRA="$EXTRA --set ${2:?--set needs K=V}"; shift 2 ;;
    --dry-run)   DRY_RUN=1; shift ;;
    --open)      OPEN=1; shift ;;
    --yes|-y)    ASSUME_YES=1; shift ;;
    --help|-h)   awk 'NR>1 && /^#/ {sub(/^# ?/,""); print; next} NR>1 {exit}' "$0"; exit 0 ;;
    *)           die "unknown flag: $1 (see --help)" ;;
  esac
done

# Resolve the chart version: flag/env, else the latest GitHub release.
if [ -z "$VERSION" ]; then
  log "resolving the latest release from github.com/$REPO"
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name": *"v\{0,1\}\([^"]*\)".*/\1/p' | head -n1)" || true
  [ -n "$VERSION" ] || die "could not resolve the latest release — pass --version X.Y.Z (chart SemVer, no leading v)"
fi
VERSION="${VERSION#v}" # OCI chart tags are bare SemVer

CTX="$(kubectl config current-context 2>/dev/null || echo '?')"

# Build the command once so --dry-run shows exactly what runs.
set -- helm upgrade --install "$RELEASE" "$CHART" --version "$VERSION" \
  --namespace "$NAMESPACE" --create-namespace --wait --timeout 6m
# shellcheck disable=SC2086 # EXTRA is intentionally word-split into args
set -- "$@" $EXTRA

log "context : $CTX"
log "namespace: $NAMESPACE"
log "chart    : $CHART:$VERSION"

if [ "$DRY_RUN" -eq 1 ]; then
  log "dry run — would execute (nothing has run):"
  printf '    %s\n' "$*"
  exit 0
fi

# --- preflight: warn, never block (the chart itself degrades gracefully) ------
command -v helm >/dev/null 2>&1    || die "helm not found — install Helm 3.8+ (OCI support): https://helm.sh/docs/intro/install/"
command -v kubectl >/dev/null 2>&1 || die "kubectl not found — https://kubernetes.io/docs/tasks/tools/"

if ! kubectl cluster-info >/dev/null 2>&1; then
  die "no reachable cluster in the current kubectl context — select one, or spin up kind/minikube, then re-run"
fi

if ! kubectl get storageclass 2>/dev/null | grep -q '(default)'; then
  warn "no default StorageClass — ClickHouse's PVC will stay Pending. Set one, use a cluster that has one, or pass --set clickhouse.persistence.enabled=false for an ephemeral trial."
fi

# curl|sh has no TTY to prompt on; only pause when a terminal is attached.
if [ "$ASSUME_YES" -eq 0 ] && [ -t 0 ]; then
  printf 'Install avuru-obs %s into context "%s"? [y/N] ' "$VERSION" "$CTX"
  read -r reply
  case "$reply" in y|Y|yes|YES) ;; *) die "aborted" ;; esac
fi

log "installing (this waits for rollout)…"
"$@"

log "installed."
if [ "$OPEN" -eq 1 ]; then
  log "forwarding the UI to http://localhost:8080/ (Ctrl-C to stop)"
  exec kubectl -n "$NAMESPACE" port-forward "svc/${RELEASE}-ui" 8080:80
else
  cat <<EOF

Open the UI:
  kubectl -n $NAMESPACE port-forward svc/${RELEASE}-ui 8080:80
  # then browse http://localhost:8080/

Point your OTLP exporters at the gateway (the only app-side change):
  ${RELEASE}-gateway.$NAMESPACE.svc:4318   # HTTP   (4317 for gRPC)
EOF
fi
