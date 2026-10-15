# agctl CLI

[简体中文](cli.zh-CN.md) · [Documentation index](README.md)

`agctl` is the Gateway command line client for CI pipelines, platform
management, and agents. It programs against the management API under
`/api/v2` with a bearer credential. This checkout provides `version`, `info`,
`commands`, `auth login|logout|whoami|change-password`, and
`config profile add|list|use|remove`. The rest of the surface ships as separate
reviewable slices and appears in the command tree only once implemented. The
client shape and its machine contract are recorded in
[ADR 0007](adr/0007-agctl-cli-shape.md).

## Install

`agctl` is a single client binary at the version the release pins in the
`VERSION` file; CI pipelines, platform management, and agents all run the same
build. It carries no server-side configuration or secrets: connection settings
come from flags, environment variables, or the profile file described below.

### Release archives

The release builder in this checkout produces one client archive per platform
next to the Gateway archives for a future release containing this batch:

```text
artifact-gateway-agctl_<version>_linux_amd64.tar.gz
artifact-gateway-agctl_<version>_linux_arm64.tar.gz
artifact-gateway-agctl_<version>_darwin_amd64.tar.gz
artifact-gateway-agctl_<version>_darwin_arm64.tar.gz
```

A client archive holds only `agctl` plus `VERSION.txt` and `INSTALL.txt`; the
server binary, healthcheck, migrations, and environment template stay in the
`artifact-gateway_<version>_<goos>_<goarch>` archive. `SHA256SUMS` lists every
archive of the release, so verify the download before extracting it:

```sh
sha256sum -c SHA256SUMS --ignore-missing   # shasum -a 256 -c SHA256SUMS on macOS
tar -xzf artifact-gateway-agctl_<version>_<goos>_<goarch>.tar.gz
./agctl version
```

`agctl version` reports the client version and revision; compare both with the
release tag before relying on the binary. Published `v0.9.0` includes only the
CLI skeleton and local source build; it excludes this auth/profile/distribution
batch and has no separate agctl archives. These archives require a subsequent
release containing this batch; merging the code does not publish them.

### Go module install

```sh
go install github.com/artifact-gateway/artifact-gateway/cmd/agctl@<tag>
```

`<tag>` is the release tag (`v<VERSION>`) of a release that contains
`cmd/agctl`, and the path is the module path `go.mod` declares. This channel
needs both to be publicly resolvable, which is not the case yet: no repository is served at
`github.com/artifact-gateway/artifact-gateway` (`repository not found`), and
requesting the module through the repository path
`github.com/ccsert/artifact-gateway` fails with a module path mismatch because
the tagged `go.mod` declares the other path. Read the line above as the target
of this channel, not as a working install. Use the local build below; independent
client archives require the future release described above. The module-path
resolution work remains tracked in #347, and #128 remains open.

### Container image

The client image is built from this checkout for pipelines that execute `agctl`
inside a job. It is a distroless static image with CA certificates, no shell,
and the client binary only — the release workflow still publishes the Gateway
and Console images, so build it locally at the version you need:

```sh
make cli-docker-build                                     # docker build -f Dockerfile.agctl -t artifact-gateway-agctl:dev .
docker run --rm artifact-gateway-agctl:dev version
docker run --rm -e AGCTL_SERVER=https://gateway.example.com -e AGCTL_TOKEN="$GATEWAY_TOKEN" \
  artifact-gateway-agctl:dev info
```

`make cli-docker-build` passes the `VERSION` file value and the current Git
revision as build arguments. A profile file works inside the image by mounting
it under the nonroot home:
`-e HOME=/home/nonroot -v "$HOME/.config/agctl/config.yaml:/home/nonroot/.config/agctl/config.yaml:ro"`.

### Local build

```sh
make cli-build                             # writes dist/agctl_<goos>_<goarch>
dist/agctl_darwin_arm64 version
dist/agctl_linux_amd64 info --server https://gateway.example.com --token "$GATEWAY_TOKEN"
```

`make cli-build` cross-compiles linux and darwin for amd64 and arm64 with the
local Go toolchain at the version declared in `go.mod`, and injects the
`VERSION` file value and the current Git revision with the same `-ldflags` as
the release archives. From a checkout, `go install ./cmd/agctl` installs the
same client into `$GOBIN`; without the injection it reports the development
version instead of a release version.

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
`default` profile, then a file that defines exactly one profile. The profile
commands manage this file without hand-editing: `agctl config profile
add|list|use|remove`; every write replaces the file atomically with mode 0600.

