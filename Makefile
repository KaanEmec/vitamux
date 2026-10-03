# Vitamux developer commands. Run `make help`.
SHELL := /bin/bash
-include .env
export

VERSION ?= 0.0.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/KaanEmec/vitamux/internal/version.Version=$(VERSION) -X github.com/KaanEmec/vitamux/internal/version.Commit=$(COMMIT)
COMPOSE := docker compose -f deploy/compose/compose.dev.yaml

.PHONY: help dev services migrate services-down web-install web-build build test test-integration sqlc openapi lint fixtures fixture-guard golden image clean

help: ## Show targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

dev: migrate web-install ## Run PostgreSQL, the Go server (live reload) and Vite
	@test -f .env || { echo "missing .env — run: cp .env.example .env"; exit 1; }
	@test -f "$(VITAMUX_MASTER_KEY_FILE)" || go run ./cmd/vitamux admin init-secrets
	@trap 'kill 0' EXIT; go tool air & npm --prefix web run dev & wait

services: ## Start dev services, wait until healthy, apply roles.sql (idempotent)
	$(COMPOSE) up -d --wait
	$(COMPOSE) exec -T postgres psql -q -U vitamux -d vitamux -v ON_ERROR_STOP=1 < deploy/sql/roles.sql

migrate: services ## Apply database migrations to the dev database
	go run ./cmd/vitamux migrate up

services-down: ## Stop dev services (keeps data volume)
	$(COMPOSE) down

web-install: ## Install pinned web dependencies
	@test -d web/node_modules || npm --prefix web ci

web-build: web-install ## Build the SPA into web/build
	npm --prefix web run build

build: web-build ## Build bin/vitamux with the UI embedded
	CGO_ENABLED=0 go build -tags webui -trimpath -ldflags "$(LDFLAGS)" -o bin/vitamux ./cmd/vitamux

test: ## Unit tests (offline)
	go test ./...

test-integration: services ## Integration tests against dev PostgreSQL
	go test -tags integration ./...

sqlc: ## Regenerate internal/db/dbq from migrations and queries
	go tool sqlc generate

openapi: web-install ## Regenerate Go server types and the TS client from api/openapi.yaml
	go tool oapi-codegen -config internal/api/oapi/config.yaml api/openapi.yaml
	npm --prefix web run openapi

lint: web-install ## Go and web linters
	golangci-lint run ./...
	npm --prefix web run check
	npm --prefix web run lint
	npm --prefix web run lint:api

SEED ?= 42
fixtures: ## Generate the synthetic year into fixtures/generated (SEED=42; ~0.8 GB, git-ignored)
	go run ./tools/fixturegen -seed $(SEED) -out fixtures/generated

fixture-guard: ## Check fixtures/ and testdata/ for the synthetic marker and PII-like strings
	go run ./tools/fixtureguard

golden: ## Run golden tests (E07)
	go test ./... -run Golden

image: ## Build the release container image locally
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t vitamux:dev .

clean: ## Remove build outputs
	rm -rf bin tmp web/build web/.svelte-kit
