# UI image: Next.js static export served by nginx. The UI is its own deployable
# (separate pod), single-origin with the hub. Build context = repo root.

# --platform=$BUILDPLATFORM: the Next.js static export is arch-independent, so
# it builds once natively; without the pin, the arm64 half of a multi-arch
# build runs Node under QEMU — an hour of emulation that then dies on
# "qemu: uncaught target signal 4" (this failed the v0.2.0 cut). Only the
# nginx stage below is per-architecture.
# Node stays on the Active LTS line, which is 24 until 2026-10-28. Odd majors
# (25, 27) are Current for their whole life and never become LTS, so the next
# candidate here is 26, not whatever number is highest; revisit once 26 is LTS,
# which is also when 24 enters maintenance.
FROM --platform=$BUILDPLATFORM node:24-alpine AS build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM nginx:1.31-alpine
# Patch base-image OS packages (OpenSSL/zlib/expat, …) to the latest Alpine
# security-fixed versions at build time. The floating nginx:1.27-alpine tag can
# lag behind Alpine's patched packages; this keeps the image scanner-clean
# (Harbor/Trivy) without pinning a digest we'd have to bump by hand.
RUN apk upgrade --no-cache
# /api single-origin proxy. HUB_UPSTREAM defaults to the compose service name;
# the Helm chart overrides it to <release>-hub:8080. The resolver comes from the
# nginx image's built-in NGINX_LOCAL_RESOLVERS (from /etc/resolv.conf — Docker
# DNS in compose, CoreDNS in k8s). The entrypoint renders templates/*.template.
ENV HUB_UPSTREAM=hub:8080
COPY ui/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /src/ui/out/ /usr/share/nginx/html/
EXPOSE 80
