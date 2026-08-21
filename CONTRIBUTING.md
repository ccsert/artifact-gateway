# Contributing to Artifact Gateway

[简体中文](CONTRIBUTING.zh-CN.md)

Artifact Gateway is currently developed by its core team. This guide records
the engineering workflow used inside the repository so changes remain
reviewable and reproducible while the project is prepared for broader
contribution.

## Development prerequisites

- Docker with Compose support
- `kubectl`, `jq`, and a local Kubernetes cluster when changing deployment
  resources
- Go at the version declared in `go.mod`
- Rust/Cargo 1.96.0 when changing the staged Cargo protocol foundation
- Node.js 24 and npm
- GNU Make
- OpenSSL

Start the complete local stack from a checkout:

```sh
make dev-bootstrap
make dev
```

`make dev-bootstrap` creates a private `.env` when needed and generates only
the credentials required by the local Gateway, PostgreSQL, and RustFS stack.
It preserves existing real values and is safe to run again. See the
[getting-started guide](docs/getting-started.md) for the complete workflow.

`make dev-status` verifies the Console, its API proxy, and Gateway health.
`make dev-down` stops only the checkout-managed Console; `make down` stops the
Compose stack without deleting its PostgreSQL and RustFS volumes.

## Change workflow

1. Keep a change focused on one behavior or engineering concern.
2. Add or update tests at the public boundary affected by the change.
3. Update the OpenAPI source before generated clients or server interfaces.
4. Add a migration for schema changes; never edit an applied migration.
5. Record user-visible behavior changes under `Unreleased` in `CHANGELOG.md`.
6. Run the smallest relevant check while developing, then the required checks
   below before committing.

Generated files must not be edited by hand. The editable OpenAPI root is
`api/openapi/native-hosted.yaml`; `make openapi-check` regenerates and verifies
the JSON bundle, Console client, and Go management contract.

Install the pinned generators before running the contract check:

```sh
npm --prefix tools/openapi ci --ignore-scripts --no-audit --no-fund
npm --prefix console ci --ignore-scripts --no-audit --no-fund
```

Do not reinstall Console dependencies while `make dev` is serving Vite. Stop
the managed Console with `make dev-down`, install dependencies, then restart it
with `make dev`; replacing `node_modules` under a live Vite process invalidates
its optimized dependency URLs.

## Required checks

For a backend-only change:

```sh
go test ./path/to/changed/package
make lint
make vet
make coverage
```

For a Console change:

```sh
npm --prefix console ci
make console-check
make console-test
make console-build
```

`make coverage` enforces both the repository-wide Go baseline and stricter
floors for stable security, lifecycle, replication, and scanning packages.
`make console-test` measures all hand-written Console TypeScript and TSX while
excluding only the generated API client and test setup. Coverage floors are
non-regression guards: raise them with meaningful public-boundary tests and do
not lower them to make a change pass.

For contract, persistence, or protocol changes, also run the matching checks:

```sh
make openapi-check
make integration-test
make native-oci-e2e
make native-raw-e2e
make native-maven-e2e
make conan-e2e
make cargo-contract
```

For Kubernetes manifests, Console container routing, or local deployment
helpers, run the offline render gate before using a live cluster:

```sh
make kubernetes-local-check
make console-docker-build
```

When Docker Desktop Kubernetes is available, also run
`make kubernetes-local-up`, `make kubernetes-local-status`, and
`make kubernetes-local-verify`. The verification publishes a unique Raw object,
restarts Gateway, and reads both its PostgreSQL Repository record and object
bytes back. Record the live evidence and use `make kubernetes-local-down` only
when deleting the namespace and its local PVC data is intended.

Run `make test` before submitting a change that affects shared backend behavior.
Run `make docs-check` when changing Markdown entry points, capability claims,
or local assets; it enforces bilingual entry-point links and validates local
documentation references.
The CI workflow is the final source of truth for the complete required matrix.

## Documentation conventions

Every published Markdown document has an English canonical route and a
Simplified Chinese companion. English uses the unsuffixed `.md` filename;
Chinese uses `.zh-CN.md`. Both pages link to each other and to the localized
documentation index.

Add every document pair to `docs/site-map.json` under one of the six stable
sections. The map is the framework-neutral navigation contract for a future
Docusaurus, VitePress, or MkDocs site; do not maintain framework-specific
frontmatter as a second navigation authority.

Keep translated pages behaviorally equivalent. Configuration keys, routes,
status codes, commands, compatibility limits, preview status, and security
boundaries must not disappear in one locale. A short language stub or a link
to the other language is not a translated body.

Run `make docs-check` after adding, renaming, or moving a page. It validates
local links, language pairs, reciprocal links, localized titles, and complete
site-map coverage. See the
[documentation site guide](docs/documentation-site-guide.md).

## Test expectations

- Test externally observable behavior through an HTTP, storage, protocol, or
  exported package interface.
- Prefer one focused behavior per test and use known literal expectations.
- Add a regression test before fixing a defect when a stable public boundary is
  available.
- Do not couple tests to private helper calls merely to increase coverage.
- Do not narrow coverage collection to a small allowlist of well-tested files;
  exclusions must be generated code, fixtures, or test infrastructure.
- Use integration tests for PostgreSQL transaction, migration, locking, and
  object-store behavior that the in-memory stores cannot prove.

## Database migrations

Migrations are append-only and are applied before Gateway nodes start. The
database records both migration filenames and SHA-256 checksums in
`artifact_gateway_schema_migrations`. Validate a migration with:

```sh
make integration-test
```

The integration suite applies all migrations to a clean PostgreSQL database
and verifies that a second run is a no-op.

## Commit messages

Use an imperative Conventional Commit subject:

```text
feat(search): add checksum lookup
fix(console): preserve selected artifact version
test(storage): cover expired worker leases
docs: describe clustered runtime roles
```

Choose the narrowest useful scope. Keep generated output in the same commit as
the contract or source change that produced it.
