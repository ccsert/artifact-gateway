# Artifact Gateway

Artifact Gateway serves native OCI, Raw, Maven, and Conan 2 Hosted repositories
using PostgreSQL for lifecycle metadata and MinIO-compatible object storage for
verified bytes. Legacy Groups remain available for allowlisted external Proxy
reads, while V2 Groups resolve managed Hosted and Proxy members.

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
containers, applies every migration, verifies a second migration run is a
no-op, and runs the persistent-store tests. Applied migration filenames and
SHA-256 checksums are recorded in `artifact_gateway_schema_migrations`; edit
history only through a new forward migration.

Native protocol fixtures exercise the externally visible behavior without an
external package service:

```sh
make native-oci-e2e
make native-raw-e2e
make native-maven-e2e
make conan-e2e
```

The repository console is generated from the same OpenAPI contract. Check its
client, types, production build, and browser flow with:

```sh
make console-api-check
make console-typecheck
make console-build
make console-e2e
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

After a Gateway route or generated management contract changes, rebuild the
running development service before testing the Console. Vite reloads the
Console independently and can otherwise call a stale Gateway image.

```sh
docker compose up --build -d gateway
docker compose ps gateway
```

## Native Hosted repositories

Administrators create repositories through `POST /api/v2/repositories` with an
idempotency key and a `format` of `oci`, `raw`, `maven`, or `conan`. OCI
repositories are rooted at `/v2/<repository>/<image>/...`; Raw repositories use
`/raw/<repository>/<path>`; Maven uses `/repository/maven/<repository>/...`;
Conan 2 uses `/conan/v2/<repository>/...`.

OCI supports blob upload, resumable PATCH, mounting, manifest/tag publication,
GET/HEAD, byte ranges, and manifest deletion. Raw supports PUT, GET, HEAD,
single byte ranges, and DELETE. Clients authenticate using the normal resolver
Bearer token flow. Metadata and coordination are stored in PostgreSQL, while
MinIO stores only content-addressed object bytes.

`GATEWAY_REPOSITORY_READERS` configures read grants in the form
`actor=repository-pattern|repository-pattern`. `GATEWAY_REPOSITORY_CACHE_QUOTAS`
sets legacy Proxy cache limits. `GATEWAY_RAW_CACHE_MAX_OBJECT_BYTES` and
`GATEWAY_CONAN_CACHE_MAX_OBJECT_BYTES` cap an individual cached Proxy object
for their respective formats (both default to 1 GiB). Configure OCI, Maven,
and Raw Proxy host allowlists with their matching
`GATEWAY_{OCI,MAVEN,RAW}_PROXY_ALLOWED_HOSTS` variables. Conan uses the
allowlist attached to its bound Group member, so it has no global host variable.
Maven Proxy caches immutable components for 24 hours by default; its metadata
and negative-cache lifetimes default to 15 and 10 minutes and can be overridden
with `GATEWAY_MAVEN_CACHE_TTL`, `GATEWAY_MAVEN_METADATA_CACHE_TTL`, and
`GATEWAY_MAVEN_NEGATIVE_CACHE_TTL`.
For OIDC, configure `GATEWAY_OIDC_ISSUER` and `GATEWAY_OIDC_AUDIENCE`; the JWKS
URL defaults to the issuer's standard path. Reader, writer, and administrator
role mappings are configured with `GATEWAY_OIDC_READER_ROLES`,
`GATEWAY_OIDC_WRITER_ROLES`, and `GATEWAY_OIDC_ADMIN_ROLES`.

Proxy repositories accept a per-repository egress proxy (`egressProxy` in the
V2 management API) with `direct`, `environment`, and `custom` modes; custom
supports HTTP CONNECT and SOCKS5 with optional authentication and a `noProxy`
bypass list. Stored proxy passwords are encrypted with AES-256-GCM using
`GATEWAY_EGRESS_PROXY_KEY` (32 bytes as hex, base64, or raw; generate with
`openssl rand -hex 32`). `POST /api/v2/repositories/{id}/egress-proxy:test`
probes the configured egress path, and the Console repository settings dialog
exposes the full form. See `docs/proxy-egress-design.md`.

Administrators can inspect audits at `GET /api/v1/audits`, metrics at
`GET /metrics`, and cache maintenance at `GET /api/v1/operations/cache`.
The Console uses the generated `/api/v2` management client for Repository
operations and calls the `/api/v1/operations/cache` surface directly for
administrator-only cache status and collection.
