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

Run `make integration-test` to start an isolated PostgreSQL container, apply every migration, and exercise the OCI Group HTTP flow against the database. It removes the test containers and volume after a successful run; `make integration-down` removes them after a failed run.

Run `make oci-e2e` after configuring `.env` to seed the local Gitea fixture, start Gateway in Gitea mode, and use Docker to log in and pull the fixture image through a Hosted Group. Run `make maven-e2e` to resolve the seeded Maven dependency through a Maven Hosted Group with both Maven and Gradle.

## OCI hosted reads

Set `GATEWAY_ADAPTER_MODE=gitea`, `GATEWAY_GITEA_USERNAME`, and `GATEWAY_GITEA_TOKEN` to let the Gateway read a Gitea Container Registry Hosted member. Create a Group whose name is the leading OCI namespace and whose first member is `hosted` with the Gitea registry base URL as its endpoint. Clients pull `gateway-host:port/<group>/<image>:<tag>` after logging in with any username and `GATEWAY_RESOLVER_TOKEN` as the password. The Gateway presents a standard Bearer challenge, forwards manifest/blob GET, HEAD, and Range requests, and uses the configured Gitea credentials upstream.

## Maven hosted reads

Create a Maven Group through `POST /api/v1/maven/groups` using the same member schema as OCI. Maven clients use `http://gateway-host:port/maven/<group>` as the repository URL and HTTP Basic authentication with any username plus `GATEWAY_RESOLVER_TOKEN` as the password. The Gateway serves POMs, JARs, checksum sidecars, and `maven-metadata.xml`; it tries Hosted members before Proxy members and records each upstream attempt in the resolver audit log.

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
