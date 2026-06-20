# Thin root dispatcher ONLY — build logic lives in each component
# (agent_docs/development.md). Keep it that way.
.PHONY: hub agent ui ui-image proto check e2e e2e-helm e2e-ui dev dev-clean

COMPOSE := docker compose -f deploy/compose/docker-compose.yaml

hub:
	$(MAKE) -C hub build

agent:
	cd agent && cargo build

# UI is a separate deployable now — build the static export only (served by
# its own nginx image / the UI container), no longer copied into the hub.
ui:
	cd ui && npm run build

ui-image:
	docker build -f ui/Dockerfile -t avuru-obs-ui:local .

proto:
	@echo "proto codegen (buf) is wired in M1 — proto/ is the source of truth"

check:
	cd hub && go build ./... && go test -race ./...
	cd agent && cargo check && cargo test && cargo clippy -- -D warnings
	cd ui && npm run lint && npm run build

e2e:
	$(COMPOSE) down -v --remove-orphans
	$(COMPOSE) up -d --build --wait clickhouse hub
	$(COMPOSE) up -d gateway demo
	sleep 3 && cd tools/seed && go run . -endpoint http://localhost:4318 -fixtures ../../deploy/compose/seed/fixtures
	cd e2e && go test -tags=e2e -count=1 -v ./... ; rc=$$? ; cd .. && $(COMPOSE) down -v && exit $$rc

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
