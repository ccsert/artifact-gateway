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
go install github.com/ccsert/artifact-gateway/cmd/agctl@<tag>
```

`<tag>` is a release tag (`v<VERSION>`) whose `go.mod` declares
`github.com/ccsert/artifact-gateway`, the source repository path
([ADR 0008](adr/0008-go-module-path.md)). The first release after this change is
the earliest such tag; `v0.9.0` and older declare the previous, unresolvable
path and cannot be installed this way. A module-mode install reports the tag as
its version, but the Go toolchain records no VCS data for `pkg@version` builds,
so `agctl version` shows the revision as `unknown`. Use a release archive or
the image when you need the revision. Publishing an eligible tag and verifying
a public GitHub install remain release acceptance work tracked in #347 and #128.

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
  `partial_failure`, or `internal_error`. `secret_not_delivered` (exit 1)
  marks a one-time secret that was issued but could not be written to
  `--secret-output`; `data` names the issued ID to revoke.
- `error.httpStatus` is null for failures that never reached the server.
- `error.code` `wait_timeout` marks a bounded `--wait` that ran out of time
  (exit 8); the operation continues on the server. `agctl status` fails with
  `unhealthy`, or under `--strict` with `degraded` or `unknown` (exit 1).
- `email delivery wait` reports `delivery_failed` (exit 1) for a `dead`
  delivery. This local terminal result has `error.httpStatus: null`; `data`
  retains the actual delivery record, including its failure code and duplicate
  risk. Waiting does not replay or change the delivery.
- Lists use `{"items": [...], "nextCursor": null}`; `nextCursor` is null when
  the data source has no server-side pagination or the listing reached its
  end, and `meta.pagination.mode` states the real mode (`none` for local
  command trees and endpoints that answer everything at once, `cursor` for
  server pages, which add `pageSize`, `pages` read, and `complete`; `bounded`
  for a window without a cursor, with its `limit` and `complete: true` only
  when the window was not full; `offset` for an offset window with `limit`,
  `offset`, and the server's `total`). A total the server does not report is
  absent, never 0.
- `meta.scope` carries what a page says about the range it covers, such as
  `searchedRepositories` of a global search or the Group a browse page
  belongs to.
- `access check --require` fails with `not_permitted` (exit 6) when a
  required permission is not granted; the explanation stays in `data`.
- `--follow` (on `runtime logs` and `audit list`) streams NDJSON with
  `--json`: one versioned event per line, `{"schemaVersion": 1, "type":
  "entry", "data": …}`, a `gap` event when entries may have been missed, and
  exactly one terminal `{"type": "end", "data": {"reason", "events",
  "resume"}, "error": …}`. A stream needs `--duration` or `--max-events`
  (otherwise exit 2) and never runs forever. `reason` is `duration` or
  `max_events` (exit 0), `interrupted` on a signal (exit 3), `scope_changed`
  when the runtime log session changed (exit 7), `poll_budget` when an audit
  poll cannot read all its pages (exit 3), or `error` (the error's
  exit code); `resume` says where a new stream continues. Usage errors before
  the stream starts are an ordinary single envelope.
- A failure that still has something to report carries it in `data` (and
  `meta`): the pages read before a listing stopped (exit 3), the accepted ID
  and last state of a timed-out wait (exit 8), or `{"outcome": "unknown",
  "readBack": …}` for a write whose result the client could not observe.
  Repository, Group, and artifact writes also report an unknown outcome when a
  successful HTTP response contains unreadable, empty, or incomplete JSON.
  Creation preserves its idempotency key for safe replay; updates and
  deletions must be read back before retrying.

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
pagination, dry-run, retry, wait, and follow capabilities. Scripts and agents use
this tree to discover what the installed client can do.

### `agctl auth login`

Exchanges a local username and password for a session token (`POST
/auth/login`) and stores the token in an explicitly selected profile. The
password comes from bounded stdin (`--password-stdin`); missing, empty, or
oversized input exits 2 before any request. Without `--profile` (or
`AGCTL_PROFILE`) the command refuses to store the token unless
`--secret-output FILE` names a new private file for it instead (the same
rules as every one-time secret: checked before the request, mode 0600, never
overwritten, never printed; `--profile` and `--secret-output` exclude each
other). The client never prints a token. A profile already bound to a different server is refused before the
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

### `agctl group`

Group management over `/api/v2/groups`. A Group references repositories of
one format; the members' configured positions decide the resolution order
within each repository type. Groups are addressed by ID.

A platform administrator reads and writes every Group. A caller who
administers every repository a Group references may read it (`show`,
`members list`, `resolution`) and finds it in `list`; writing it needs that
for the members before and after the change. Any other caller gets 404, as if
the Group did not exist, so a Group it only partly administers stays hidden.

- `agctl group list [--page-size N] [--cursor TOKEN | --all [--max-pages
  N]]` pages through Groups like `agctl repo list`.
- `agctl group show ID` prints the Group and its members by configured
  position; `agctl group members list ID` prints only the members
  (`meta.pagination.mode` is `none`: the endpoint is not paginated).
- `agctl group resolution ID` shows the order the protocol resolver reads the
  members in (hosted before proxy, then configured position) with repository
  names, and how many members it excludes as missing, inactive, or of another
  format. It does not query artifacts or decide any reader's access.
- `agctl group create NAME --format FORMAT --member REPOSITORY_ID…
  [--anonymous-read]` creates a Group; the order of `--member` is the
  configured position. Like `repo create`, every create sends an
  `Idempotency-Key` that retries reuse, and an unknown outcome prints the key
  for `--idempotency-key`.
- `agctl group update ID [--name NAME] [--anonymous-read=true|false]` reads
  the Group and sends it back whole (`PUT`) with the change, keeping its
  format and members; `If-Match` is `--if-match VERSION` or the version just
  read, and a 412 (exit 7) overwrites nothing. Change members with
  `agctl group members set`.
- `agctl group members set ID --member REPOSITORY_ID…` replaces the members
  in the order given, conditional on `--if-match` or the version just read.
  The server returns the updated Group; an older server that returns a bare
  member list is followed by a read of the Group to report its new version.
  If that read
  fails or returns an incomplete Group, the command fails while retaining
  the confirmed member list and a read-back hint; it never repeats the write.
- `agctl group delete ID --yes` deletes the Group; its member repositories
  are kept. Deletion is synchronous (`data` is `{id, deleted: true}`) and is
  never retried, because a repeat after a lost response would answer 404.

`update`, `members set`, and `delete` are never retried automatically.
`--dry-run` on the Group writes is a client plan, as for `agctl repo`.

### Repository content and format views

Content reads follow the repository's read authorization; a repository the
caller may not read answers 403 (exit 6), while a global search filters to
readable repositories and an empty result is a success. Repositories are
addressed by ID with `--repo`.

- `agctl artifact search --repo ID [--q TEXT]` searches one repository;
  `agctl artifact search --global --q TEXT [--format FORMAT]` searches every
  readable repository and reports `meta.scope.searchedRepositories`.
  `--match prefix|contains` selects the server's match mode (prefix when
  omitted). Both page with `--page-size`, `--cursor`, and `--all`.
- `agctl artifact browse --repo ID [--parent NODE_ID]` (also
  `agctl repo browse ID`) lists one level of the server's browse tree; node
  IDs are opaque, signed, and expire. `agctl group browse ID` browses the
  union of a Maven or Raw Group's readable members and reports the Group and
  its candidate members in `meta.scope`.
- `agctl artifact identities --repo ID --purpose scan|distribution` lists the
  canonical coordinate and digest pairs scanners or promotion accept. The
  endpoint has no cursor: `--limit` (1–200) bounds the window, and a full
  window may hide more.
- `agctl artifact usage --repo ID [--limit N] [--offset N]` shows download
  counts per resource in an offset window with the server's total.
- `agctl artifact intelligence show` and `agctl artifact quarantine show`
  read the record of one `--coordinate` and `--digest`; 404 means none is
  recorded.
- `agctl oci images|manifests` and `agctl maven coordinates` are the format
  views; `oci manifests` needs `--name IMAGE` and includes untagged
  manifests.
- `agctl artifact list|show|delete --repo ID` cover the artifacts the Maven
  hosted publication flow records; other formats answer 404. `list` returns
  every visible artifact at once (`meta.pagination.mode` is `none`). `show`
  adds the artifact's intelligence and quarantine records, each reported as
  `recorded`, `none`, `not_permitted`, or `unavailable` — never as healthy
  when it was not read. `delete ARTIFACT_ID --repo ID --yes` tombstones the
  artifact (202; `serverState` is the server's deletion state); repeating it
  is accepted again, so the request is retried like other idempotent calls.
- `agctl repo capabilities ID` lists the operations a hosted repository's
  format supports.
- `agctl repo connect ID` prints client configuration for the repository's
  format (`.npmrc`, `pip.conf` and `.pypirc`, Maven `settings.xml` and
  `pom.xml`, `GOPROXY` and `.netrc`, `docker login`, Raw `curl`, Conan
  remotes, APT `sources.list` and `auth.conf`, Cargo `config.toml`), with
  publish snippets for hosted repositories. Credentials are always the
  placeholder `${ARTIFACT_GATEWAY_TOKEN}`; the configured token is never
  written into a snippet. Maven Hosted reads and deploys through
  `/repository/maven/<repository>/`; Proxy guides use `/maven/<repository>/`.
  URLs follow the server the CLI talks to, with
  credentials, query, and fragment removed. PyPI `.netrc` entries use the
  bare hostname that pip matches, while index URLs retain any explicit port.

### Audit and runtime

These commands read; none of them changes anything.

- `agctl audit list [--actor A] [--operation O] [--outcome O] [--repository
  R] [--group G] [--format F] [--from TIME] [--to TIME]` pages through the
  audit log with cursors (`--page-size`, `--cursor`, `--all`).
  `--bounded [--limit N]` instead reads one window of at most 500 records
  from `GET /audits`, which has no cursor and no time range. `--csv` prints
  RFC 4180 CSV with a header row. The log records authorization decisions,
  refusals, grant and template writes, and Group resolution rather than
  every management write; an audit record carries the request id an `agctl`
  failure reports. `--follow` streams new records: audit records have no
  sequence, so each poll re-reads a two-minute lookback window and skips
  records it already delivered; without `--from` the stream starts now, with
  it the records since `--from` come first. If a poll still has unread pages
  after ten requests, it emits a `gap` and ends with `poll_budget` (exit 3)
  before delivering that incomplete poll. Narrow the filters or read history
  with `audit list --all`. `end.resume.from` preserves the inclusive lookback
  floor for restarting with the same filters; records can replay, including
  those sharing a timestamp, so consumers should deduplicate. Records arriving
  later than the two-minute lookback are outside this polling guarantee.
- `agctl runtime logs [--level L] [--component C] [--request-id ID]
  [--trace-id ID] [--keyword TEXT] [--from TIME] [--to TIME | --window D]`
  reads one page (newest first, `--limit` up to 100) from the memory of the
  process that answers: the logs are local to one instance and session and
  are lost on restart (`meta.scope`). `nextCursor` pages older entries with
  `--before`. `--follow` first emits a `scope` event with local instance,
  session, retention and source metadata (also for an empty stream), prints
  the page oldest first and then follows with the server's cursor. It caps
  each request to the remaining event budget, so an end cursor never skips
  unread entries in a fetched page. Restart with
  `--after-cursor CURSOR --follow --duration D` using `end.resume.afterCursor`
  and the same filters; `--after-cursor` also reads one ascending page without
  follow. The cursor belongs to one process session, so a
  restart or another instance answering ends the stream with
  `scope_changed`. Overwritten unread entries are reported as a `gap`.
- `agctl status` probes `/livez`, `/readyz`, and the diagnostics and prints
  one verdict per check: dependencies, runtime nodes (with their issue
  codes), scanner, and failed queue jobs. The overall result is the worst
  check — `unhealthy`, `degraded`, `unknown`, `healthy` — and the command
  exits 1 when it is unhealthy, or with `--strict` whenever it is not
  healthy. Without credentials, or when the caller may not read the
  diagnostics, those checks are `unknown`, never healthy.
  An unhealthy or unreachable scanner makes ordinary `status` fail;
  a stale scanner database remains degraded and fails with `--strict`.
- `agctl diagnostics`, `agctl runtime nodes`, and `agctl overview show` print
  the server documents (JSON output passes them through unchanged, so new
  fields reach scripts) with a human summary. Diagnostics includes each
  local mount's status, reason, byte values, actual sample time and shared
  aliases, plus the observation scope and unit. Missing values remain unknown.
- `agctl lifecycle-job list [--repo ID | --limit N]` reads the newest jobs of
  one repository (at most 100), or a global window of at most 500 by default.
  The server limits the global window before filtering repository permissions:
  a short or empty result does not prove completeness. Both sources report
  `mode: bounded`; global completeness stays unknown, and a repository window
  is complete only when fewer than 100 jobs are returned.
  `agctl lifecycle-job show JOB_ID` finds one job in the same window; a job
  outside it answers exit 4 with the range that was searched.
  Running, retrying, and cancelling jobs is not part of this command family
  yet.

### Authorization

Grants are written one row at a time, keyed by principal and resource
prefix; the whole-table replace endpoint is not exposed. Repositories are
addressed by ID with `--repo`.

- `agctl grant list --repo ID` prints a repository's grants and the grant
  set version (from `ETag`) that conditions `template apply`;
  `agctl grant list --all-repositories` lists the grants on every repository
  the caller administers.
- `agctl grant upsert --repo ID --principal P --scope S… [--resource-prefix
  X]` creates or replaces one grant and leaves the others untouched; the
  same row may be sent again, so the request is retried. The answer must
  contain the written row and the new version, or the outcome is unknown.
  `--dry-run` reads the current row and reports `create`, `replace`, or
  `unchanged`.
- `agctl grant delete --repo ID --principal P [--resource-prefix X] --yes`
  removes one grant. It is not retried: a repeat answers 404, which is also
  what a grant that does not exist answers.
- `agctl role list|show` and `agctl template list|show` read the catalogues
  (platform administrators).
- `agctl template apply TEMPLATE_ID --repo ID --yes` replaces every grant of
  the repository with the template's, conditional on `--if-match` or the
  grant set version read just before; a 412 (exit 7) replaces nothing and the
  apply is never retried. `--dry-run` reads the grants and the template and
  lists every grant the apply would add, remove, change, and keep.
- `agctl access check --repo ID [--actor A [--role member|admin]]
  [--resource PATH]` explains read, write, admin, intelligence, and
  anonymous read with the source and reason of each decision.
  `--require PERMISSION` (repeatable) exits 6 when one is not allowed, for
  CI and agent self-checks. Explaining another actor needs a platform
  administrator; `--role` simulates that actor's global role. A caller with
  no read or intelligence authority gets 404 (exit 4), hiding the repository's
  existence; `--require` exits 6 when a returned decision denies permission.
- `agctl access anonymous show` reads the platform switch for anonymous
  reads; `agctl access anonymous set --enabled=true|false` replaces it
  conditional on `--if-match` or the version just read, and is never
  retried. Enabling needs `--yes`, because it applies to every repository
  and Group that allows anonymous reads.

### Least-privilege service-account check

Use an existing active service account and its credential: credential issuance
is tracked separately in #133. Set `AGCTL_SERVER`, `REPO_ID`,
`SERVICE_ACCOUNT_ID`, `ADMIN_TOKEN`, and `SERVICE_ACCOUNT_TOKEN` in the
environment; only the administrator writes the grant. This example admits
reads under `releases/` and leaves write authority denied:

```sh
AGCTL_TOKEN="$ADMIN_TOKEN" agctl grant upsert --repo "$REPO_ID" \
  --principal "service-account:$SERVICE_ACCOUNT_ID" \
  --resource-prefix releases/ --scope repositories:read
