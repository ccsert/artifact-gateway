# OIDC Browser SSO

[简体中文](oidc-sso.zh-CN.md) | [Documentation index](README.md)

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

When the OIDC identity is linked to a local account, the signed cookie contains
a random session identifier backed by server-side metadata. The database stores
the identifier, account, login kind, bounded client address and user agent, and
lifecycle timestamps, but never the cookie, access token, refresh token, or ID
token. Administrators can inspect and revoke individual sessions from the user
detail view. `POST /auth/logout` revokes the current linked session before
clearing the browser cookie.

API Bearer tokens are checked against `GATEWAY_OIDC_AUDIENCE`. Browser ID
tokens are checked independently against `GATEWAY_OIDC_CLIENT_ID`, so the API
and Console may use separate audiences.

## Local account linkage

Validated OIDC identities can be bound to local accounts by normalized issuer
and stable `sub`. Administrators may manage links from the Users page. The
runtime Authentication settings can also enable just-in-time account creation,
choose its default role, and opt into linking an existing account by a unique
verified email claim. These provisioning options default to disabled.

Once linked, both OIDC Bearer and browser-session authentication use the local
account's current role and security state. Disabling the account, requiring a
password change, or incrementing its session version therefore takes effect on
the next authenticated request. Linked browser sessions additionally require an
active, unexpired server-side session record. An unlinked identity continues to
use the external-principal behavior and a stateless browser session when JIT
provisioning is disabled.

### Register users before granting access

To manage all sign-ins from the Gateway Users page without granting automatic
repository access:

1. In **Authentication**, set **Unlinked identities** to **Provision on first sign-in**
   and **JIT default role** to **none · awaiting approval**. Leave verified-email
   linking off unless existing-account merging is explicitly required.
2. Ask each user to sign in again. Gateway creates one local account and OIDC
   binding keyed by the provider's issuer and stable subject. The account has
   no local password and appears in **Users** as **Pending**.
3. Open the user in **Users** and change the role to `reader`, `writer`, or
   `admin`. The user can choose **Check access** on the waiting screen to
   refresh their session view. Change the role back to **Pending** to revoke
   repository access, or disable the account to block sign-in.

The pending `none` role denies repository reads, writes, administration, and
intelligence even when legacy read defaults or repository grants would
otherwise allow them. An administrator subject or an explicitly mapped OIDC
role can bootstrap a higher role on first registration. Subsequent sign-ins
refresh identity metadata but do not overwrite an administrator-assigned
Gateway role. Existing linked accounts retain their role and state.

`reader`, `writer`, and `admin` are **global** Gateway roles; assigning
one gives its corresponding capability across repositories. Repository-scoped
grant design is separate from this onboarding flow.

After approval, `reader` and `writer` can use the Console **Repositories**
page to browse repositories and artifacts for which they have read access.
`writer` can also use Raw file upload, the Maven publish wizard, and
OCI/npm/PyPI publish guides where repository write access applies. OCI,
Conan, Go, and APT publishing continues through their native protocol clients.
Docker/Podman publishing to OCI requires an administrator-issued
service-account credential with repository write access, delivered through a
secure channel; the SSO browser session is not a Docker credential. `reader` has no
upload or publish controls. Creating repositories, changing configuration,
managing grants, and managing users remain administrator operations.

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
