# agctl CLI

[简体中文](cli.zh-CN.md) · [Documentation index](README.md)

`agctl` is the Gateway command line client for CI pipelines, platform
management, and agents. It programs against the management API under
`/api/v2` with a bearer credential. The commands available today are
`version`, `info`, and `commands`; the rest of the surface ships as separate
reviewable slices and appears in the command tree only once implemented. The
client shape and its machine contract are recorded in
[ADR 0007](adr/0007-agctl-cli-shape.md).

## Install

`agctl` is a single client binary at the version the release pins in the
`VERSION` file; CI pipelines, platform management, and agents all run the same
build. It carries no server-side configuration or secrets: connection settings
come from flags, environment variables, or the profile file described below.

### Release archives

Every release tag is `v` followed by the `VERSION` value, and each release
publishes one client archive per platform next to the Gateway archives:

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
release tag before relying on the binary. The newest published release at the
time of writing is `v0.8.1` and predates the CLI, so the first release carrying
these archives is the next tag cut from `main`.

### Go module install

```sh
go install github.com/artifact-gateway/artifact-gateway/cmd/agctl@<tag>
```

`<tag>` is the release tag (`v<VERSION>`) of a release that contains
`cmd/agctl`, and the path is the module path `go.mod` declares. This channel
needs both to be publicly resolvable, which is not the case yet: the newest tag
`v0.8.1` predates the CLI, no repository is served at
`github.com/artifact-gateway/artifact-gateway` (`repository not found`), and
requesting the module through the repository path
`github.com/ccsert/artifact-gateway` fails with a module path mismatch because
the tagged `go.mod` declares the other path. Read the line above as the target
of this channel, not as a working install: release archives and the local build
below are the channels that work today, and this one starts working once a
post-CLI tag is published at the declared module path.

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
