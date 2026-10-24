.PHONY: dev-up dev-down generate test lint

# podman compose also works: make dev-up COMPOSE="podman compose"
COMPOSE ?= docker compose

FOLIO_TEST_DB_URL ?= postgres://postgres:postgres@localhost:5452/postgres?sslmode=disable
export FOLIO_TEST_DB_URL

dev-up:
	$(COMPOSE) -f zarf/compose/docker_compose.yaml up -d --wait

dev-down:
	$(COMPOSE) -f zarf/compose/docker_compose.yaml down

generate:
	go tool oapi-codegen -config internal/httpapi/oapi.yaml internal/httpapi/openapi.yaml

test: generate
	go test ./...

lint:
	go vet ./...
