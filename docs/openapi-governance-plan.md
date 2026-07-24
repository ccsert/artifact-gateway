# OpenAPI Governance Plan

Status: implemented governance baseline. This document defines the source,
generation, and review boundaries for the Native Hosted contract.

## Target source layout

The manually maintained source is a multi-file YAML tree:

```text
api/openapi/native-hosted.yaml
api/openapi/components/*.yaml
api/openapi/management/*.yaml
api/openapi/protocols/*.yaml
```

`native-hosted.yaml` is the public source entrypoint. `management.yaml` keeps
the complete management projection and `management-runtime.yaml` is the
generated-server projection for the active repository-management routes.
`api/openapi/native-hosted-v1.json` is a versioned generated bundle. It must
not be hand-edited; the Console client, API-diff check, release artifacts, and
external consumers consume this resolved document.

## Build and review gates

The repository provides these Make targets:

- `make openapi-bundle`: resolve the YAML entrypoint and references into the
  versioned JSON bundle.
- `make openapi-check`: regenerate the bundle and fail on a diff, validate the
  resolved OpenAPI document, run contract tests, and regenerate/check the
  Console client and generated management Go contract.
- `make openapi-generate-admin`: bundle the active management projection in a
  temporary file and regenerate `internal/admin/openapi/generated.go`.

Every contract change must update its source fragment, generated bundle,
contract test, and any affected generated client in the same reviewable change.
Redocly CLI is pinned in `tools/openapi/package-lock.json`; `oapi-codegen` is
pinned with the Go `tool` directive. CI runs `make openapi-check` before the
test suite accepts generated output.

## Code generation boundary

Management APIs are conventional resource routes. `oapi-codegen` generates
request/response types plus standard and strict server interfaces for the active
repository-management boundary. The runtime uses the generated standard HTTP
server wrapper so path, query, and idempotency-header binding follows the
contract, while handwritten code retains authorization, transactions, and
domain decisions. The strict interface is generated alongside it as the typed
extension point for later management route migrations.

Protocol APIs are not handler-generated. OCI Registry V2, Raw, Maven, and
Conan behavior is defined first by official specifications and ecosystem client
expectations. `api/openapi/protocols/*.yaml` contains the Gateway overlay and
the compatibility matrix records the normative references and executable gates.
OpenAPI documents the exposed Gateway surface; focused handler/contract tests
and E2E fixtures remain the conformance evidence.

## Review sequence

1. Edit only the YAML source fragments and update the relevant overlay notes.
2. Run `make openapi-check`; include the generated JSON, Console, and Go files
   in the same change.
3. Run the protocol E2E fixture affected by the change and `go test ./...`.
4. Keep protocol handler extraction and new protocol support out of contract
   generation changes.
