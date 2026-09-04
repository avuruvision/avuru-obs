# Avuru gateway — minimal OTel Collector distro built with OCB from
# ocb-manifest.yaml. Build from the REPO ROOT:
#   docker build -t avuru-obs-gateway -f gateway/Dockerfile .
# Go stays on the line the compiled-in modules declare. sentryreceiver,
# avuruingestauth and tenantfromauth all say `go 1.25.0` to match the collector
# release OCB_VERSION pins, and dependabot holds them there on purpose. This
# image moves as part of a collector upgrade, never on its own.
#
# Cross-compiled, not emulated. The build stage is pinned to BUILDPLATFORM and
# the collector is compiled for TARGETARCH, so the arm64 image is produced by a
# native toolchain instead of running the whole Go compiler under QEMU. OCB
# builds a complete collector, which is the slowest thing in this repo: emulated,
# this image's arm64 leg took 37 minutes of the v0.13.0 release, and the agent's
# took over an hour — the only thing between a tag push and a published Release.
FROM --platform=$BUILDPLATFORM golang:1.25 AS build
ARG OCB_VERSION=0.154.0
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

# The builder itself is a build-platform tool: it runs here and shells out to
# `go build` for the target, so it is installed WITHOUT the target env below.
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
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} builder --config ocb-manifest.yaml

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /src/build/avuru-gateway /avuru-gateway
# OTLP gRPC / OTLP HTTP / Sentry ingest / healthcheck
EXPOSE 4317 4318 4319 13133
ENTRYPOINT ["/avuru-gateway"]
