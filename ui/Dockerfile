# UI image: Next.js static export served by nginx. The UI is its own deployable
# (separate pod), single-origin with the hub. Build context = repo root.

FROM node:24-alpine AS build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM nginx:1.27-alpine
# /api single-origin proxy. HUB_UPSTREAM defaults to the compose service name;
# the Helm chart overrides it to <release>-hub:8080. The resolver is derived
# from /etc/resolv.conf at start (10-resolver.envsh) so it works in Docker AND
# Kubernetes. nginx's entrypoint renders templates/*.template via envsubst.
ENV HUB_UPSTREAM=hub:8080
COPY ui/default.conf.template /etc/nginx/templates/default.conf.template
COPY ui/docker-entrypoint.d/10-resolver.envsh /docker-entrypoint.d/10-resolver.envsh
COPY --from=build /src/ui/out/ /usr/share/nginx/html/
EXPOSE 80
