# Avuru gateway — minimal OTel Collector distro built with OCB from
# ocb-manifest.yaml. Build from the REPO ROOT:
#   docker build -t avuru-obs-gateway -f gateway/Dockerfile .
FROM golang:1.25 AS build
ARG OCB_VERSION=0.154.0
WORKDIR /src

# The builder version matches otelcol_version — mixed lines are unsupported.
RUN go install go.opentelemetry.io/collector/cmd/builder@v${OCB_VERSION}

COPY gateway/ocb-manifest.yaml .
RUN CGO_ENABLED=0 builder --config ocb-manifest.yaml

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /src/build/avuru-gateway /avuru-gateway
# OTLP gRPC / OTLP HTTP / healthcheck
EXPOSE 4317 4318 13133
ENTRYPOINT ["/avuru-gateway"]