AGCTL_TOKEN="$SERVICE_ACCOUNT_TOKEN" agctl access check --repo "$REPO_ID" \
  --resource releases/app.txt --require read --json
# Expected exit 6: read authority reveals the resource, but write is denied.
AGCTL_TOKEN="$SERVICE_ACCOUNT_TOKEN" agctl access check --repo "$REPO_ID" \
  --resource releases/app.txt --require write --json
AGCTL_TOKEN="$ADMIN_TOKEN" agctl grant delete --repo "$REPO_ID" \
  --principal "service-account:$SERVICE_ACCOUNT_ID" \
  --resource-prefix releases/ --yes
# Expected exit 4 after revocation, if this was the account's only authority.
AGCTL_TOKEN="$SERVICE_ACCOUNT_TOKEN" agctl access check --repo "$REPO_ID" \
  --resource releases/app.txt --require read --json
```

### Service accounts, credentials, and API keys

Service accounts are the machine principals for CI
(`service-account:<id>` in grants). Their credentials, and platform API keys,
carry one-time secrets: the server returns a secret only in the answer that
issues it.

- `agctl service-account list` pages through the accounts.
  `agctl service-account show ID` finds one by reading the list, because
  there is no endpoint for a single account: it answers 404 (exit 4) only
  after reading the whole list, and exit 3 when `--max-pages` runs out first.
- `agctl service-account create NAME [--description TEXT]` creates an
  account; it is not retried (the endpoint has no idempotency key, and a
  repeat answers 409 for the account it created).
  `agctl service-account update ID [--description TEXT] [--state
  active|disabled]` is conditional on `--if-match` or the version read from
  the list; disabling stops every credential of the account.
- `agctl credential list --service-account ID` and `agctl api-key list` never
  show secrets.
- `agctl credential issue --service-account ID --name NAME --secret-output
  FILE [--expires-in DURATION]` and `agctl api-key create NAME
  --secret-output FILE [--role member|admin]…` write the secret only to
  `FILE`. Without `--secret-output` they exit 2 before any request. Before
  the request the destination must not exist (files and links alike are
  never overwritten) and a private temporary file is created in its
  directory; after the answer the secret is written there with mode 0600,
  synced, and linked into place. Output reports the ID, expiry, and
  `secret: {path, mode, delivered}`, never the secret, also with `--json` and
  `--verbose`. Issuing is never retried, since every request mints a new
  secret. If the secret cannot be written after it was issued, the command
  exits 1 with `secret_not_delivered`, keeps the secret out of every message,
  and names the ID to revoke; it never issues another one automatically.
  `--dry-run` checks the destination and leaves nothing behind.
- `agctl credential revoke CREDENTIAL_ID --service-account ID --yes` and
  `agctl api-key revoke ID --yes` stop the secret at once. Revocation records
  its time once and answers the same record again, so it is retried; the
  record stays listed as revoked.

Rotating a CI credential without downtime: issue the new credential to a new
file, deploy it, confirm it works (for example with `agctl auth whoami`), then
revoke the old one; the old secret then answers 401 (exit 5).

### Users and sessions

Local users have a global role (`none`, `member`, `admin`); `member` grants
no repository access by itself — repository access comes from grants
(`user:<name>`).

- `agctl user list [--search TEXT] [--role R] [--state S] [--limit N]
  [--offset N]` reads an offset window with the server's total;
  `agctl user show ID` reads one user.
- `agctl user create NAME --role R (--password-stdin | --password-file FILE)`
  creates a user. Passwords come only from bounded stdin or the first line
  of a private regular file (no group or other permissions; use `chmod 600`),
  are checked locally (8 characters to 72 bytes), and never appear
  in output, plans (`--dry-run` shows a placeholder), errors, or verbose
  logs. Creation is not retried.
- `agctl user update ID [--role R] [--state S] [--display-name …] [--email …]
  [--description …]` and `agctl user reset-password ID (--password-stdin |
  --password-file FILE) [--must-change-password]` are conditional on
  `--if-match` or the version read just before; neither is retried. The
  server refuses to demote, disable, or delete the last active
  administrator (409, exit 7).
- `agctl user delete ID --yes` deletes a user; disabling with
  `--state disabled` is the reversible alternative.
- `agctl user sessions list ID [--include-inactive]` lists sign-in sessions.
  `agctl user sessions revoke ID --session SESSION_ID --yes` ends one
  session (retried: ending it twice is harmless); `--all` ends every session,
  conditional on the user's version and not retried. An ended session's
  token answers 401 at once.
- `agctl user identities list|link|unlink ID` manage linked OIDC identities;
  linking needs the issuer configured on the Gateway, and unlinking needs
  `--yes`.

### Publishing

- `agctl publish raw SOURCE --repo ID [--to PATH_OR_PREFIX]` uploads a file,
  or every regular file of a directory tree (links are not followed), to a
  Raw hosted repository with `PUT /raw/{repository}/{path}`. Each object's
  SHA-256 is computed locally and sent as the `Digest` header, so the Gateway
  verifies the bytes, and the answer's ETag must name the same digest or the
  object is reported `unknown`. Objects whose repository copy already has the
  same digest are `skipped` (a `HEAD` read; `--force` uploads them anyway),
  so re-running after a partial failure is safe. Uploads run `--parallel`
  at a time (default 4) and stream from disk; a 1 GiB object limit applies.
  Results list every object with its `path`, canonical `coordinate` (each
  segment escaped like Go's `url.PathEscape`, as search reports it), size,
  digest, and status; any `failed` or `unknown` object exits 3. A Raw PUT
  replaces whatever the path held: there is no conditional write yet.
- `agctl publish maven --repo ID --coordinate G:A:V --pom FILE [--file
  FILE]…` publishes one Maven coordinate through a publish session: it
  declares every file with its size and digest under an `Idempotency-Key`,
  uploads them, and commits. Re-running with the same `--idempotency-key`
  resumes the same open session; a committed session is never reused (exit
  7). The steps are also separate commands: `agctl publish create`,
  `agctl publish upload SESSION --file …`, `agctl publish commit SESSION`, and
  `agctl publish status SESSION`. Uploads are content-addressed and retried;
  the commit is not retried (a repeat answers 409), and a lost commit answer
  is settled by reading the session back: `committed` confirms it.
- Other formats publish with their native clients; `agctl repo connect`
  prints their configuration.

### Quota alerts and email

These second-batch (M2) commands need a platform administrator.

- `agctl email capability` reports whether the answering process can queue
  email (`ready`, `email_disabled`, `encryption_key_unavailable`). Ready means
  configured, not that mail reaches an inbox.
- `agctl email target list|show` never show recipients, only
  `recipientConfigured`. `agctl email target create NAME --locale en|zh-CN
  (--recipient-stdin | --recipient-file FILE) [--enabled]` reads the address
  only from bounded stdin or a private (mode 0600) file and redacts it
  everywhere, including plans. `agctl email target update ID` changes the
  name, locale, enabled state, or recipient conditional on the version read
  just before; fields not given are sent back as they are, because the
  endpoint treats an omitted `enabled` as disabled. Rules keep the target
  version they were bound to until updated.
- `agctl email preview --scenario S --locale L [--html-output FILE]
  [--text-output FILE]` renders fixed synthetic data and queues nothing; the
  two parts must go to different files.
- `agctl email test --target ID --scenario S --yes` queues one real test
  email for the target's current version under an `Idempotency-Key` (a UUID;
  `--idempotency-key` reuses one, and the same key answers the same delivery,
  so the request is retried). The server matches a repeat by key, target,
  target version, and scenario, so an unknown outcome reports the version it
  was sent with, and a later retry passes it back as
  `--idempotency-key K --if-match VERSION` instead of reading a newer one.
  Queued is not delivered:
  `agctl email delivery wait ID [--timeout D]` polls until the relay accepted
  it (exit 0) or it is dead (exit 1, `delivery_failed`, with the actual record
  in `data`), and a timeout exits 8 with the delivery still queued —
  there is no cancel (`commands --json` reports `cancelSupported: false`).
  A wait accepts only a complete answer for the requested delivery ID.
  `agctl email delivery list [--limit N]` reads the newest
  deliveries (at most 100, no cursor); `show` reads one, including attempts
  and `possibleDuplicate`. `agctl email delivery replay ID --yes` queues a
  dead delivery again, conditional on its version and never retried.
- `agctl quota-alert list|show|events` read rules, their state, and their
  warning, critical, and resolved events with delivery state. Events are the
  newest 50-record window (`meta.pagination.mode: bounded`, `limit: 50`); a full
  window leaves completeness unknown, and there is no cursor for older history.
  `agctl quota-alert create --repo ID --target ID` needs every threshold and
  duration (`--warning-bp`, `--critical-bp`, `--recovery-below-bp`,
  `--warning-for`, `--critical-for`, `--recovery-for`, `--max-sample-age`);
  there is no default policy, and a rule starts disabled unless
  `--enabled --yes`. `agctl quota-alert update RULE_ID` changes thresholds,
  target, or enabled state conditional on the version read just before,
  sending unchanged fields as they are; enabling needs `--yes`.
  `agctl quota-alert delete RULE_ID --yes` deletes conditionally.
- A 503 that names `email_disabled`, `encryption_key_unavailable`, or
  `queue_full` is a definite refusal (exit 1) only when no earlier attempt may
  already have queued the test. After a lost or uncertain earlier answer, the
  result stays unknown with the original key and target version for recovery.
  An explicit key/version recovery also stays unknown if channel readiness
  refuses it before the server can look up the earlier key.

### Agent skill

`.agents/skills/agctl/SKILL.md` teaches agents the machine contract and the
CI and administration recipes. A unit test fails when the skill names a
command, flag, or argument count the client does not have.

## Verification

- `make cli-test` runs the command entry point and `internal/agctl` tests
  offline against in-process fakes (also run by CI and `ci-local`):
  command parsing, exit codes, redaction, and the JSON envelopes. Golden
  envelopes live in `internal/agctl/testdata/golden/`; after an intended
  output change, rewrite them with `go test ./internal/agctl/ -update` and
  review the diff.
- `make cli-e2e` builds `agctl` and runs it against a real, isolated Gateway:
  it boots a Compose project from `GATEWAY_ENV_FILE` (or `.env`) on free
  ports and removes it afterwards. With `CLI_E2E_SERVER` and `CLI_E2E_TOKEN`
  (an admin token) it reuses a running Gateway instead, which is how CI runs
  it after the Console E2E; `CLI_E2E_RESOLVER_TOKEN` adds the checks that need
  a credential without repository permissions, and `CLI_E2E_OIDC_ISSUER` (the
  Gateway's configured OIDC issuer, with surrounding whitespace and trailing
  slashes normalized) adds the identity link checks; isolated
  Compose mode configures a synthetic issuer itself, and CI configures one on
  the shared Gateway. Agent recipes first inspect the same real Gateway through
  `commands --json` and `info --json`, retaining schema/capability and identity
  checks. The scenario creates a repository, replays the
  create, updates it (dry-run, real update, stale `If-Match`), and finds it in
  the list; builds a Group over it and a second repository (dry-run, replay,
  unknown member, members, resolution order, rename with a stale `If-Match`,
  reorder, delete that keeps the members); creates and updates a Maven Proxy
  with a synthetic upstream (no fetch), verifies its capabilities and read-only
  connection guide, refuses a stale update, and accepts deletion; publishes a Raw file and a Maven
  POM through the native protocols and searches, browses, lists, inspects,
  and deletes them; checks that a credential without permissions is refused
  (6), sees an empty global search, and that the refusal is in the audit log
  under the reported request id; writes, checks, narrows, and removes a
  grant for a simulated member (access check exit 6 when denied), finds the
  grant writes in the audit log and in a bounded `--follow` stream, applies a
  template (dry-run diff, stale `If-Match`, apply), re-sets the anonymous
  switch to its own value with a stale-version refusal, reads runtime logs
  as a page and a bounded stream that can resume with `--after-cursor`;
  verifies concurrent single-row grants preserve both principals. In isolated
  Compose mode it also verifies a real non-admin caller's access, refused
  simulation and grant write, unchanged version and absence of mutation audit,
  and the hidden repository after revocation. It reads status, diagnostics, runtime nodes,
  overview, and lifecycle jobs; then deletes the repository (confirmation,
  dry-run, 202, timed-out wait) and waits for `deleted`. The CI playbook runs
  as well: a service account gets a credential only into a 0600 file, reads
  the repository with it, rotates it (the old secret answers 401), and loses
  access with its grant. With only read/write on a separate Maven repository,
  the new credential creates/replays a session, uploads and commits its POM/JAR,
  searches the coordinate and reads both files byte-for-byte through the native
  endpoint; the read-back is found by actor/operation in the audit. Revoking its
  grant refuses a new session with 403 while the committed artifact remains.
  An API key is created and revoked; a pending user
  becomes a member without repository access until granted, its session is
  ended and rejected, its password reset from a file, and the user deleted;
  each of those changes (create, role update, session list and revokes,
  password reset, delete) is in the audit log under `users/<id>`, by the
  administrator;
  while a member, its session is refused account and identity management
  (6). An OIDC identity is linked to the user (dry-run first) and listed; the
  same subject on another user, or again on the same one, is a conflict (7),
  and an issuer other than the configured one is refused; it is unlinked
  (`--yes`, dry-run first), a second unlink answers 404 (4), the freed subject
  links to the other user, and both changes are in the audit log;
  no token or password may appear in any output. Publishing uploads a Raw
  tree (dry run, upload, skip on re-run, re-upload of the changed file only,
  byte-for-byte read-back, canonical coordinates in search) and publishes two
  Maven coordinates through sessions (orchestrated and step by step, with a
  committed session refused and a repeated commit answering 409); a user signs
  in with `auth login --secret-output` and uses the token. It also checks the exit codes for a wrong token (5), a missing
  repository (4), invalid input (2), and conflicts (7).
- `make cli-runtime-e2e` always creates its own Gateway, PostgreSQL and RustFS
  with synthetic credentials and ephemeral loopback ports. It restarts that
  Gateway and checks the changed log session and bounded `scope_changed` result
  from the old cursor; stops only its PostgreSQL to verify unhealthy status and
  not-ready info, restores it, and disables only its log buffer to verify
  `503/log_buffer_unavailable` rather than a successful empty list. A member,
  a pending user and a user required to change their password remain refused;
  unread member diagnostics are unknown and fail `status --strict`. CI and
  `ci-local` run the same target; no existing development stack is reused.
- `make email-compose-check` runs the email commands against an isolated
  Gateway with owned TLS SMTP: a target from a private recipient file, a
  rename that keeps it enabled, a preview and a plan that queue nothing, a
  keyed test delivered exactly once, a refused replay, and a quota rule
  lifecycle, with the recipient absent from all agctl output.
- `make cli-contract-check` verifies that the command map classifies every
  contract operation and that each row marked `implemented` names a
  registered command that declares the row's endpoint.

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
- One-time secrets (service account credentials and API keys) are written
  only to the new private file named by `--secret-output`; without it the
  command refuses before the request. They never reach stdout, stderr, JSON,
  verbose output, plans, or error text (see
  [ADR 0007](adr/0007-agctl-cli-shape.md)). Passwords come only from
  bounded stdin or a file and are redacted the same way.
