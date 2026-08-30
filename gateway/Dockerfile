# Avuru gateway — minimal OTel Collector distro built with OCB from
# ocb-manifest.yaml. Build from the REPO ROOT:
#   docker build -t avuru-obs-gateway -f gateway/Dockerfile .
FROM golang:1.27 AS build
ARG OCB_VERSION=0.154.0
WORKDIR /src

# The builder version matches otelcol_version — mixed lines are unsupported.
RUN go install go.opentelemetry.io/collector/cmd/builder@v${OCB_VERSION}

COPY gateway/ocb-manifest.yaml .
# The Sentry receiver is an in-repo module; the manifest's replaces resolves
# ../sentryreceiver relative to the generated ./build/go.mod (i.e. /src/sentryreceiver).
COPY gateway/sentryreceiver ./sentryreceiver
# Ingest-key authenticator + tenant-stamping processor — same in-repo pattern
# (../avuruingestauth, ../tenantfromauth relative to the generated build/go.mod).
COPY gateway/avuruingestauth ./avuruingestauth
COPY gateway/tenantfromauth ./tenantfromauth
RUN CGO_ENABLED=0 builder --config ocb-manifest.yaml

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /src/build/avuru-gateway /avuru-gateway
# OTLP gRPC / OTLP HTTP / Sentry ingest / healthcheck
EXPOSE 4317 4318 4319 13133
ENTRYPOINT ["/avuru-gateway"]
