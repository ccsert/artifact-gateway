# agctl CLI

[简体中文](cli.zh-CN.md) · [Documentation index](README.md)

`agctl` is the Gateway command line client for CI pipelines, platform
management, and agents. It programs against the management API under
`/api/v2` with a bearer credential. The commands available today are
`version`, `info`, and `commands`; the rest of the surface ships as separate
reviewable slices and appears in the command tree only once implemented. The
client shape and its machine contract are recorded in
[ADR 0007](adr/0007-agctl-cli-shape.md).

## Build

```sh
make cli-build          # writes dist/agctl
dist/agctl version
dist/agctl info --server https://gateway.example.com --token "$GATEWAY_TOKEN"
```

The build uses the local Go toolchain at the version declared in `go.mod`.
Release packaging and multi-platform binaries are tracked separately.

## Configuration

Every setting resolves with fixed precedence:

1. explicit flag (`--server`, `--token`, `--profile`);
2. environment variable (`AGCTL_SERVER`, `AGCTL_TOKEN`, `AGCTL_PROFILE`);
3. the selected profile in `~/.config/agctl/config.yaml`.

A missing server on a command that needs one exits with code 2 before any
request. `--server` is the Gateway root URL; management endpoints are
addressed under `/api/v2` automatically.

The configuration file holds one or more profiles and must not be readable by
other users (mode 0600) — otherwise commands fail with a `chmod 600` hint:

```yaml
current_profile: production
profiles:
  production:
    server: https://gateway.example.com
    token: replace-with-a-service-account-token
    insecure: false
```

Profile selection order: `--profile`, `AGCTL_PROFILE`, `current_profile`, the
`default` profile, then a file that defines exactly one profile.

## Global flags

| Flag | Meaning |
| --- | --- |
| `--server URL` | Gateway root URL, for example `https://gateway.example.com` |
| `--token TOKEN` | bearer token for the management API |
| `--profile NAME` | profile from `~/.config/agctl/config.yaml` |
| `--json` | emit the versioned JSON envelope instead of human text |
| `-o table\|json` | output format (`--json` wins when both are given) |
| `--timeout DURATION` | overall request timeout including retries (default `30s`) |
| `--verbose` | print request method and URL to stderr; credentials are never printed |
| `--insecure` | skip TLS certificate verification |
| `--dry-run` | plan a write without submitting it (write commands) |
| `--yes` | assume yes for confirmations (write commands) |

Global flags work before and after the command name: `agctl --json info` and
`agctl info --json` are equivalent. `--dry-run` and `--yes` apply to write
commands; the commands available today are read-only and ignore them.

## Output contract

Human mode writes results to stdout and diagnostics to stderr. Commands never
prompt: missing required input fails with exit code 2 before the first
request, so `agctl` is safe in pipelines and agent loops.

With `--json`, stdout carries exactly one document — also on failure:

```json
{
  "schemaVersion": 1,
  "ok": false,
  "data": null,
  "error": {
    "code": "access_denied",
    "httpStatus": 403,
    "message": "not allowed",
    "hint": "the credential is valid but lacks the required permission for this operation",
    "requestId": "1f0c..."
  },
  "meta": null
}
```

- `schemaVersion` starts at 1 and only changes on a breaking change to this
  schema.
- `error.code` is the server problem code when the response carries one;
  local failures use `usage_error`, `credentials_missing`, `connection_error`,
  `partial_failure`, or `internal_error`.
- `error.httpStatus` is null for failures that never reached the server.
- Lists use `{"items": [...], "nextCursor": null}`; `nextCursor` is null when
  the data source has no server-side pagination, and `meta.pagination.mode`
  states the real mode (`none` for local command trees).

`--verbose` explains requests on stderr (`> GET https://…`, `< 204`) with
credential-bearing URL parts redacted; the token itself is never printed, and
neither are response bodies of failed authentication.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | generic failure |
| 2 | usage error (bad flags, missing input, invalid configuration) |
| 3 | batch finished with partial success |
| 4 | not found (HTTP 404) |
| 5 | unauthenticated or missing credentials (HTTP 401) |
| 6 | forbidden (HTTP 403) |
| 7 | conflict or precondition failed (HTTP 409/412) |
| 8 | connection failure or timeout |

## Commands

### `agctl version`

Prints the client version, revision, and Go version; `--json` data is
`{version, revision, goVersion, modified}`.

### `agctl info`

Probes the deployment and reports one line per source:

- `GET /livez` and `GET /readyz` — reachability and readiness;
- `GET /api/v2/identity` — the caller's actor, kind, role, and administrator
  flag;
- `GET /api/v2/formats` — the format profiles the deployment serves.

Without configured credentials the identity and format probes report
`unauthenticated` instead of failing, so `info` works as a pre-login probe; a
rejected token reports `unauthorized`. The command exits 0 whenever the server
answered, and exits 8 when it could not be reached. `--json` data is an
`InfoReport` with per-source `status` (`ok`, `not_ready`, `unauthenticated`,
`unauthorized`, `forbidden`, `error`) and details.
The `server` field is the effective endpoint with userinfo, query, and fragment
removed. Failed probes preserve `code`, `requestId`, and an actionable `hint`
when available; human output displays the same diagnostics.

### `agctl commands`

Prints the command tree. `agctl commands --json` returns, for every command,
its usage, flags, and contract metadata: resource, scenario tags, delivery
stage, real endpoints, whether it mutates state, minimum role, and its
pagination, dry-run, retry, and wait capabilities. Scripts and agents use
this tree to discover what the installed client can do.

## Security notes

- Credentials never reach output: not through verbose logs, error messages,
  or JSON envelopes. The `Authorization` header is never printed.
- Authentication failures use a standard HTTP message; unstructured error
  bodies are not echoed. Structured problem messages are redacted.
- HTTP redirects are reported without following them, so credentials cannot
  be forwarded to another endpoint or a plaintext connection.
- A server's `Retry-After` delay is never shortened; the request timeout
  cancels the wait when the remaining budget is insufficient.
- The configuration file must be mode 0600; commands refuse a lax file.
- `--insecure` disables TLS certificate verification and is intended only for
  trusted test networks; prefer a valid certificate.
- One-time secrets (issued API keys, tokens) are governed by the
  service-account command work: without an explicit secure destination they
  are never written to stdout, stderr, or history (see
  [ADR 0007](adr/0007-agctl-cli-shape.md)).
