# Artifact Gateway

Artifact Gateway serves native OCI, Raw, Maven, Conan 2, npm, and PyPI Hosted
repositories plus Go Module and APT Proxy repositories, using PostgreSQL for lifecycle
metadata and S3-compatible object storage for verified bytes. The bundled local
stack uses RustFS. Legacy Groups
remain available for allowlisted external Proxy reads, while V2 Groups resolve
managed members.

> **Project status:** active development. The core team is hardening contracts,
> operations, and contributor-facing engineering practices before a stable
> public release. Do not infer production support commitments from the current
> package version.

Artifact Gateway is intended to be a complete repository manager for the six
Hosted-capable formats: native client reads and publication, Hosted/Proxy/Group
resolution, browsing and search, authorization, audit, retention, recovery,
promotion, and replication. Go and APT are admitted under the protocol-only
format rule: they declare Proxy and Group capabilities until a standard or
trusted publication workflow exists. It is not a transparent rewrite proxy, a generic
object browser, or a vulnerability scanner.

## Local development

```sh
cp .env.example .env
# Replace local-only credential placeholders.
make dev
```

`make dev` builds and starts Gateway, PostgreSQL, and RustFS, then starts the
Vite Console under a checkout-specific local supervisor. It waits for both
Gateway readiness and the Console before printing their configured addresses.
Use `make dev-status` as a repeatable health check.

For an existing MinIO-era `.env`, first run
`./scripts/local-dev.sh migrate-rustfs-env`. It adds independent local RustFS
credentials without printing them and retains a timestamped rollback copy.
Existing MinIO data is never mounted in place or discarded; follow the
[S3 cutover procedure](docs/rustfs-migration.md), then record the verified
manifest with `./scripts/local-dev.sh confirm-rustfs-migration sha256:...`.

Open the Console at `http://127.0.0.1:4173` by default. The checked-in example
uses `GATEWAY_HTTP_PORT=8080` and `GATEWAY_CONSOLE_PORT=4173`; local `.env`
overrides are reflected in the status output.

```sh
make dev-status
make dev-down
```

`make dev-down` stops only the Console managed for this checkout; Gateway and
its data volumes remain running. `make down` stops the Compose services while
preserving local volumes.

### Local Kubernetes

The repository also includes an executable Kustomize baseline for a local
Kubernetes cluster:

```sh
make kubernetes-local-check
make kubernetes-local-up
make kubernetes-local-status
make kubernetes-local-verify
```

It exposes the Console and every same-origin protocol/API route through a local
Ingress at `http://artifact-gateway.localhost`; `http://127.0.0.1:18081`
remains a direct Console fallback. The overlay provisions single-node PostgreSQL and
RustFS storage for local validation and is intentionally not a production
topology. See the [Kubernetes deployment guide](docs/kubernetes-deployment.md)
for credential overrides, data deletion behavior, architecture, and the
remaining production deployment work.

For the Compose workflow, use `GATEWAY_ADMIN_TOKEN` from the local `.env` file.
The Kubernetes helper prints its effective local administrator token after
startup and uses `local-gateway-admin-token` only as a disposable default.
Anonymous browse remains available at `/browse` when the global policy and
repository policy both permit it.

### 单机与集群运行模式

Gateway 默认以 `GATEWAY_NODE_ROLES=standalone` 启动，同时提供协议 API、
周期调度和后台 worker。生产环境可以使用同一个镜像拆分节点职责：

```text
GATEWAY_NODE_ROLES=api
GATEWAY_NODE_ROLES=scheduler
GATEWAY_NODE_ROLES=worker GATEWAY_WORKER_FORMATS=oci
```

所有节点共享 PostgreSQL 和 S3 兼容对象存储；任务领取由数据库租约保证幂等。worker
可以用 `GATEWAY_WORKER_FORMATS` 和 `GATEWAY_WORKER_KINDS` 限制格式与任务类型，
例如只部署 OCI 的 `reclaim,replication` worker，或将 `scan` 交给配置了外部
扫描器的隔离 worker；`webhook` 是不受格式过滤器影响的全局投递任务。非 API 节点仅暴露
`/livez`、`/readyz` 和 `/metrics`，不会暴露制品协议或管理接口。

拆分部署时应按副本数降低每个节点的数据库连接池上限，避免连接总数超过
PostgreSQL 的 `max_connections`。

Run `make test`, `make lint`, `make build`, or `make docker-build` from a clean
checkout. `make integration-test` creates isolated PostgreSQL and RustFS
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
make native-pypi-e2e
make native-go-e2e
make native-apt-e2e
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

