# Keycloak Kubernetes Acceptance

[简体中文](oidc-keycloak-k8s.zh-CN.md) · [Documentation index](README.md)

`scripts/oidc-keycloak-k8s-e2e.sh` starts an isolated, real Keycloak fixture
in the current Kubernetes context. It also deploys a transient PostgreSQL,
RustFS, and Gateway instance, runs every migration, forwards Gateway and
Keycloak locally, starts the Console, and executes the browser SSO test.

```sh
./scripts/oidc-keycloak-k8s-e2e.sh
```

The test proves the full browser flow: discovery, Authorization Code with
PKCE, `state` and `nonce` validation, code exchange, ID-token signature and
role mapping validation, issuance of the HttpOnly Gateway session cookie, and
logout invalidation. Before browser login, the script moves the fixture from
environment bootstrap to the versioned database configuration API and tests
provider discovery through that runtime configuration.

The fixture uses a non-production Keycloak realm and an intentionally known
test account. It is deleted at the end of the run. Set
`OIDC_TEST_KEEP_NAMESPACE=1` when inspecting a failed deployment. The script
expects a Kubernetes environment whose local image store is shared with Docker
(Docker Desktop satisfies this) and requires free local ports `18080`, `8081`,
and `4173`. Override those ports with `OIDC_TEST_GATEWAY_PORT`,
`OIDC_TEST_KEYCLOAK_PORT`, and `OIDC_TEST_CONSOLE_PORT` when needed.
The script updates the test-only Keycloak issuer and browser assertions to use
the selected Keycloak port, including the Keycloak sidecar listener and its
Kubernetes Service target port.

Production issuer and JWKS endpoints must use HTTPS. HTTP is accepted only for
the loopback hosts `localhost`, `127.0.0.1`, and `::1`, enabling this local
fixture and no remote insecure provider.
