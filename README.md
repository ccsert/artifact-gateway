<p align="center">
  <img src="docs/assets/artifact-gateway-hero.png" alt="Artifact streams converging through one verification gateway into PostgreSQL metadata and immutable object storage" width="100%">
</p>

<h1 align="center">Artifact Gateway</h1>

<p align="center">
  A lightweight, protocol-native repository for verified software artifacts.
</p>

<p align="center">
  <a href="README.zh-CN.md">简体中文</a> ·
  <a href="docs/README.md">Documentation</a> ·
  <a href="ARCHITECTURE.md">Architecture</a> ·
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

<p align="center">
  <a href="https://github.com/ccsert/artifact-gateway/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/ccsert/artifact-gateway/actions/workflows/ci.yml/badge.svg"></a>
  <img alt="Go 1.26.6" src="https://img.shields.io/badge/Go-1.26.6-00ADD8?logo=go&logoColor=white">
  <img alt="PostgreSQL 16" src="https://img.shields.io/badge/PostgreSQL-16-4169E1?logo=postgresql&logoColor=white">
  <img alt="S3 compatible" src="https://img.shields.io/badge/Object_storage-S3_compatible-06B6D4">
  <img alt="Status: preparing" src="https://img.shields.io/badge/status-preparing-6B7280">
</p>

> [!IMPORTANT]
> **Project status: preparation stage.** The core team is still hardening
> contracts, operations, documentation, and contribution workflows. This
> repository is not yet a stable public release or a formally distributed
> product, and the current package version does not imply support commitments.

## Why Artifact Gateway

Artifact Gateway keeps the operational control plane intentionally small:

- **One Gateway binary.** The same image can run as a compact standalone node
  or split into API, scheduler, and worker roles.
- **PostgreSQL is the only coordination and database dependency.** Repository
  state, authorization, lifecycle jobs, leases, locks, idempotency, audit, and
  operational coordination all use PostgreSQL.
- **No Redis, Kafka, Elasticsearch, or external message queue is required.**
- **Measured Go footprint.** The current local Docker baseline produced a
  28.88 MiB Linux/arm64 Gateway binary and a 36.06 MiB runtime image. Gateway
  averaged 53.59 MiB at quiet idle and peaked near 104 MiB while serving 128
  concurrent clients; see the [performance baseline](docs/performance-baseline.md)
  for throughput, full-stack memory, methodology, and limits.
- **Artifact bytes stay outside the database.** Verified immutable bytes use
  an S3-compatible object-storage interface; the local stack bundles RustFS.
- **Native protocols remain first-class.** Clients use familiar registry and
  package-manager routes instead of a generic upload-only object browser.

In short: PostgreSQL owns the control plane, S3-compatible storage owns the
byte plane, and Gateway connects the two without adding a middleware fleet.

## Repository capabilities

| Format | Hosted | Proxy | Group | Notes |
| --- | :---: | :---: | :---: | --- |
| OCI | ✓ | ✓ | ✓ | Registry V2 uploads, manifests, tags, ranges, and referrers |
| Raw | ✓ | ✓ | ✓ | PUT/GET/HEAD, ranges, checksums, and resumable upload |
| Maven | ✓ | ✓ | ✓ | Maven/Gradle staging with explicit coordinate commit |
| Conan 2 | ✓ | ✓ | ✓ | Revision-aware publication and lifecycle |
| npm | ✓ | ✓ | ✓ | Native publication, verified cache, merged packuments |
| PyPI | ✓ | ✓ | ✓ | twine upload plus PEP 503/691 reads |
| Go modules | Gateway extension | ✓ | ✓ | Standard GOPROXY reads; atomic single-ZIP Hosted publication |
| APT | Preview only | ✓ | ✓ | Hosted signing remains unadvertised until production custody gates pass |

