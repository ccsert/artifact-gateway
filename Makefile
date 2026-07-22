.DEFAULT_GOAL := help

COMPOSE := docker compose --env-file .env -f compose.gitea.yml
GO_IMAGE := golang:1.26-alpine
LINT_IMAGE := golangci/golangci-lint:v2.12.2

.PHONY: help gitea-up gitea-down gitea-reset gitea-seed gitea-fixture oci-e2e raw-e2e conan-e2e maven-e2e maven-e2e-cleanup-test performance-readiness upgrade-readiness release-readiness release-readiness-cleanup-test up down test integration-test integration-down lint fmt build docker-build migrate backup-drill restore-drill

help:
	@printf '%s\n' 'Targets: up, down, test, integration-test, integration-down, lint, fmt, build, docker-build, migrate, backup-drill, restore-drill, gitea-up, gitea-down, gitea-reset, gitea-seed, gitea-fixture, oci-e2e, raw-e2e, conan-e2e, maven-e2e, maven-e2e-cleanup-test, performance-readiness, upgrade-readiness, release-readiness, release-readiness-cleanup-test'

up:
	@docker compose --env-file .env -f compose.yml up --build --wait

down:
	@docker compose --env-file .env -f compose.yml down

test:
	@python3 -m unittest scripts/maven_proxy_fixture_test.py
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) go test ./...

integration-test: integration-down
	@docker compose -f compose.integration.yml up -d --wait postgres redis minio-ready
	@docker compose -f compose.integration.yml run --rm --no-deps migrate
	@docker compose -f compose.integration.yml run --rm --no-deps test
	@docker compose -f compose.integration.yml down -v

integration-down:
	@docker compose -f compose.integration.yml down -v

lint:
	@docker run --rm -v "$(CURDIR):/src" -w /src $(LINT_IMAGE) golangci-lint run

fmt:
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) gofmt -w cmd internal

build:
	@docker build -t artifact-gateway:dev .

docker-build:
	@docker build -t artifact-gateway:dev .

migrate:
	@docker compose --env-file .env -f compose.yml run --rm migrate

backup-drill:
	@./scripts/backup-drill.sh

restore-drill:
	@./scripts/restore-drill.sh "$(BACKUP_DIR)"

gitea-up:
	@./scripts/gitea-up.sh

gitea-down:
	@$(COMPOSE) down

gitea-reset:
	@./scripts/gitea-reset.sh

gitea-seed:
	@./scripts/gitea-seed.sh

gitea-fixture: gitea-up gitea-seed

oci-e2e: gitea-fixture
	@./scripts/oci-e2e.sh

raw-e2e:
	@docker run --rm -v "$(CURDIR):/src" -v artifact-gateway-go-mod:/go/pkg/mod -w /src $(GO_IMAGE) go test -v -count=1 -run '^TestRaw(StandardHTTPClientE2E|HostedStandardHTTPClientE2E)$$' ./internal/app

conan-e2e:
	@docker run --rm -v "$(CURDIR):/src" -v artifact-gateway-go-mod:/go/pkg/mod -w /src $(GO_IMAGE) sh -ec 'apk add --no-cache py3-pip >/dev/null && pip install --break-system-packages --no-cache-dir conan==2.21.0 >/dev/null && CONAN_BINARY="$$(command -v conan)" go test -v -count=1 -run "^TestConan2Client" ./internal/app'

maven-e2e: gitea-fixture
	@./scripts/maven-e2e.sh

maven-e2e-cleanup-test: gitea-fixture
	@./scripts/maven-e2e-cleanup-test.sh

performance-readiness:
	@./scripts/performance-readiness.sh

upgrade-readiness:
	@./scripts/upgrade-readiness.sh

release-readiness:
	@./scripts/release-readiness.sh

release-readiness-cleanup-test: gitea-fixture
	@./scripts/release-readiness-cleanup-test.sh
