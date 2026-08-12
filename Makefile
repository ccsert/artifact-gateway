.DEFAULT_GOAL := help

GO_IMAGE := golang:1.26.5-alpine
LINT_IMAGE := golangci/golangci-lint:v2.12.2
OPENAPI_TOOLS := tools/openapi
CONSOLE_DIR := console
OPENAPI_SOURCE := api/openapi/native-hosted.yaml
OPENAPI_BUNDLE := api/openapi/native-hosted-v1.json

.PHONY: help dev dev-status dev-down local-dev-test kubernetes-local-check kubernetes-local-up kubernetes-local-status kubernetes-local-verify kubernetes-local-down reference-scanner-smoke openapi-dependency-test openapi-tools-ready console-codegen-ready raw-e2e conan-e2e native-maven-e2e native-oci-e2e native-raw-e2e native-npm-e2e native-pypi-e2e native-go-e2e native-apt-e2e readiness-e2e resolver-rotation-e2e oci-performance-e2e cache-operations-e2e backup-restore-readiness upgrade-readiness release-readiness-check docs-check preflight evidence up down test api-contract api-change-check integration-test integration-down lint vet race coverage dependency-audit fmt build docker-build console-docker-build migrate backup-drill restore-drill console-build console-typecheck console-check console-test console-api-check console-e2e openapi-bundle openapi-generate-admin openapi-check

help:
	@printf '%s\n' 'Targets: dev, dev-status, dev-down, kubernetes-local-check, kubernetes-local-up, kubernetes-local-status, kubernetes-local-verify, kubernetes-local-down, reference-scanner-smoke, up, down, test, api-contract, api-change-check, integration-test, integration-down, lint, vet, race, coverage, dependency-audit, fmt, build, docker-build, console-docker-build, migrate, backup-drill, restore-drill, preflight, evidence, raw-e2e, conan-e2e, native-maven-e2e, native-oci-e2e, native-raw-e2e, native-npm-e2e, native-pypi-e2e, native-go-e2e, native-apt-e2e, readiness-e2e, resolver-rotation-e2e, oci-performance-e2e, cache-operations-e2e, backup-restore-readiness, upgrade-readiness, release-readiness-check, docs-check, console-build, console-typecheck, console-check, console-test, console-api-check, console-e2e, openapi-bundle, openapi-generate-admin, openapi-check'

dev:
	@./scripts/local-dev.sh start

dev-status:
	@./scripts/local-dev.sh status

dev-down:
	@./scripts/local-dev.sh stop

local-dev-test:
	@./scripts/local-dev-test.sh

kubernetes-local-check:
	@./scripts/kubernetes-local-manifest-test.sh
	@./scripts/kubernetes-local-test.sh

kubernetes-local-up:
	@./scripts/kubernetes-local.sh up

kubernetes-local-status:
	@./scripts/kubernetes-local.sh status

kubernetes-local-verify:
	@./scripts/kubernetes-local.sh verify

kubernetes-local-down:
	@./scripts/kubernetes-local.sh down

reference-scanner-smoke:
	@./scripts/reference-scanner-smoke.sh

openapi-dependency-test:
	@bash ./scripts/openapi-dependency-test.sh

openapi-tools-ready:
	@bash ./scripts/check-node-dependencies.sh $(OPENAPI_TOOLS) redocly

console-codegen-ready:
	@bash ./scripts/check-node-dependencies.sh $(CONSOLE_DIR) openapi-ts prettier

openapi-bundle: openapi-tools-ready
	@$(OPENAPI_TOOLS)/node_modules/.bin/redocly bundle $(OPENAPI_SOURCE) --output $(OPENAPI_BUNDLE) --ext json

openapi-generate-admin: openapi-bundle
	@$(OPENAPI_TOOLS)/node_modules/.bin/redocly bundle api/openapi/management-runtime.yaml --output api/openapi/management-runtime-v1.json --ext json
	@./scripts/generate-admin-openapi.sh

