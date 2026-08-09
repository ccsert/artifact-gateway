# OIDC Browser SSO

Artifact Gateway supports two OIDC credential paths:

- bearer validation for CI and API clients;
- browser sign-in through Authorization Code + PKCE.

The Console reads `GET /auth/oidc/config` at runtime. Provider URLs and client
identifiers are therefore not compiled into the frontend bundle.

## Required configuration

```dotenv
GATEWAY_OIDC_ISSUER=https://login.example.com/realms/acme
GATEWAY_OIDC_AUDIENCE=artifact-gateway-api
GATEWAY_OIDC_CLIENT_ID=artifact-gateway-console
GATEWAY_OIDC_CLIENT_SECRET=
GATEWAY_OIDC_REDIRECT_URL=https://gateway.example.com/auth/oidc/callback
GATEWAY_OIDC_SCOPES=openid profile email
GATEWAY_SETTINGS_ENCRYPTION_KEY=<32-byte-key>
```

Set `GATEWAY_OIDC_CLIENT_SECRET` for a confidential client. Leave it empty for
a public client that requires PKCE. Issuer and JWKS endpoints must use HTTPS,
except for the loopback hosts `localhost`, `127.0.0.1`, and `::1` in local
development. The redirect URL must use HTTPS except for those same local
development callbacks. API-only bearer validation may leave both
`GATEWAY_OIDC_CLIENT_ID` and `GATEWAY_OIDC_REDIRECT_URL` empty; when browser
login is enabled, those two values must be configured together.

Environment variables are the bootstrap source. Administrators can then open
**Authentication** in the Console and save a runtime configuration. The first
save creates the singleton database record; subsequent changes use versioned
`If-Match` updates. `GATEWAY_SETTINGS_ENCRYPTION_KEY` encrypts confidential
client secrets with AES-256-GCM. Responses expose only
`clientSecretConfigured` and never return plaintext or ciphertext.

The provider discovery document must contain matching `issuer`,
`authorization_endpoint`, and `token_endpoint` values. Artifact Gateway never
stores provider access or refresh tokens. After validating the ID token,
issuer, audience, signature, expiry, state, and nonce, it creates a bounded
12-hour HttpOnly `SameSite=Lax` Gateway session cookie.

API Bearer tokens are checked against `GATEWAY_OIDC_AUDIENCE`. Browser ID
tokens are checked independently against `GATEWAY_OIDC_CLIENT_ID`, so the API
and Console may use separate audiences.

## Keycloak

Create an OpenID Connect client, enable Standard Flow, and register the exact
Gateway callback URL. The browser ID token must include the client ID in its
`aud` claim; API Bearer tokens may use the separate Gateway API audience.

Realm roles, client roles, top-level `roles`, and `groups` claims can be mapped
onto Gateway roles:

```dotenv
GATEWAY_OIDC_READER_ROLES=artifact-reader
GATEWAY_OIDC_WRITER_ROLES=artifact-writer
GATEWAY_OIDC_ADMIN_ROLES=artifact-admin
```

The highest matching Gateway role wins.

## GitLab

Register an OAuth/OpenID Connect application with the exact Gateway callback
URL, then use the GitLab issuer and application ID as the client ID. When role
mapping is required, include the relevant group claims in the ID token and map
their exact values through the same reader, writer, and administrator
variables above.

## Operational checks

`gateway preflight` reports `oidc_enabled`, `oidc_browser_login_enabled`, and
whether the runtime settings encryption key is configured. The Console can
test the saved discovery document from the Authentication page. Runtime saves
take effect immediately on the current node; other API nodes observe the new
database version after the bounded settings-cache window. No Gateway restart
or Console rebuild is required.

For a real Keycloak browser callback check in the local Kubernetes environment,
see [Keycloak Kubernetes acceptance](oidc-keycloak-k8s.md).
