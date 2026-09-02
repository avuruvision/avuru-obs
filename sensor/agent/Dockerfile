# Avuru node agent — minimal OTel Collector distro built with OCB from
# ocb-manifest.yaml. Build from the REPO ROOT:
#   docker build -t avuru-obs-agent -f sensor/agent/Dockerfile .
# The builder version matches the component line in the manifest; mixed lines
# are unsupported. This distro tracks CONTRIB (0.159.0), independently of the
# gateway's 0.154.0 — see the manifest header for why that is safe.
FROM golang:1.25 AS build
ARG OCB_VERSION=0.159.0
WORKDIR /src

RUN go install go.opentelemetry.io/collector/cmd/builder@v${OCB_VERSION}

COPY sensor/agent/ocb-manifest.yaml .
RUN CGO_ENABLED=0 builder --config ocb-manifest.yaml

# NOT the :nonroot variant. The agent tails /var/log/pods, whose files are
# root-owned and mode 0600 on every runtime we support, so the DaemonSet runs
# it with runAsUser: 0 (sensor-daemonset.yaml) — an image that forced a
# non-root uid would read nothing and say only "permission denied".
FROM gcr.io/distroless/static-debian12
COPY --from=build /src/build/avuru-agent /avuru-agent
# health_check
EXPOSE 13134
ENTRYPOINT ["/avuru-agent"]
