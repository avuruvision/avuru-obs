# UI image: Next.js static export served by nginx. The UI is its own deployable
# (separate pod), single-origin with the hub. Build context = repo root.

FROM node:26-alpine AS build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM nginx:1.31-alpine
# /api single-origin proxy. HUB_UPSTREAM defaults to the compose service name;
# the Helm chart overrides it to <release>-hub:8080. The resolver comes from the
# nginx image's built-in NGINX_LOCAL_RESOLVERS (from /etc/resolv.conf — Docker
# DNS in compose, CoreDNS in k8s). The entrypoint renders templates/*.template.
ENV HUB_UPSTREAM=hub:8080
COPY ui/default.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /src/ui/out/ /usr/share/nginx/html/
EXPOSE 80
