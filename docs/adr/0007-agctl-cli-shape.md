# agctl: One Client Binary and Its Cross-Cutting Contract

[简体中文](0007-agctl-cli-shape.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

## Context

Remote platform management had no first-class client contract:

- curl-driven scripts under `scripts/` repeat URL building, token handling, and
  exit-code logic, with no shared error model;
- `gateway` subcommands (`version`, `backup`, `snapshot-import`, `preflight`,
  `evidence`, `theme`, `apt-snapshot`) exist to run beside the server process,
  not against a remote deployment;
- the Console serves browser interaction, and its session lives in browser
  storage, so scripts and agents have no stable call surface.

The management contract itself is complete — `/api/v2` publishes its OpenAPI
source, and service-account credentials are already the supported CI path — so
the missing piece is a first-class client, not new server capability.

## Considered designs

### 1. Extend the `gateway` binary with remote client subcommands

Rejected. A client must not load server configuration or carry server
dependencies, and mixing local operator commands (which run with the server's
privileges and stores) with a remote bearer-token client muddles the security
model for both.

### 2. Adopt a third-party command framework (cobra)

Evaluated. The stdlib `flag` package matches the repository precedent
(`internal/evidence/cli.go`) and adds zero dependencies; the cost is hand
written help and, later, completion. Decision: stdlib `flag` for the skeleton.
Revisit only if help/completion maintenance cost exceeds the framework's
weight, and record the change here.

### 3. An MCP bridge or a second admin CLI

Rejected. The Gateway already speaks REST; the CLI is the agent interface.
A second bridge or a parallel admin command tree would duplicate authentication,
pagination, and exit-code semantics with no new observer.

### 4. A REPL with undo/redo

Partially rejected. Writes land server-side, so client-side undo has no
coherent meaning; recovery goes through the server's own restore endpoints.
An interactive shell (context, history, completion — no undo) stays in scope
as a later command.

## Decision

**One binary, one contract.** A separate `cmd/agctl` binary is the single
client for CI, platform management, and agents. It programs against the
published `/api/v2` contract and reuses the generated management types
(`internal/admin/openapi`); it never loads `internal/config` or server
dependencies.

**Configuration precedence.** Explicit flag, then environment variable
(`AGCTL_SERVER`, `AGCTL_TOKEN`, `AGCTL_PROFILE`), then the selected profile in
`~/.config/agctl/config.yaml` (mode 0600, multiple profiles), then a usage
error (exit 2). Profile selection follows `--profile`, `AGCTL_PROFILE`,
`current_profile`, the `default` profile, a single-profile file.

**Exit codes are contract.** 0 success; 1 generic failure; 2 usage error;
3 batch partial success; 4 not found (404); 5 unauthenticated or missing
credentials (401); 6 forbidden (403); 7 conflict or precondition (409/412);
8 connection or timeout. Commands fail with exit 2 before sending any request
when required input is missing, and never prompt.

**One JSON envelope.** `--json` (or `-o json`) prints exactly one document:
`{schemaVersion, ok, data, error, meta}`. Errors carry
`{code, httpStatus, message, hint, requestId}`; lists carry
`{items, nextCursor}` with a null cursor when the source has no server-side
pagination, and `meta.pagination` states the real mode. `schemaVersion` starts
at 1 and only changes on a breaking change. Human output goes to stdout;
diagnostics go to stderr; `--verbose` prints request method and URL with
credentials redacted — the token never reaches any writer.

**Client behavior.** A thin HTTP wrapper over the management API: bearer
authentication, one overall timeout, and retries only for idempotent requests
on transport failures, 429, and 502/503/504, with exponential backoff honoring
`Retry-After`. Writes are never retried without endpoint-declared idempotency.
Server problem documents are parsed into the error envelope, and the request
id is surfaced for support.
Server retry delays are not capped by the local backoff limit; the request's
timeout bounds the wait. Redirects are returned without following them.
Authentication failures and unstructured bodies use safe HTTP descriptions;
structured problem messages are redacted before rendering. The `info` probes
preserve the error code, request id, and hint alongside each source's status.

**Introspection is a feature.** `agctl version`, `agctl info` (health,
readiness, caller identity, format catalog), and `agctl commands --json`
(the command tree with per-command resource, scenario tags, stage, endpoints,
and write/dry-run/retry/wait capabilities) make the CLI discoverable by
scripts and agents.

**Secret handling boundary.** One-time secrets (issued tokens, API keys) are
governed by the service-account command work: they never reach stdout, JSON,
stderr, history, or retry caches without an explicit secure destination.
This skeleton establishes the redaction rule for everything it already prints.

## Consequences

The CLI skeleton ships `version`, `info`, and `commands`; the rest of the
command surface arrives in later independently mergeable slices and appears in
`agctl commands --json` only as it is implemented. Distribution (multi-platform
binaries, `go install`, container image) and the OpenAPI-to-command drift guard
are separate issues; until they land, `make cli-build` is the supported local
build. The exit codes and JSON envelope are the machine contract — later
commands extend the envelope and never repurpose a code. The framework choice
is the only provisional part; anything else changes through a new decision
here, not silently.
