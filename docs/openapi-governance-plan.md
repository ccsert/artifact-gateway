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
repository, Maven publish-session, and Maven artifact-list boundaries. The
runtime uses the generated standard HTTP server wrapper so path, query, and
idempotency-header binding follows the contract, while handwritten code retains
authorization, transactions, and domain decisions. The `:commit` session route
uses a small standard-library routing bridge because its path parameter has a
literal suffix. The strict interface is generated alongside it as the typed
extension point for later management route migrations.

## Management runtime coverage

`management.yaml` is the complete reviewed management design; it is not a
runtime authority. `management-runtime.yaml` is the authoritative contract for
the production-backed `/api/v2` surface. Its versioned bundle, generated
server/client code, and assembled-handler conformance tests must change
together. This intentionally keeps the API shape in one compact, reviewable
source rather than duplicating operation metadata across Go annotations and
OpenAPI fragments. The runtime projection intentionally contains only
operations with an equivalent handler:

| Contract area | Runtime status | Reason |
| --- | --- | --- |
| Repositories | Generated | Hosted repositories have UUID identity, lifecycle state, and idempotent creation. |
| Maven publish sessions | Generated | The existing session service supplies authorization, staged-object validation, and transactional commit. |
| Maven artifact list | Generated | The existing committed-artifact store supplies the list response. |
| Artifact detail and deletion | Generated | Maven artifacts support UUID detail and idempotent logical deletion; tombstoning removes resolvable asset metadata while the orphan collector retains responsibility for byte reclamation. |
| Groups | Generated | V2 groups are a separate UUID-based aggregate over active Hosted Repositories; V1 OCI, Maven, Raw, and Conan groups remain protocol-specific and unchanged. |
| Grants | Generated | Repository grant sets are versioned with `ETag`/`If-Match` and persist principal-to-scope mappings. |
| Retention policies | Generated | Policies have a default `keepDays=30`/`minimumVersions=1`, versioned `If-Match` replacement, and Memory/Postgres persistence. Durable retention jobs apply format-aware cleanup units for Maven, OCI, Conan, Raw, npm, and PyPI Hosted repositories; byte reclamation remains the orphan collector's responsibility. |

Adding a deferred path to `management-runtime.yaml` requires first adding the
corresponding domain aggregate, persistence operations in both memory and
Postgres stores, authorization behavior, and a handler-level contract test.
The generated wrapper then owns route and parameter binding; it must not be
used to publish an unsupported route.

`internal/app/openapi_runtime_contract_test.go` is the management contract
laboratory. Its operation inventory executes every published operation through
the assembled Gateway and rejects missing or duplicate operation IDs and
unregistered routes. Its fixture matrix validates declared response status,
headers, and JSON schema for representative successful requests. Add a strict
fixture whenever a route family is introduced or its response shape changes;
feature tests remain responsible for domain scenarios and authorization
branches outside that matrix.

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