openapi-check: openapi-generate-admin console-codegen-ready
	@npm --prefix $(CONSOLE_DIR) run check:api
	@go test ./contracts
	@git diff --exit-code -- $(OPENAPI_BUNDLE) api/openapi/management-runtime-v1.json console/src/client internal/admin/openapi/generated.go

console-build:
	@cd console && npm run build

console-typecheck:
	@cd console && npm run typecheck

console-check:
	@cd console && npm run lint
	@cd console && npm run format:check

console-test:
	@cd console && npm run test:coverage

console-api-check:
	@cd console && npm run check:api

console-e2e:
	@./scripts/console-e2e.sh

up:
	@./scripts/local-dev.sh guard
	@docker compose --env-file .env -f compose.yml up --build --wait --remove-orphans

down:
	@docker compose --env-file .env -f compose.yml down

test:
	@./scripts/local-dev-test.sh
	@./scripts/run-rustfs-test.sh
	@bash ./scripts/openapi-dependency-test.sh
	@./scripts/release-readiness-check.sh
	@./scripts/docs-capability-check.sh
	@python3 -m unittest scripts/maven_proxy_fixture_test.py

docs-check:
	@./scripts/docs-capability-check.sh
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) sh -ec 'go list ./... | grep -v "/console/node_modules/" | xargs go test'

api-contract:
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) go test ./contracts

api-change-check:
	@./scripts/api-change-check.sh

integration-test:
	@./scripts/integration-test.sh

integration-down:
	@docker volume create artifact-gateway-go-mod >/dev/null
	@docker volume create artifact-gateway-go-build >/dev/null
	@docker compose -f compose.integration.yml down -v --remove-orphans

lint:
	@docker run --rm -v "$(CURDIR):/src" -w /src $(LINT_IMAGE) golangci-lint run

vet:
	@packages="$$(go list ./... | grep -v '/console/node_modules/')"; go vet $$packages

race:
	@go test -race ./internal/...

coverage:
	@./scripts/coverage-check.sh

dependency-audit:
	@go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...
	@node ./scripts/npm-audit-check.mjs console tools/openapi

fmt:
	@docker run --rm -v "$(CURDIR):/src" -w /src $(GO_IMAGE) gofmt -w cmd internal

build:
	@docker build -t artifact-gateway:dev .

docker-build:
	@docker build -t artifact-gateway:dev .

console-docker-build:
	@docker build -f Dockerfile.console -t artifact-gateway-console:dev .

migrate:
	@docker compose --env-file .env -f compose.yml run --rm migrate

backup-drill:
	@./scripts/backup-drill.sh

restore-drill:
	@./scripts/restore-drill.sh "$(BACKUP_DIR)"

preflight:
	@docker compose --env-file .env -f compose.yml run --rm --no-deps gateway preflight run --format json

evidence:
	@test -n "$(GATEWAY_URL)" || { printf '%s\n' 'set GATEWAY_URL'; exit 2; }
	@test -n "$(EVIDENCE_OUTPUT)" || { printf '%s\n' 'set EVIDENCE_OUTPUT'; exit 2; }
	@test -n "$$GATEWAY_EVIDENCE_ADMIN_TOKEN" || { printf '%s\n' 'set GATEWAY_EVIDENCE_ADMIN_TOKEN'; exit 2; }
	@go run ./cmd/gateway evidence collect --gateway-url "$(GATEWAY_URL)" --output "$(EVIDENCE_OUTPUT)" --revision "$(GIT_REVISION)" --image "$(IMAGE_DIGEST)"

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

native-npm-e2e:
	@./scripts/native-npm-e2e.sh

native-pypi-e2e:
	@./scripts/native-pypi-e2e.sh

native-go-e2e:
	@./scripts/native-go-e2e.sh

native-apt-e2e:
	@./scripts/native-apt-e2e.sh

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

release-readiness-check:
	@./scripts/release-readiness-check.sh
