# Contributing to Artifact Gateway

Artifact Gateway is currently developed by its core team. This guide records
the engineering workflow used inside the repository so changes remain
reviewable and reproducible while the project is prepared for broader
contribution.

## Development prerequisites

- Docker with Compose support
- Go at the version declared in `go.mod`
- Node.js 24 and npm
- GNU Make

Start the complete local stack from a checkout:

```sh
cp .env.example .env
# Replace every local credential placeholder before starting the stack.
make up
```

`make down` stops the stack without deleting its PostgreSQL and MinIO volumes.

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

For contract, persistence, or protocol changes, also run the matching checks:

```sh
make openapi-check
make integration-test
make native-oci-e2e
make native-raw-e2e
make native-maven-e2e
make conan-e2e
```

Run `make test` before submitting a change that affects shared backend behavior.
The CI workflow is the final source of truth for the complete required matrix.

## Test expectations

- Test externally observable behavior through an HTTP, storage, protocol, or
  exported package interface.
- Prefer one focused behavior per test and use known literal expectations.
- Add a regression test before fixing a defect when a stable public boundary is
  available.
- Do not couple tests to private helper calls merely to increase coverage.
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
