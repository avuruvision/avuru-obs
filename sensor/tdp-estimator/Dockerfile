# tdp-estimator image: the sensor DaemonSet's opt-in 5th container. Mirrors
# hub/Dockerfile's shape (static Go binary, distroless runtime).
# Build context = repo root (release.yml: context ., file sensor/tdp-estimator/Dockerfile).

# Cross-compiled rather than emulated, like hub/Dockerfile.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src/sensor/tdp-estimator
COPY sensor/tdp-estimator/go.mod ./
RUN go mod download
COPY sensor/tdp-estimator/ ./
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /tdp-estimator .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /tdp-estimator /tdp-estimator
ENTRYPOINT ["/tdp-estimator"]
