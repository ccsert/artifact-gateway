# Artifact Gateway

Artifact Gateway serves native OCI, Raw, Maven, Conan 2, and npm Hosted repositories
using PostgreSQL for lifecycle metadata and MinIO-compatible object storage for
verified bytes. Legacy Groups remain available for allowlisted external Proxy
reads, while V2 Groups resolve managed Hosted and Proxy members.

> **Project status:** active development. The core team is hardening contracts,
> operations, and contributor-facing engineering practices before a stable
> public release. Do not infer production support commitments from the current
> package version.

Artifact Gateway is intended to be a complete repository manager for the five
admitted formats: native client reads and publication, Hosted/Proxy/Group
resolution, browsing and search, authorization, audit, retention, recovery,
promotion, and replication. It is not a transparent rewrite proxy, a generic
object browser, or a vulnerability scanner. New formats are added only after
their protocol and full lifecycle contract are defined.

## Local development

```sh
cp .env.example .env
# Replace local-only credential placeholders.
make up
```

`make up` starts Gateway, PostgreSQL, and MinIO. `GET /livez` reports process
liveness; `GET /readyz` verifies PostgreSQL and object storage. `make down`
preserves local volumes.

Start the Console separately and open `http://127.0.0.1:4173`:

```sh
npm --prefix console ci
npm --prefix console run dev -- --host 127.0.0.1
```

Use the `GATEWAY_ADMIN_TOKEN` from the local `.env` file to sign in. Anonymous
browse remains available at `/browse` when the global policy and repository
policy both permit it.

### 单机与集群运行模式

Gateway 默认以 `GATEWAY_NODE_ROLES=standalone` 启动，同时提供协议 API、
周期调度和后台 worker。生产环境可以使用同一个镜像拆分节点职责：

```text
GATEWAY_NODE_ROLES=api
GATEWAY_NODE_ROLES=scheduler
GATEWAY_NODE_ROLES=worker GATEWAY_WORKER_FORMATS=oci
```

所有节点共享 PostgreSQL 和 S3/MinIO；任务领取由数据库租约保证幂等。worker
可以用 `GATEWAY_WORKER_FORMATS` 和 `GATEWAY_WORKER_KINDS` 限制格式与任务类型，
例如只部署 OCI 的 `reclaim,replication` worker。非 API 节点仅暴露
`/livez`、`/readyz` 和 `/metrics`，不会暴露制品协议或管理接口。

拆分部署时应按副本数降低每个节点的数据库连接池上限，避免连接总数超过
PostgreSQL 的 `max_connections`。

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
make native-npm-e2e
make conan-e2e
```

The repository console is generated from the same OpenAPI contract. Check its
client, types, lint rules, component behavior, production build, and browser
flow with:

```sh
make console-api-check
make console-typecheck
make console-check
make console-test
make console-build
make console-e2e
```

New package ecosystems follow the capability admission gate in
[`docs/format-extension-guide.md`](docs/format-extension-guide.md); adding only
an enum, route placeholder, or Console option is not considered format support.

The high-level runtime and ownership boundaries are documented in
[`ARCHITECTURE.md`](ARCHITECTURE.md). Engineering changes follow
[`CONTRIBUTING.md`](CONTRIBUTING.md); security-sensitive findings follow
[`SECURITY.md`](SECURITY.md), and user-visible changes are collected in
[`CHANGELOG.md`](CHANGELOG.md).

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
idempotency key and a `format` of `oci`, `raw`, `maven`, `conan`, or `npm`. OCI
repositories are rooted at `/v2/<repository>/<image>/...`; Raw repositories use
`/raw/<repository>/<path>`; Maven uses `/repository/maven/<repository>/...`;
Conan 2 uses `/conan/v2/<repository>/...`; npm uses
`/npm/<repository>/<package>`.

npm Groups expose Hosted and Proxy members through the same
`/npm/<group>/` Registry URL. Their packuments merge versions and distribution
tags by member priority, with Hosted members taking precedence over Proxy
members when a version exists in more than one repository.

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
npm Proxy metadata, negative-cache, and circuit-breaker lifetimes default to
15 minutes, 10 minutes, and 30 seconds. Override them with
`GATEWAY_NPM_METADATA_CACHE_TTL`, `GATEWAY_NPM_NEGATIVE_CACHE_TTL`, and
`GATEWAY_NPM_PROXY_BREAKER_TTL`. Each npm Proxy repository requires an HTTPS
endpoint and an `allowedHosts` list covering the registry and any tarball CDN
hosts it may use.
For OIDC bearer validation, configure `GATEWAY_OIDC_ISSUER` and
`GATEWAY_OIDC_AUDIENCE`; the JWKS URL is read from provider discovery unless
explicitly configured.
To enable browser SSO through Authorization Code + PKCE, also configure
`GATEWAY_OIDC_CLIENT_ID` and `GATEWAY_OIDC_REDIRECT_URL`;
`GATEWAY_OIDC_CLIENT_SECRET` is optional for public clients. The Console reads
provider availability from `GET /auth/oidc/config` at runtime, so changing
providers does not require a frontend rebuild. Keycloak realm/client roles,
top-level roles, and GitLab-style groups can be mapped with
`GATEWAY_OIDC_READER_ROLES`, `GATEWAY_OIDC_WRITER_ROLES`, and
`GATEWAY_OIDC_ADMIN_ROLES`.
Administrators can persist and apply the same settings from the Console's
Authentication page without restarting the Gateway. Runtime client secrets are
encrypted with `GATEWAY_SETTINGS_ENCRYPTION_KEY` and are never returned by the
management API.

An authenticated client can inspect the identity used by authorization with
`GET /api/v2/identity`. The response reports a bounded credential source
(`static_admin`, `static_resolver`, `local_session`, `api_key`, or `oidc`), the
effective global role, and administrator status. For OIDC, it includes only
configured role mappings that matched the validated token and whether the
subject matched the configured administrator list; raw claims and token
material are never returned.

`GET /api/v2/repositories/{repositoryId}/effective-access` explains the same
caller's read, write, admin, and anonymous-read decisions for a known
Repository. Any authenticated caller may inspect its own denied decisions, so
operators can diagnose a missing grant without first granting Repository read
access. The endpoint cannot evaluate another actor and remains unavailable to
anonymous requests. See `docs/anonymous-access-operations.md` for the anonymous
policy gates and operational checks.

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
