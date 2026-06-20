# Hub image: Next.js static export embedded into a static Go binary.
# Build context = repo root (compose: context ../.., dockerfile hub/Dockerfile).

FROM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src/hub
COPY hub/go.mod hub/go.sum ./
RUN go mod download
COPY hub/ ./
COPY --from=ui /src/ui/out/ internal/ui/dist/
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/avuru/avuru-obs/hub/internal/api.Version=${VERSION}" -o /hub ./cmd/hub

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /hub /hub
EXPOSE 8080
ENTRYPOINT ["/hub"]