A stored token is used only when the effective server matches its profile's
target. Overriding `--server` or `AGCTL_SERVER` with a different target does
not reuse that token; supply the new target's credential explicitly through
`--token` or `AGCTL_TOKEN`. Equivalent host/default-port spellings retain the
binding; distinct IPv6 authorities, interface zones, and base-path case do not
share credentials.

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
commands and are ignored by read-only ones. A write command that cannot plan
without side effects rejects `--dry-run` with a usage error (exit 2) before
any change; the command tree reports `dry-run: unsupported` for such commands.

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
- `error.code` `wait_timeout` marks a bounded `--wait` that ran out of time
  (exit 8); the operation continues on the server.
- Lists use `{"items": [...], "nextCursor": null}`; `nextCursor` is null when
  the data source has no server-side pagination or the listing reached its
  end, and `meta.pagination.mode` states the real mode (`none` for local
  command trees, `cursor` for server pages, which add `pageSize`, `pages`
  read, and `complete`).
- A failure that still has something to report carries it in `data` (and
  `meta`): the pages read before a listing stopped (exit 3), the accepted ID
  and last state of a timed-out wait (exit 8), or `{"outcome": "unknown",
  "readBack": …}` for a write whose result the client could not observe.

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

### `agctl auth login`

Exchanges a local username and password for a session token (`POST
/auth/login`) and stores the token in an explicitly selected profile. The
password comes from bounded stdin (`--password-stdin`); missing, empty, or
oversized input exits 2 before any request. Without `--profile` (or
`AGCTL_PROFILE`) the command refuses to store the token: a secure
`--secret-output` destination does not exist yet, and the client never prints
a token. A profile already bound to a different server is refused before the
request (`--server` or `AGCTL_SERVER` overrides included), and a profile
without a server is bound to the actual login target when the token is
stored, so a token is never sent to the wrong deployment later. When the
server reports `mustChangePassword`, the command stores the token, exits 0,
and points at `agctl auth change-password`; it never changes the password
automatically, and management endpoints answer 403 for such an account until
the password is changed.

### `agctl auth logout`

Requests the server-side logout (`POST /auth/logout`) and removes the
matching token from every profile that stores it. The endpoint answers 204
for every credential kind but only revokes local user sessions, so the
command probes the credential state afterwards (`GET /auth/session`) instead
of assuming revocation: `credentialInvalidated` reports whether the
credential still authenticates — a machine credential or static admin token
is reported as still authenticating and must be revoked from its own
management surface. `--local-only` skips the server call and only forgets the
local credential. Tokens supplied through `--token` or `AGCTL_TOKEN` are
never removed from anywhere.

### `agctl auth whoami`

Shows the caller identity (`GET /api/v2/identity`) and one bounded page of
readable repositories (`GET /api/v2/repositories`, `pageSize=200`). `--json`
data is `{actor, kind, role, administrator, oidc, repositories: {count,
names, truncated}}`; `truncated` is true when more repositories exist than
the page or the sample of names shows. Missing credentials exit 5 before any
request.

### `agctl auth change-password`

Changes the caller's own password (`POST /auth/change-password`) using a local
user session. Both passwords come from bounded stdin, one line each (current
first, then new). On success the matching stored token is removed and a fresh
`agctl auth login` is required; a rejected change keeps the stored token. The
endpoint only accepts local user sessions.

### `agctl config profile`

Manages `~/.config/agctl/config.yaml` without hand-editing:

- `agctl config profile add NAME --server URL [--token-stdin] [--insecure]`
  creates or updates a profile; without `--server` an existing profile keeps
  its server, and `--token-stdin` reads one bounded secret line that is never
  echoed. Changing the server drops the stored token unless `--token-stdin`
  provides the new target's credential, so an old deployment's token is never
  sent to a new one;
- `agctl config profile list` shows the profiles and which one is current;
- `agctl config profile use NAME` sets `current_profile`;
- `agctl config profile remove NAME` deletes a profile and unsets
  `current_profile` when it pointed at it.

Writes serialize the full read/modify/replace transaction across processes on
Linux and macOS, then replace the file atomically with mode 0600;
the stable sibling `config.yaml.lock` is released when its handle closes or
the process exits, and contention fails after ten seconds so callers can retry.
A lax existing file is refused with a `chmod 600` hint. Profile and login
output strips URL userinfo, query parameters, and fragments.

The Windows lock implementation is compile-checked, but private profile
persistence is not supported by the current POSIX permission validation:
Windows file modes do not establish the required 0600 protection. Use file-less
`--server`/`--token` or `AGCTL_SERVER`/`AGCTL_TOKEN` there; Windows profile runtime
acceptance remains incomplete and is not claimed by this batch.

