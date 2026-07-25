.DEFAULT_GOAL := help

GO_IMAGE := golang:1.26-alpine
LINT_IMAGE := golangci/golangci-lint:v2.12.2
OPENAPI_TOOLS := tools/openapi
OPENAPI_SOURCE := api/openapi/native-hosted.yaml
OPENAPI_BUNDLE := api/openapi/native-hosted-v1.json

.PHONY: help raw-e2e conan-e2e native-maven-e2e native-oci-e2e native-raw-e2e readiness-e2e resolver-rotation-e2e oci-performance-e2e cache-operations-e2e backup-restore-readiness upgrade-readiness up down test api-contract api-change-check integration-test integration-down lint fmt build docker-build migrate backup-drill restore-drill console-build console-typecheck console-api-check console-e2e openapi-bundle openapi-generate-admin openapi-check

help:
	@printf '%s\n' 'Targets: up, down, test, api-contract, api-change-check, integration-test, integration-down, lint, fmt, build, docker-build, migrate, backup-drill, restore-drill, raw-e2e, conan-e2e, native-maven-e2e, native-oci-e2e, native-raw-e2e, readiness-e2e, resolver-rotation-e2e, oci-performance-e2e, backup-restore-readiness, upgrade-readiness, openapi-bundle, openapi-generate-admin, openapi-check'

openapi-bundle:
	@npm --prefix $(OPENAPI_TOOLS) ci --ignore-scripts --no-audit --no-fund
	@$(OPENAPI_TOOLS)/node_modules/.bin/redocly bundle $(OPENAPI_SOURCE) --output $(OPENAPI_BUNDLE) --ext json

openapi-generate-admin: openapi-bundle
	@./scripts/generate-admin-openapi.sh

openapi-check: openapi-generate-admin
	@npm --prefix console ci --ignore-scripts --no-audit --no-fund
	@npm --prefix console run check:api
	@go test ./contracts
	@git diff --exit-code -- $(OPENAPI_BUNDLE) console/src/client internal/admin/openapi/generated.go

console-build:
	@cd console && npm run build

console-typecheck:
	@cd console && npm run typecheck

console-api-check:
	@cd console && npm run check:api

console-e2e:
	@cd console && npm run e2e

up:
	@docker compose --env-file .env -f compose.yml up --build --wait --remove-orphans

down:
	@docker compose --env-file .env -f compose.yml down

test:
	@python3 -m unittest scripts/maven_proxy_fixture_test.py
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) go test ./...

api-contract:
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) go test ./contracts

api-change-check:
	@./scripts/api-change-check.sh

integration-test: integration-down
	@docker compose -f compose.integration.yml up -d --wait postgres minio-ready
	@docker compose -f compose.integration.yml run --rm --no-deps migrate
	@docker compose -f compose.integration.yml run --rm --no-deps test
	@docker compose -f compose.integration.yml down -v

integration-down:
	@docker compose -f compose.integration.yml down -v --remove-orphans

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

raw-e2e:
	@./scripts/raw-e2e.sh

conan-e2e:
	@docker run --rm -v "$(CURDIR):/src" -v artifact-gateway-go-mod:/go/pkg/mod -w /src $(GO_IMAGE) sh -ec 'apk add --no-cache py3-pip >/dev/null && pip install --break-system-packages --no-cache-dir conan==2.21.0 >/dev/null && CONAN_BINARY="$$(command -v conan)" go test -v -count=1 -run "^TestConan2Client" ./internal/app'

native-maven-e2e:
	@./scripts/native-maven-e2e.sh

native-oci-e2e:
	@./scripts/native-oci-e2e.sh

native-raw-e2e:
	@./scripts/native-raw-e2e.sh

readiness-e2e:
	@./scripts/readiness-e2e.sh

resolver-rotation-e2e:
	@./scripts/resolver-rotation-e2e.sh

oci-performance-e2e:
	@./scripts/oci-performance-e2e.sh

cache-operations-e2e:
	@./scripts/cache-operations-e2e.sh

backup-restore-readiness:
	@./scripts/backup-restore-readiness.sh

upgrade-readiness:
	@./scripts/upgrade-readiness.sh
