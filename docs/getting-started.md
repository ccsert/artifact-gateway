# Getting started

[简体中文](getting-started.zh-CN.md) · [Back to README](../README.md)

This guide brings up the complete development stack: one standalone Gateway,
PostgreSQL for metadata and coordination, RustFS for the S3-compatible byte
plane, and the Vite Console. No Redis, Kafka, Elasticsearch, external queue, or
service-discovery component is required.

## Prerequisites

- Docker with Compose support
- Node.js 24 or later and npm
- GNU Make
- OpenSSL
- Approximately 4 GiB of free memory for the default development stack

Go is required for backend development and the version declared by `go.mod`
should be used. `kubectl` is only required for the Kubernetes workflow.

## Install a packaged release

Version 0.1.0 is available from [GitHub Releases](https://github.com/ccsert/artifact-gateway/releases/tag/v0.1.0)
as Linux/macOS amd64/arm64 Gateway archives, a Console static bundle, resolved
OpenAPI contracts, and checksums. Verify `SHA256SUMS` before unpacking and run
`gateway version` to confirm the embedded version and Git revision.

Each Gateway archive also contains `migrations/`, `run-migrations.sh`, an
environment example, and a compact installation guide. A binary installation
requires `psql`; export `PGHOST`, `PGUSER`, `PGDATABASE`, and `PGPASSWORD`, then
apply the schema before starting Gateway:

```sh
MIGRATION_DIR=./migrations ./run-migrations.sh
```

Release containers use `ghcr.io/ccsert/artifact-gateway:0.1.0` and
`ghcr.io/ccsert/artifact-gateway-console:0.1.0`. The corresponding `main` tags
are moving CI-qualified snapshots and should not replace a pinned release tag
or digest in a controlled deployment.

## 1. Prepare the environment

```sh
make dev-bootstrap
```

The command creates `.env` from `.env.example` when the file is absent, sets
its permissions to `0600`, and generates these required local-only values:

- `GATEWAY_POSTGRES_PASSWORD`
- `GATEWAY_ADMIN_TOKEN`
- `GATEWAY_RESOLVER_TOKEN`
- `RUSTFS_ACCESS_KEY`
- `RUSTFS_SECRET_KEY`
- `RUSTFS_RPC_SECRET`

Existing non-placeholder values are never replaced. If an existing `.env`
needs changes, the helper writes atomically and keeps a rollback copy under
`.local-dev/environment-backups/`. Generated credentials are not printed.

Review `.env` if you need different ports or optional OIDC, scanner, egress
proxy, APT signer, or worker settings.

## 2. Start the stack

```sh
make dev
```

The command builds and starts PostgreSQL, RustFS, migrations, and Gateway with
Docker Compose. It also installs the lockfile-pinned Console dependencies when
needed, starts Vite under a checkout-specific supervisor, and waits for the
full stack to become ready.

Default local endpoints:

| Surface | Address |
| --- | --- |
| Console | <http://127.0.0.1:4173> |
| Gateway API | <http://127.0.0.1:8080> |
| Liveness | <http://127.0.0.1:8080/livez> |
| Readiness | <http://127.0.0.1:8080/readyz> |
| RustFS S3 API | <http://127.0.0.1:9000> |
| RustFS Console | <http://127.0.0.1:9001> |

Overrides in `.env` are reflected by `make dev-status`.

## 3. Create the first repository

1. Open the Console.
2. Read `GATEWAY_ADMIN_TOKEN` from the local `.env` file and use it at the
   administrator sign-in entry.
3. Open **Repositories**, choose **Create repository**, and select Hosted,
   Proxy, or Group plus a currently supported format.
4. After creation, use the repository detail page's client snippet for the
   correct protocol root and authentication shape.

Use Hosted for artifacts published into Gateway, Proxy for an allowlisted
upstream, and Group to expose ordered Hosted and Proxy members through one
client endpoint. The [protocol compatibility baseline](protocol-compatibility.md)
records format-specific behavior and limitations.

## Daily commands

```sh
make dev-status       # verify Console, API proxy, liveness, and readiness
make dev-down         # stop only the Console managed by this checkout
make down             # stop Compose services; preserve volumes
make dev              # start the full stack again
make test             # run the shared local regression gate
```

`make dev-down` intentionally leaves Gateway and its data services running.
Use `make down` when the whole Compose stack should stop. Neither command
deletes PostgreSQL or RustFS volumes.

## Troubleshooting

### `.env` is missing or still contains placeholders

Run `make dev-bootstrap`. The command is idempotent and does not rotate valid
existing credentials.

### Console dependencies are stale

Stop the managed Console with `make dev-down`, run
`npm --prefix console ci`, and start it again with `make dev`. Do not replace
`console/node_modules` while Vite is running.

### A local port is already in use

Change `GATEWAY_HTTP_PORT`, `GATEWAY_CONSOLE_PORT`, `GATEWAY_POSTGRES_PORT`, or
the RustFS ports in `.env`, then restart the affected services. Confirm the
effective endpoints with `make dev-status`.

### Legacy MinIO resources are detected

The current local runtime is RustFS-only. The guard fails closed when it finds
legacy MinIO containers or volumes and never deletes or mounts them. Inspect
and preserve the old data explicitly before removing or renaming legacy
resources; the helper has no in-place migration bypass.

### Gateway is live but not ready

Run `docker compose --env-file .env -f compose.yml ps` and inspect Gateway,
PostgreSQL, migration, and RustFS readiness. Readiness requires the storage
dependencies for the configured role; liveness reports only process health.

## Where to continue

- [Architecture](../ARCHITECTURE.md)
- [Documentation index](README.md)
- [Contributing workflow](../CONTRIBUTING.md)
- [Kubernetes development deployment](kubernetes-deployment.md)
- [Recovery runbook](recovery-runbook.md)