### `agctl repo`

Repository management over `/api/v2/repositories`. Repositories are addressed
by ID (`agctl repo list` shows it).

- `agctl repo list [--page-size N] [--cursor TOKEN]` reads one page (50 by
  default, 1–200) of the repositories visible to the caller; an empty list is
  a success, not a permission error. `nextCursor` continues the listing with
  `--cursor`. `--all` follows cursors within `--max-pages` (default 20) and
  one overall `--timeout`; reaching the budget, or a failure after the first
  page, exits 3 with the items read, `complete: false`, and the cursor to
  continue from.
- `agctl repo show ID` prints one repository; credentials of proxy settings
  are reported only as configured.
- `agctl repo create NAME --format FORMAT [--type hosted|proxy] [--endpoint
  URL] [--allowed-host HOST]… [--anonymous-read]
  [--maven-strict-publication]` creates a repository (platform admin). Every
  create sends an `Idempotency-Key`, and retries of the same call reuse it, so
  a lost response never creates a second repository. When the outcome stays
  unknown (connection failure, 5xx) the command prints the key: re-running
  with `--idempotency-key KEY` replays the original result, and the same key
  with a different request is refused with exit 7. Upstream credentials,
  egress proxies, and OCI bearer exchange are not exposed by this command yet;
  use the Console or the API for them.
- `agctl repo update ID [--anonymous-read=true|false]
  [--maven-strict-publication=true|false] [--endpoint URL] [--allowed-host
  HOST]…` sends only the given settings as a conditional `PATCH`. `If-Match`
  is `--if-match VERSION` or, without it, the version read just before the
  update; a concurrent change fails with 412 (exit 7) and nothing is
  overwritten. The update is never retried automatically. Hosted repositories
  accept only anonymous read and Maven publication policy; the server refuses
  other settings (exit 1).
- `agctl repo delete ID --yes [--wait]` requests the asynchronous deletion.
  Without `--yes` it exits 2 before any request. A 202 is acceptance, not
  completion: `data` is `{id, status: "accepted", repositoryState: null,
  terminal: false}`. Protocol access stops at once and the deletion worker,
  which runs once a minute, advances the repository from `deleting` to
  `deleted`. `--wait` reads the repository back until it is `deleted`
  (`status: "deleted"`, `terminal: true`); `--timeout` then bounds the whole
  command, so allow more than a minute. A timeout exits 8 with the ID and the
  last state read; the server keeps deleting and nothing is resent or
  cancelled. A deleted repository stays readable as an audit anchor; deleting
  it again answers 404.

`--dry-run` on `create`, `update`, and `delete` is a client plan
(`mode: "client-plan"`): it validates the input locally, may read the current
repository (`update` reads its version, `delete` its state), and prints the
request it would send with what was and was not checked. It never submits the
write, so it cannot claim the server would accept it.

## Verification

- `go test ./internal/agctl/...` runs offline against in-process fakes:
  command parsing, exit codes, redaction, and the JSON envelopes. Golden
  envelopes live in `internal/agctl/testdata/golden/`; after an intended
  output change, rewrite them with `go test ./internal/agctl/ -run Repo
  -update` and review the diff.
- `make cli-e2e` builds `agctl` and runs it against a real, isolated Gateway:
  it boots a Compose project from `GATEWAY_ENV_FILE` (or `.env`) on free
  ports and removes it afterwards. With `CLI_E2E_SERVER` and `CLI_E2E_TOKEN`
  (an admin token) it reuses a running Gateway instead, which is how CI runs
  it after the Console E2E. The scenario creates a repository, replays the
  create, updates it (dry-run, real update, stale `If-Match`), finds it in the
  list, deletes it (confirmation, dry-run, 202, timed-out wait), and waits for
  `deleted`; it also checks the exit codes for a wrong token (5), a missing
  repository (4), invalid input (2), and conflicts (7).
- `make cli-contract-check` verifies that the command map classifies every
  contract operation and that each row marked `implemented` names a
  registered command.

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
- `agctl auth login` stores the session token only in an explicitly selected
  profile and refuses before the request otherwise; passwords and tokens come
  from bounded stdin, are never echoed by `--json`, verbose output, or error
  rendering, and are registered for failure redaction.
- `--insecure` disables TLS certificate verification and is intended only for
  trusted test networks; prefer a valid certificate.
- One-time secrets (issued API keys, tokens) are governed by the
  service-account command work: without an explicit secure destination they
  are never written to stdout, stderr, or history (see
  [ADR 0007](adr/0007-agctl-cli-shape.md)).
