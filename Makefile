# Thin root dispatcher ONLY — build logic lives in each component
# (agent_docs/development.md). Keep it that way.
.PHONY: hub ui ui-image gateway-image check helm-check e2e e2e-helm e2e-ui dev dev-clean version version-set

COMPOSE := docker compose -f deploy/compose/docker-compose.yaml

# Single source of truth for the version (see RELEASING.md). `make version`
# prints it; `make version-set V=x.y.z` stamps it into the components. The hub
# takes its version from the build tag (hub/Dockerfile ARG VERSION), so it has
# no file to stamp.
version:
	@cat VERSION

version-set:
	@test -n "$(V)" || { echo "usage: make version-set V=x.y.z[-SNAPSHOT]"; exit 1; }
	@printf '%s\n' "$(V)" > VERSION
	@perl -i -pe 's/"version": ".*"/"version": "$(V)"/ && ($$done=1) if !$$done' ui/package.json
	@perl -i -pe 's/^version: .*/version: $(V)/ && ($$done=1) if !$$done' deploy/helm/avuruops/Chart.yaml
	@perl -i -pe 's/^appVersion: .*/appVersion: "$(V)"/ && ($$done=1) if !$$done' deploy/helm/avuruops/Chart.yaml
	@perl -i -pe 's{(avuru-obs-(?:hub|ui|gateway)):\S+}{$$1:$(V)}g' deploy/helm/avuruops/Chart.yaml
	@echo "version set to $(V) (VERSION, ui/package.json, Chart.yaml)"

hub:
	$(MAKE) -C hub build

# UI is a separate deployable now — build the static export only (served by
# its own nginx image / the UI container), no longer copied into the hub.
ui:
	cd ui && npm run build

ui-image:
	docker build -f ui/Dockerfile -t avuru-obs-ui:local .

gateway-image:
	docker build -f gateway/Dockerfile -t avuru-obs-gateway:local .

check:
	cd hub && go build ./... && go test -race ./...
	cd gateway/sentryreceiver && go build ./... && go vet ./... && go test ./...
	cd ui && npm run lint && npm run build

e2e:
	$(COMPOSE) down -v --remove-orphans
	$(COMPOSE) up -d --build --wait clickhouse hub
	$(COMPOSE) up -d --build gateway demo
	sleep 3 && cd tools/seed && go run . -endpoint http://localhost:4318 -fixtures ../../deploy/compose/seed/fixtures
	cd e2e && go test -tags=e2e -count=1 -v ./... ; rc=$$? ; cd .. && $(COMPOSE) down -v && exit $$rc

# Chart render assertions (lint + template matrix) — fast, no cluster.
helm-check:
	bash deploy/helm/template-test.sh

# Helm install smoke: kind cluster + helm install + seed + assert (traces +
# correlated logs). Owns the kind lifecycle (deploy/helm/e2e-helm.sh).
e2e-helm:
	bash deploy/helm/e2e-helm.sh

# UI smoke (Playwright) against the seeded stack — Playwright hits the UI
# origin (:3001), which serves the SPA and proxies /api to the hub.
e2e-ui:
	$(COMPOSE) down -v --remove-orphans
	$(COMPOSE) up -d --build --wait clickhouse hub
	$(COMPOSE) up -d --build gateway demo ui
	sleep 3 && cd tools/seed && go run . -endpoint http://localhost:4318 -fixtures ../../deploy/compose/seed/fixtures
	cd ui && npx playwright test ; rc=$$? ; cd .. && $(COMPOSE) down -v && exit $$rc

dev:
	$(COMPOSE) up --build

dev-clean:
	$(COMPOSE) down -v