Cargo is a staged parser/identity foundation and NuGet remains roadmap work;
neither is advertised as a usable repository format. The detailed, test-bound
compatibility statement lives in the
[protocol compatibility baseline](docs/protocol-compatibility.md).

Beyond protocol reads and writes, the current foundation includes repository
grants, local users and OIDC, service accounts, anonymous-read policy, audit,
search and browse, retention, recoverable deletion, promotion, replication,
webhooks, scanner integration, quarantine, diagnostics, metrics, and backup /
restore workflows. Each area is tracked from the [documentation index](docs/README.md).

## Quick local start

Prerequisites: Docker with Compose, Node.js 24+, npm, GNU Make, and OpenSSL.

```sh
git clone https://github.com/ccsert/artifact-gateway.git
cd artifact-gateway
make dev-bootstrap
make dev
```

`make dev-bootstrap` creates a private `.env` when needed and generates only
the six credentials required by the local Gateway/PostgreSQL/RustFS stack. It
does not print secrets, overwrite real values, or create a new rollback copy
when no change is needed.

`make dev` builds and starts Gateway, PostgreSQL, and RustFS, installs pinned
Console dependencies when absent, and waits for both the API and Console.

Open the Console at <http://127.0.0.1:4173>. Sign in with the
`GATEWAY_ADMIN_TOKEN` stored in `.env`, then create a Hosted, Proxy, or Group
repository from the Repositories page.

```sh
make dev-status   # Console, proxy, liveness, and readiness checks
make dev-down     # stop only the checkout-managed Console
make down         # stop Compose services and preserve data volumes
```

See [Getting started](docs/getting-started.md) for credential handling,
first-repository guidance, ports, lifecycle commands, and troubleshooting.

## Architecture at a glance

![Artifact Gateway lightweight system architecture](docs/assets/artifact-gateway-system-architecture.png)

The default `standalone` role runs API, scheduler, and worker responsibilities
in one process. Larger installations can split the same image by role without
introducing a separate queue or service-discovery dependency. See
[Architecture](ARCHITECTURE.md), [Architecture diagrams](docs/architecture-diagrams.md),
and [PostgreSQL capabilities](docs/postgresql-capabilities.en.md).

## Documentation map

| Need | Start here |
| --- | --- |
| Set up a local checkout | [Getting started](docs/getting-started.md) |
| Understand protocol behavior | [Protocol compatibility](docs/protocol-compatibility.md) |
| Understand core boundaries | [Architecture](ARCHITECTURE.md) |
| Explore system and publication flows | [Architecture diagrams](docs/architecture-diagrams.md) |
| Understand PostgreSQL coordination | [PostgreSQL capabilities](docs/postgresql-capabilities.en.md) |
| Review size, memory, and local concurrency | [Performance baseline](docs/performance-baseline.md) |
| Review current engineering quality | [Project quality assessment](docs/project-quality-assessment.md) |
| Operate identity and access | [User governance](docs/user-governance.md), [OIDC SSO](docs/oidc-sso.md), [Service accounts](docs/service-account-operations.md) |
| Deploy or recover | [Kubernetes](docs/kubernetes-deployment.md), [Distributed deployment](docs/distributed-deployment.md), [Recovery runbook](docs/recovery-runbook.md) |
| Extend a package format | [Format extension guide](docs/format-extension-guide.md) |
| Change the management API | [OpenAPI governance](docs/openapi-governance-plan.md) |
| Browse every maintained guide | [Documentation index](docs/README.md) |

## Development workflow

```sh
make test
make lint
make vet
make coverage
make build
```

Protocol, persistence, Console, and deployment changes have additional focused
gates. Generated OpenAPI clients and server contracts must not be edited by
hand. Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing code, and record
user-visible behavior under `Unreleased` in [CHANGELOG.md](CHANGELOG.md).

New package ecosystems must pass the admission rules in the
[format extension guide](docs/format-extension-guide.md). Adding an enum, route
placeholder, or Console option alone is not considered protocol support.
