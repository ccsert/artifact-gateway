# Local User Governance

Artifact Gateway local users are administrator-managed accounts for Console and
management API access. They are separate from API keys, break-glass static
tokens, and OIDC identities.

## Account Model

Every local account has an immutable username and mutable display name, email,
description, global role, and active/disabled state. Usernames are unique
without regard to case. The stored security state includes:

- last successful sign-in and password-change timestamps;
- consecutive failed-sign-in count and an optional lock deadline;
- whether the user must change the password at the next sign-in;
- a monotonically increasing session version.

Passwords are stored only as hashes. Password updates and explicit session
revocation increment the session version, invalidating every previously issued
local session token. A user required to change their password can authenticate
only to `POST /auth/change-password`; management roles are withheld until that
change succeeds. Local passwords must contain at least 8 characters and at most
72 bytes; the byte upper bound is enforced explicitly because bcrypt cannot
process longer inputs safely.

## Management API

All `/api/v2/users` operations require an administrator identity.

| Operation | Behavior |
| --- | --- |
| `GET /users` | Server-side search by username, display name, or email; role/state filters; offset pagination |
| `POST /users` | Creates a profile, role, initial password, and optional mandatory password change |
| `PATCH /users/{userId}` | Updates profile, role, or state with `If-Match` concurrency control |
| `POST /users/{userId}/password` | Resets the password, optionally requires another change, and revokes sessions |
| `POST /users/{userId}/sessions:revoke` | Invalidates all current local sessions with `If-Match` control |
| `GET /users/{userId}/identities` | Lists external identities linked to the account |
| `POST /users/{userId}/identities` | Links an OIDC issuer and subject from the configured provider |
| `DELETE /users/{userId}/identities/{identityId}` | Removes an external identity link |
| `DELETE /users/{userId}` | Permanently removes the account |

The final active administrator cannot be disabled, demoted, or deleted. Failed
optimistic updates return `412`; last-administrator protection returns `409`.

## External Identities

OIDC identities are stored separately from local accounts and linked by their
normalized issuer and stable `sub` claim. A linked OIDC sign-in resolves to the
local account and therefore uses its current role, active/disabled state,
mandatory password-change restriction, and session version. Disabling the
account or revoking all sessions invalidates an existing browser session on its
next authenticated request.

Administrators can link and unlink identities explicitly. Runtime OIDC settings
can also enable just-in-time provisioning, select its default role, and allow a
verified `email` claim to link an existing account. Email linking requires
`email_verified=true` and rejects ambiguous matches. With JIT disabled, an
unlinked subject retains the legacy external-principal behavior and is not
silently attached to a local account.

## Lockout Policy

`GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS` controls consecutive failures before a
temporary lock and defaults to `5`. `GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION`
defaults to `15m`. The supported configuration bounds are 1 to 100 attempts and
1 minute to 24 hours.

Locked, disabled, unknown, and wrong-password accounts return the same login
error to avoid username enumeration. A successful sign-in clears the failure
count and lock deadline. An administrator password reset also clears both.

## Audit And Operations

Successful and failed local sign-ins, self-service password changes, user
creation/update/deletion, administrator password resets, and session revocation
are audited. Management audit actors identify the administrator performing the
operation; the resource identifies the target user.

Operators should verify the following after changing authentication policy:

1. Repeated failed logins lock the test account at the configured threshold.
2. A password reset invalidates a token issued before the reset.
3. Explicit session revocation invalidates every current token for the account.
4. The final active administrator cannot be disabled or deleted.
5. Audit queries distinguish the performing administrator from the target user.

## Current Limitations

Deletion is permanent rather than a recoverable tombstone. A local account has
one global role; custom roles and multiple role assignments are not yet part of
the model. Password expiry and configurable complexity rules are not enforced.
Session inventory, per-session revocation, OIDC back-channel logout, and
identity-provider-initiated logout are not yet supported.