Local administrator-managed accounts support profile metadata, failed-login
lockout, mandatory password changes, password reset, and immediate session
revocation. Administrators can inspect active and retained session metadata,
identify the current client, and revoke one client without signing out the
account everywhere. Configure the lock threshold with
`GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS` (default `5`) and the lock interval
with `GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION` (default `15m`). See the
[local user governance guide](docs/user-governance.md) for API behavior,
operator checks, and current limitations.

## OpenAPI contract workflow

The editable Native Hosted contract starts at
`api/openapi/native-hosted.yaml`. Shared components, management routes, and
protocol overlays live in its sibling YAML directories. Do not edit
`api/openapi/native-hosted-v1.json`, `console/src/client`, or
`internal/admin/openapi/generated.go` by hand: they are generated artifacts.

```sh
npm --prefix tools/openapi ci --ignore-scripts --no-audit --no-fund
npm --prefix console ci --ignore-scripts --no-audit --no-fund
make openapi-bundle
make openapi-generate-admin
make openapi-check
```

`make openapi-check` rebuilds the public JSON bundle, the generated Console
client, and the generated repository-management Go contract; it then fails if
any generated artifact differs from the worktree.

The check validates but does not reinstall Node.js dependencies. When a lockfile
changes, stop a running local Console with `make dev-down`, run the two pinned
install commands above, and restart with `make dev`. This keeps Vite's optimized
dependency cache consistent with `node_modules`.

After a Gateway route or generated management contract changes, rebuild the
running development service before testing the Console. Vite reloads the
Console independently and can otherwise call a stale Gateway image.

```sh
docker compose up --build -d gateway
docker compose ps gateway
```

## Native Hosted repositories

Administrators create repositories through `POST /api/v2/repositories` with an
idempotency key and a `format` of `oci`, `raw`, `maven`, `conan`, `npm`,
`pypi`, `go`, or `apt`. Go and APT repositories may only use the Proxy type. OCI
repositories are rooted at `/v2/<repository>/<image>/...`; Raw repositories use
`/raw/<repository>/<path>`; Maven uses `/repository/maven/<repository>/...`;
Conan 2 uses `/conan/v2/<repository>/...`; npm uses
`/npm/<repository>/<package>`; PyPI exposes twine uploads at
`/pypi/<repository>/legacy/` and the pip Simple API at
`/pypi/<repository>/simple/`; Go uses
`/go/<repository>/<escaped-module>/@v/...` as its `GOPROXY` root; APT uses
`/apt/<repository>/dists/...` and `/apt/<repository>/pool/...` without rewriting
signed upstream metadata.
Twine authenticates with any non-empty username and the configured resolver
token as its Basic-auth password. Anonymous-enabled repositories expose the
Simple API without credentials.

npm Groups expose Hosted and Proxy members through the same
`/npm/<group>/` Registry URL. Their packuments merge versions and distribution
tags by member priority, with Hosted members taking precedence over Proxy
members when a version exists in more than one repository.
PyPI Groups expose the same Simple API, merge Hosted and Proxy distribution
files with Hosted-first conflict resolution, and keep cached Proxy files
installable while the upstream is unavailable.
Go Groups merge member version lists and resolve `.info`, `.mod`, and `.zip`
assets by member priority while preserving offline reads from verified cache.
APT Groups resolve signed metadata and packages from Proxy members in configured
order and keep cached bytes available when an upstream is unavailable.

OCI supports blob upload, resumable PATCH, mounting, manifest/tag publication,
GET/HEAD, byte ranges, and manifest deletion. Raw supports PUT, GET, HEAD,
single byte ranges, and DELETE. Clients authenticate using the normal resolver
Bearer token flow. Metadata and coordination are stored in PostgreSQL, while
RustFS stores only content-addressed object bytes through the S3 contract.

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
Each Go Proxy repository likewise requires an endpoint and `allowedHosts`; it
supports `@v/list`, `@latest`, `.info`, `.mod`, and `.zip` with immutable
SHA-256 cache validation.
Each APT Proxy repository requires an endpoint and `allowedHosts`; see
[`docs/apt-proxy.md`](docs/apt-proxy.md) for source configuration and route
security rules. Hosted publication is scheduled through the staged
[APT Hosted roadmap](docs/apt-hosted-roadmap.md); it remains unadvertised until
the signed-snapshot acceptance gates pass.
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
