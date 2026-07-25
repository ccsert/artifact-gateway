# Gitea Back-End Reference And Next Objective

> The operational objective in this document has been superseded as the
> product's strategic boundary by the [full artifact repository roadmap](full-artifact-repository-roadmap.md).
> Preflight and evidence collection remain required release infrastructure.

## Reference Baseline

This analysis uses a local, read-only checkout of Gitea `v1.24.7`, source
revision `99053ce4fa2b45f1bca5837418c0c57f793ca824`. It lives outside this
repository at `../gitea-reference-v1.24.7` and must not be committed here.

Gitea is MIT licensed. Its implementation is a design and test reference only:
new Artifact Gateway code must be independently written, preserve applicable
copyright notices when required, and must not be copied wholesale.

## Current Position

Artifact Gateway already has the core runtime needed for OCI, Maven, Raw, and
Conan reads:

- PostgreSQL metadata and MinIO-compatible object storage;
- protocol-owned cache and maintenance behavior, composed in `internal/app`;
- configured proxy allowlists, quotas, OIDC validation, repository grants,
  audit records, metrics, readiness, and asynchronous cache collection;
- isolated integration, protocol, upgrade, and backup/restore release gates.

The local gates prove code behavior in an isolated Docker environment. They do
not prove that a deployed instance has correct credentials, external
dependencies, TLS termination, policy configuration, or recoverability within
the stated RPO/RTO. That is the remaining back-end risk.

## Relevant Gitea Patterns

| Gitea reference | What it demonstrates | Artifact Gateway decision |
| --- | --- | --- |
| `cmd/doctor.go`, `services/doctor/doctor.go` | Named, ordered checks; basic configuration is checked before DB/storage; checks can be listed and selective checks can abort. | Add a read-only preflight command with named checks and machine-readable output. Do not add an HTTP route. |
| `modules/storage/storage.go` | One storage interface supports lifecycle and migration tooling. | Keep the existing S3 object-store boundary. Do not generalize storage providers until there is a concrete product requirement. |
| `cmd/migrate_storage.go` | State-changing storage work is a deliberate CLI operation, separate from normal serving. | Keep backup, restore, and any future repair/remediation as explicit operator commands, never as preflight side effects. |
| `routers/api/packages/*` | Protocol handlers retain format-specific validation and response semantics. | Preserve the current protocol ownership in `internal/protocol/{oci,maven,raw,conan}` and avoid a shared generic protocol handler. |

## Objective

**Production-readiness evidence loop:** without changing the database schema,
V1/V2 routes, authentication/authorization semantics, or protocol responses,
provide a repeatable operator workflow that proves a target deployment is safe
to release and that its recovery objectives can be evidenced.

### In Scope

1. A CLI-first `release preflight` with stable named checks and JSON output.
   It validates configuration shape and connectivity for PostgreSQL, object
   storage, OIDC/JWKS when enabled, the gateway readiness endpoint, configured
   proxy allowlists, cache quotas, and repository grants. Output must redact
   secrets, tokens, query passwords, and full upstream paths.
2. A production evidence collector that reads existing `/readyz`, `/metrics`,
   `/api/v1/audits`, and `/api/v1/operations/cache` endpoints using supplied
   operator credentials, redacts output, and emits a directory suitable for
   linking from the release record.
3. A controlled recovery-drill wrapper that orchestrates the existing backup
   and restore steps, records timestamps and checksums, then verifies both an
   allowed and denied repository read. It must refuse to run unless explicitly
   marked as an isolated drill environment.

### Explicit Non-Goals

- No Console work, protocol expansion, publishing, replication, or deletion
  workflows.
- No new public HTTP management endpoint and no V1/V2 alias or OpenAPI change.
- No automatic repair, migration, object deletion, grant modification, or
  credential rotation from preflight/evidence commands.
- No new storage-provider abstraction or schema migration in this objective.

## Delivery Order And Exit Criteria

1. **Preflight foundation.** Define a small check registry with `list`,
   `run`, and `--format json`; establish exit-code semantics and unit tests for
   redaction and failure classification. Exit: a bad dependency/configuration
   produces a named failed check without exposing credentials.
2. **Target checks.** Implement PostgreSQL, S3, OIDC, TLS, policy, and existing
   endpoint checks as read-only operations. Exit: an integration fixture covers
   success, unavailable dependency, invalid OIDC/TLS setting, and redaction.
3. **Evidence collector.** Write a timestamped manifest and redacted endpoint
   snapshots matching `docs/release-record-template.md`. Exit: a fixture proves
   all records are attributable to the target revision and contain no supplied
   secret or bearer token.
4. **Recovery drill orchestration.** Wrap the existing scripts with explicit
   safety confirmation and capture RPO/RTO evidence. Exit: an isolated drill
   verifies readiness, one granted read, one denied read, audits, and checksum
   integrity after restore.
5. **Release adoption.** Add only the new backend commands to the readiness
   checklist and record template. Exit: a production release record links to a
   preflight/evidence artifact and a completed, separately authorized drill.

## Architecture Boundaries

The command package may depend on `internal/config`, repository/object-store
constructors, and HTTP clients. It must not import protocol handlers or mutate
their stores. `internal/app` remains the composition root. Protocol caches and
their lifecycle behavior remain in their owning `internal/protocol` packages.

Production access, operator credential source, evidence retention location,
and the isolated-drill marker are deployment decisions. They must be supplied
before implementing stages 2 through 4; local Docker results are not a
substitute for those production records.

## Stage 1 Status

The preflight foundation is available in the Gateway binary:

```sh
gateway preflight list
gateway preflight run --format json
gateway preflight run --check postgres --check object_store --format json
```

`make preflight` runs the command in the existing Compose Gateway environment;
start the local stack with `make up` first. The command performs configuration,
policy, PostgreSQL, object-store, and optional OIDC/JWKS checks. It returns `0`
for pass/skip-only reports, `1` for a failed check, and `2` for invalid CLI
usage. Its JSON report deliberately excludes credentials, token values, full
database URLs, and dependency error text.

## Stage 2 Status

The evidence collector reads the existing readiness, metrics, audit, and cache
operations endpoints. It creates `manifest.json` plus one redacted JSON record
per endpoint:

```sh
export GATEWAY_EVIDENCE_ADMIN_TOKEN='injected-at-runtime'
make evidence \
  GATEWAY_URL='https://gateway.example.test' \
  EVIDENCE_OUTPUT='.artifacts/release-evidence/20260725T000000Z' \
  GIT_REVISION='candidate-revision' \
  IMAGE_DIGEST='registry.example/gateway@sha256:...'
```

The output directory must be empty. The manifest hashes the target URL rather
than storing it. Metrics are reduced to a fixed allowlist of aggregate values;
audits become outcome/format counts; cache errors are omitted. The token, URL,
audit actor/path/upstream fields, and response error bodies are never written.
