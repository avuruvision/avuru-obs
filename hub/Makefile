.PHONY: build test test-int lint run

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/avuru/avuru-obs/hub/internal/api.Version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o bin/hub ./cmd/hub

test:
	go test -race ./...

# Integration tests: testcontainers-go + ephemeral ClickHouse with the real
# gateway/schemas migrations. Colima users: see agent_docs/development.md for
# the DOCKER_HOST/TESTCONTAINERS env vars.
test-int:
	go test -race -tags=integration -count=1 -timeout 8m ./internal/storage/...

lint:
	golangci-lint run

run:
	go run ./cmd/hub
