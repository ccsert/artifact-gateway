.DEFAULT_GOAL := help

COMPOSE := docker compose --env-file .env -f compose.gitea.yml

.PHONY: help gitea-up gitea-down gitea-reset gitea-seed gitea-fixture up down test lint fmt build docker-build migrate

help:
	@printf '%s\n' 'Targets: up, down, test, lint, fmt, build, docker-build, migrate, gitea-up, gitea-down, gitea-reset, gitea-seed, gitea-fixture'

up:
	@docker compose --env-file .env -f compose.yml up --build --wait

down:
	@docker compose --env-file .env -f compose.yml down

test:
	@go test ./...

lint:
	@golangci-lint run

fmt:
	@gofmt -w cmd internal

build:
	@go build ./cmd/gateway

docker-build:
	@docker build -t artifact-gateway:dev .

migrate:
	@docker compose --env-file .env -f compose.yml run --rm migrate

gitea-up:
	@./scripts/gitea-up.sh

gitea-down:
	@$(COMPOSE) down

gitea-reset:
	@./scripts/gitea-reset.sh

gitea-seed:
	@./scripts/gitea-seed.sh

gitea-fixture: gitea-up gitea-seed
