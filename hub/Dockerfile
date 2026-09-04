# Hub image: API-only static Go binary (the UI is a separate nginx image).
# Build context = repo root (compose: context ../.., dockerfile hub/Dockerfile).

# Cross-compiled rather than emulated: a static CGO-free binary needs no
# target toolchain, so the arm64 image is built natively on the build host.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src/hub
COPY hub/go.mod hub/go.sum ./
RUN go mod download
COPY hub/ ./
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags "-X github.com/avuru/avuru-obs/hub/internal/api.Version=${VERSION}" -o /hub ./cmd/hub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hub /hub
EXPOSE 8080
ENTRYPOINT ["/hub"]
