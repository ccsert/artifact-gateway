# Anonymous Access Operations

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

Review the effective decision through `GET
/api/v2/repositories/{repositoryId}/effective-access`. Anonymous successful and
denied requests record `actor=anonymous` and an authorization source/reason in

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
- Disable the global policy during incident response; do not edit each
  Repository first.
