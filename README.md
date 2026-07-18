# Artifact Gateway

## Gateway local development

Prerequisite: Docker Desktop. The Go compiler and linter run in fixed-version containers, so no host Go or `golangci-lint` installation is required.

```sh
cp .env.example .env
# Replace every replace-with-* value with local-only credentials.
make up
```

`make up` starts Gateway, PostgreSQL, Redis, and MinIO with the built-in `test` Adapter, so it does not need a live Gitea instance. The service exposes `GET /livez` for process liveness and `GET /readyz` for PostgreSQL, Redis, and S3-compatible object-storage availability. `make down` preserves local volumes.

Run `make test`, `make lint`, `make build`, or `make docker-build` from a clean checkout. `make up` runs all SQL files in `migrations/` through an isolated one-shot migration service before starting Gateway; it also waits for MinIO's built-in live endpoint through a fixed-version curl sidecar. `make migrate` reruns that idempotent service when needed.

Run `make integration-test` to start an isolated PostgreSQL container, apply every migration, and exercise the OCI Group HTTP flow against the database. Run `make integration-down` afterward to remove its containers and volume.

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

The seed process needs Docker to pull and push `busybox:1.36`, plus `curl`, `zip`, `shasum`, and `md5` from macOS. It is idempotent for users, organization, team membership, and fixture package coordinates; each run intentionally creates a fresh locally stored API token.
