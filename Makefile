# Vitamux developer commands. Run `make help`.
SHELL := /bin/bash
-include .env
export

VERSION ?= 0.0.0-dev
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X github.com/KaanEmec/vitamux/internal/version.Version=$(VERSION) -X github.com/KaanEmec/vitamux/internal/version.Commit=$(COMMIT)
COMPOSE := docker compose -f deploy/compose/compose.dev.yaml

.PHONY: help dev services services-down web-install web-build build test test-integration lint fixtures golden image clean

help: ## Show targets
	@grep -E '^[a-z-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-18s %s\n", $$1, $$2}'

dev: services web-install ## Run PostgreSQL, the Go server (live reload) and Vite
	@test -f .env || { echo "missing .env — run: cp .env.example .env"; exit 1; }
	@trap 'kill 0' EXIT; go tool air & npm --prefix web run dev & wait

services: ## Start dev services and wait until healthy
	$(COMPOSE) up -d --wait

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

lint: web-install ## Go and web linters
	golangci-lint run ./...
	npm --prefix web run check
	npm --prefix web run lint

fixtures: ## Generate synthetic fixtures (E04)
	@echo "fixturegen arrives in J04.1"; exit 1

golden: ## Run golden tests (E07)
	go test ./... -run Golden

image: ## Build the release container image locally
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t vitamux:dev .

clean: ## Remove build outputs
	rm -rf bin tmp web/build web/.svelte-kit
