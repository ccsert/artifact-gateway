# Artifact Gateway

## Gateway local development

Prerequisites: Docker Desktop and `python3` for the Maven Proxy E2E fixture. The Go compiler and linter run in fixed-version containers, so no host Go or `golangci-lint` installation is required.

```sh
cp .env.example .env
# Replace every replace-with-* value with local-only credentials.
make up
```

`make up` starts Gateway, PostgreSQL, Redis, and MinIO with the built-in `test` Adapter, so it does not need a live Gitea instance. The service exposes `GET /livez` for process liveness and `GET /readyz` for PostgreSQL, Redis, and S3-compatible object-storage availability. `make down` preserves local volumes.

Run `make test`, `make lint`, `make build`, or `make docker-build` from a clean checkout. `make up` runs all SQL files in `migrations/` through an isolated one-shot migration service before starting Gateway; it also waits for MinIO's built-in live endpoint through a fixed-version curl sidecar. `make migrate` reruns that idempotent service when needed.

Run `make integration-test` to start an isolated PostgreSQL container, apply every migration, and exercise the OCI Group HTTP flow against the database. It removes the test containers and volume after a successful run; `make integration-down` removes them after a failed run.

Run `make oci-e2e` after configuring `.env` to seed the local Gitea fixture, start Gateway in Gitea mode, and use Docker to log in and pull the fixture image through a Hosted Group. Run `make maven-e2e` to resolve the seeded Maven dependency through a controlled Maven Proxy: Maven performs the first upstream read, Gradle resolves from the Gateway cache after the fixture upstream stops, and the fixture exercises retry, negative-cache, allowlist, invalidation, and metrics paths.

Administrators can query resolver audit entries using `GET /api/v1/audits` with
an administrator bearer token. Optional `group`, `repository`, and `limit`
parameters narrow the newest-first result set. The `/metrics` endpoint exposes
resolver outcomes plus OCI and Maven cache and upstream counters. See
[`docs/recovery-runbook.md`](docs/recovery-runbook.md) for the backup and
restore drill procedure.

`GET /api/v1/operations/cache` is an administrator-only operational view of
OCI cache capacity and the asynchronous retention collector. It reports the
number and size of digest objects, pending grace-period candidates, and the
last cleanup result. The process runs cleanup every five minutes; object
removal is delayed by the cache TTL grace period so active indexes remain
readable.

## OCI hosted reads

Set `GATEWAY_ADAPTER_MODE=gitea`, `GATEWAY_GITEA_USERNAME`, and `GATEWAY_GITEA_TOKEN` to let the Gateway read a Gitea Container Registry Hosted member. Create a Group whose name is the leading OCI namespace and whose first member is `hosted` with the Gitea registry base URL as its endpoint. Clients pull `gateway-host:port/<group>/<image>:<tag>` after logging in with any username and `GATEWAY_RESOLVER_TOKEN` as the password. The Gateway presents a standard Bearer challenge, forwards manifest/blob GET, HEAD, and Range requests, and uses the configured Gitea credentials upstream.

## Maven hosted reads

Create a Maven Group through `POST /api/v1/maven/groups` using the same member schema as OCI. Maven clients use `http://gateway-host:port/maven/<group>` as the repository URL and HTTP Basic authentication with any username plus `GATEWAY_RESOLVER_TOKEN` as the password. The Gateway serves POMs, JARs, checksum sidecars, and `maven-metadata.xml`; it tries Hosted members before Proxy members and records each upstream attempt in the resolver audit log. Set `GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS` to a comma-separated allowlist before enabling external Maven Proxy members. Proxy component files are cached for 15 minutes, version metadata for one minute, and cached misses for one minute.

Set `GATEWAY_REPOSITORY_READERS` to enforce repository-scoped reads. Its format is semicolon-separated actor grants: `actor=repository-pattern|repository-pattern`; a pattern ending in `/*` matches that repository prefix. For example, `ci=team/*;release=public/base` permits the `ci` identity to read all `team` repositories and `release` only `public/base`. The Bearer token subject and Maven Basic username are the actor. Leave it unset only for local development: when configured, unmatched identities are denied and the denial is recorded as `access_denied` in the audit log.

Set `GATEWAY_REPOSITORY_CACHE_QUOTAS` to bound read-through cache retention per
logical repository, using semicolon-separated byte limits such as
`team/app=1073741824;engineering=536870912`. OCI entries use the OCI repository
name and Maven entries use the configured Maven Group name. When a limit is
full, the Gateway serves the verified upstream response but does not retain a
new cache index; `/metrics` increments `artifact_gateway_cache_quota_rejections_total`.

For production identity integration, set `GATEWAY_OIDC_ISSUER` and
`GATEWAY_OIDC_AUDIENCE`. The Gateway validates HTTPS JWKS-backed RS256 bearer
tokens and uses the OIDC `sub` claim as the repository authorization identity.
`GATEWAY_OIDC_JWKS_URL` defaults to `<issuer>/.well-known/jwks.json`; list
administrator subjects in `GATEWAY_OIDC_ADMIN_SUBJECTS`. Static admin and
resolver tokens remain supported for local development and break-glass access.

Set `GATEWAY_OTLP_HTTP_ENDPOINT` to export OpenTelemetry traces to an OTLP/HTTP
collector and tune head sampling with `GATEWAY_OTEL_SAMPLING_RATIO` (`0` to
`1`). Gateway request spans propagate W3C trace context to configured upstream
OCI and Maven HTTP calls.

## Gitea development fixture

The local Gitea fixture runs independently from the Gateway and exposes both package protocols through one HTTP address. It uses PostgreSQL and named Docker volumes; no repository data, password, or API token is checked in.

## Start and reset

```sh
cp .env.example .env
# Replace every replace-with-* value with local-only values.
make gitea-fixture
```

`make gitea-up` starts Gitea and waits for its health endpoint. `make gitea-seed` can be rerun after it starts. `make gitea-down` stops containers while preserving their data. `make gitea-reset` removes containers, named volumes, and generated connection data; run `make gitea-fixture` afterward for a clean environment.

The seed script creates a non-admin user and grants it only the fixture organization's package-writer team. Its credentials therefore have no instance-administrator permission; the separate local admin identity is used only to bootstrap the organization and its team.

## Test connection data

After seeding, source `.gitea-fixture/connection.env` from an integration-test setup. It is mode `0600`, ignored by Git, and contains the generated fixture API token. The values identify:

- OCI registry: `GITEA_OCI_REGISTRY`, with image `GITEA_OCI_IMAGE` (`localhost:3000/gateway-fixtures/gateway-fixture:1.0.0`). OCI's protocol probe is `http://${GITEA_OCI_REGISTRY}/v2/`.
- Maven repository: `GITEA_MAVEN_REGISTRY`, with `com.example.gatewayfixture:sample-library:1.0.0`, including POM, JAR, `.sha1`, `.sha256`, and `.md5` files.
- Minimal client identity: `GITEA_FIXTURE_USERNAME`, `GITEA_FIXTURE_PASSWORD`, and `GITEA_FIXTURE_TOKEN`. The generated token has only `read:package` and `write:package` scopes.

The seed process needs Docker to pull and push `busybox:1.36`, plus `curl`, `python3`, `zip`, `shasum`, and `md5` from macOS. It is idempotent for users, organization, team membership, and fixture package coordinates; each run intentionally creates a fresh locally stored API token.
