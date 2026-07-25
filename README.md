# Artifact Gateway

Artifact Gateway serves native OCI, Raw, and Maven Hosted repositories using
PostgreSQL for lifecycle metadata and MinIO-compatible object storage for
verified bytes. Legacy Groups remain available only for allowlisted external
Proxy reads.

## Local development

```sh
cp .env.example .env
# Replace local-only credential placeholders.
make up
```

`make up` starts Gateway, PostgreSQL, and MinIO. `GET /livez` reports process
liveness; `GET /readyz` verifies PostgreSQL and object storage. `make down`
preserves local volumes.

Run `make test`, `make lint`, `make build`, or `make docker-build` from a clean
checkout. `make integration-test` creates isolated PostgreSQL and MinIO
containers, applies every migration, and runs the persistent-store tests.

Native protocol fixtures exercise the externally visible behavior without an
external package service:

```sh
make native-oci-e2e
make native-raw-e2e
make native-maven-e2e
make conan-e2e
```

## OpenAPI contract workflow

The editable Native Hosted contract starts at
`api/openapi/native-hosted.yaml`. Shared components, management routes, and
protocol overlays live in its sibling YAML directories. Do not edit
`api/openapi/native-hosted-v1.json`, `console/src/client`, or
`internal/admin/openapi/generated.go` by hand: they are generated artifacts.

```sh
make openapi-bundle
make openapi-generate-admin
make openapi-check
```

`make openapi-check` rebuilds the public JSON bundle, the generated Console
client, and the generated repository-management Go contract; it then fails if
any generated artifact differs from the worktree.

## Native Hosted repositories

Administrators create repositories through `POST /api/v2/repositories` with an
idempotency key and a `format` of `oci`, `raw`, `maven`, or `conan`. OCI repositories
are rooted at `/v2/<repository>/<image>/...`; Raw repositories use
`/raw/<repository>/<path>`; Maven uses `/repository/maven/<repository>/...`.

OCI supports blob upload, resumable PATCH, mounting, manifest/tag publication,
GET/HEAD, byte ranges, and manifest deletion. Raw supports PUT, GET, HEAD,
single byte ranges, and DELETE. Clients authenticate using the normal resolver
Bearer token flow. Metadata and coordination are stored in PostgreSQL, while
MinIO stores only content-addressed object bytes.

`GATEWAY_REPOSITORY_READERS` configures read grants in the form
`actor=repository-pattern|repository-pattern`. `GATEWAY_REPOSITORY_CACHE_QUOTAS`
sets legacy Proxy cache limits. `GATEWAY_RAW_CACHE_MAX_OBJECT_BYTES` and
`GATEWAY_CONAN_CACHE_MAX_OBJECT_BYTES` cap an individual cached Proxy object
for their respective formats (both default to 1 GiB). Configure each Proxy's
upstream allowlist with its format-specific `GATEWAY_*_PROXY_ALLOWED_HOSTS`
variable. For OIDC, configure `GATEWAY_OIDC_ISSUER` and `GATEWAY_OIDC_AUDIENCE`;
the JWKS URL defaults to the issuer's standard path.

Administrators can inspect audits at `GET /api/v1/audits`, metrics at
`GET /metrics`, and cache maintenance at `GET /api/v1/operations/cache`.
