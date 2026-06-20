# UI image: Next.js static export served by nginx. The UI is its own deployable
# (separate pod), single-origin with the hub. Build context = repo root.

FROM node:24-alpine AS build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/ ./
RUN npm run build

FROM nginx:1.27-alpine
COPY ui/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/ui/out/ /usr/share/nginx/html/
EXPOSE 80
