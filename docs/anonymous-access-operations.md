# Anonymous Access Operations

[简体中文](anonymous-access-operations.zh-CN.md) | [Documentation index](README.md)

Anonymous access is default-deny. It is limited to protocol and browse `GET`
and `HEAD` requests; publication, deletion, promotion, cache mutation, and all
other management writes always require authentication.

## Enablement

An anonymous read is allowed only when every applicable gate is enabled:

1. An administrator enables the global policy with `PUT
   /api/v2/anonymous-access-policy` using `If-Match`.
2. The addressed Repository or Group has `anonymousRead: true`.
3. For a Group, the resolved member Repository also has `anonymousRead: true`.

Enabling the global policy does not expose any existing Repository or Group by
itself. Disabling it immediately denies all anonymous reads, including targets
whose local policy remains enabled.

An authenticated operator or scoped user can review its own effective decision
through `GET /api/v2/repositories/{repositoryId}/effective-access`. The
diagnostic endpoint itself is not anonymous: it accepts a known Repository ID,
does not permit actor impersonation, and returns the caller's read, write,
admin, and anonymous-read decisions even when Repository read is denied.

Use `GET /api/v2/identity` to confirm which authenticated identity, credential
source, and global role the Gateway is evaluating. OIDC diagnostics expose only
configured role mappings that matched the validated token, never arbitrary
provider claims or token material.

Anonymous successful and denied requests record `actor=anonymous` and an
authorization source/reason in the administrator-only audit log.

The Console presents these same gates as one public-access boundary: the
global gate is shown separately from Repository and Group/member opt-in, and a
public-target count makes the potential blast radius visible before an
administrator changes the global switch. This presentation does not introduce
a new policy or bypass any gate.

The unauthenticated `/browse` catalog presents only effective public targets.
Its repository search, format filters, source-type guidance, and read-only
notices are discovery features; publishing, grants, and administration still
require an authenticated management identity.

## Client Examples

Authenticated Maven pull:

```sh
curl -u resolver:$GATEWAY_RESOLVER_TOKEN \
  https://gateway.example.com/maven/releases/org/example/widget/1.0/widget-1.0.jar
```

Anonymous Maven pull after all gates are enabled:

```sh
curl https://gateway.example.com/maven/public/org/example/widget/1.0/widget-1.0.jar
```

Authenticated Raw read:

```sh
curl -H "Authorization: Bearer $GATEWAY_TOKEN" \
  https://gateway.example.com/raw/releases/widgets/widget.tar.gz
```

Anonymous OCI manifest pull:

```sh
oras manifest fetch gateway.example.com/public/widget:1.0
```

Conan 2 remotes retain the Group in their URL. Do not use anonymous access for
any endpoint other than the Gateway's normal revision reads.

```sh
conan remote add public https://gateway.example.com/conan/v2/public
conan install --requires=widget/1.0@team/stable
```

## Operational Checks

- Confirm an unauthenticated `GET` succeeds only for the intended target.
- Confirm `POST`, `PUT`, and `DELETE` without credentials remain denied.
- Inspect the audit record for `actor=anonymous` and the policy reason.
- Call `/api/v2/identity` with the operator credential before comparing a
  Repository's effective-access result; this avoids diagnosing the wrong saved
  Console credential.
- Disable the global policy during incident response; do not edit each
  Repository first.
