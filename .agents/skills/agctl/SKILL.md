---
name: agctl
description: Use when operating an Artifact Gateway through its agctl command line — onboarding CI identities, publishing or finding artifacts, managing repositories, Groups, grants, users, and credentials, or reading audits, status, and logs — especially from scripts or agents that must act on exit codes and JSON instead of human text.
---

# agctl

`agctl` is the Gateway's command line for CI, platform administration, and
agents. Every command is non-interactive, prints exactly one JSON document
with `--json`, and reports its result through documented exit codes. The full
contract is in `docs/cli.md`; the operation map is `docs/cli-command-map.md`.

## Before you start

1. Build or install: `make cli-build` (local), a release archive, or the
   container image (see `docs/cli.md`).
2. Point it at a Gateway and a credential, in this order of precedence:
   flags (`--server`, `--token`), environment (`AGCTL_SERVER`, `AGCTL_TOKEN`,
   `AGCTL_PROFILE`), then a profile in `~/.config/agctl/config.yaml`.
3. Discover what the installed client can do before planning:

```sh
agctl commands --json
agctl info --json
```

`agctl commands --json` lists every command with its endpoints, minimum
role, and capabilities (`mutating`, `dryRun`, `retry`, `wait`, `follow`,
`pagination`). A command that is not listed does not exist yet: do not
improvise it with raw HTTP.

## Reading results

- Always pass `--json` and decode stdout: `{schemaVersion, ok, data, error,
  meta}`. Never parse human output or stderr.
- Act on the exit code: 0 success, 2 usage (nothing was sent), 3 partial
  (read `data` for what completed), 4 not found, 5 unauthenticated, 6
  forbidden, 7 conflict or stale version, 8 connection failure or wait
  timeout, 1 anything else (read `error.code`).
- Lists carry `{items, nextCursor}`; `meta.pagination` says whether the
  result is `complete`. A single page is not the whole collection: follow
  `nextCursor` with `--cursor`, or use `--all` (bounded by `--max-pages` and
  `--timeout`).
- `data.outcome == "unknown"` means a write may or may not have been applied.
  Follow `data.readBack` before retrying; for creates, retry only with the
  reported `--idempotency-key`.

## Safety rules

- Plan destructive or broad changes first, then confirm explicitly:

```sh
agctl repo delete REPOSITORY_ID --dry-run --json
agctl repo delete REPOSITORY_ID --yes --json
```

- One-time secrets (credentials, API keys, session tokens) go only to a new
  private file with `--secret-output`; they are never printed. Do not copy
  them into logs, commits, issue comments, or chat. Read the file into the
  consuming system and delete it.
- Passwords come only from `--password-stdin` or a private `--password-file`
  (mode 0600).
- Conditional writes use the version just read; a 412 (exit 7) means someone
  else changed the object. Re-read it and decide again — never force.
- Streams (`--follow`) need `--duration` or `--max-events` and end with one
  `end` event; resume from its `resume` field.

## Recipe: give CI least-privilege access

```sh
agctl service-account create release-bot --description "CI publisher" --json
agctl credential issue --service-account ACCOUNT_ID --name ci --secret-output ./ci-token --json
agctl grant upsert --repo REPOSITORY_ID --principal service-account:ACCOUNT_ID --scope repositories:write --json
agctl access check --repo REPOSITORY_ID --actor service-account:ACCOUNT_ID --role member --require write --json
agctl repo connect REPOSITORY_ID --json
```

Store `./ci-token` as a CI secret (`ARTIFACT_GATEWAY_TOKEN`) and delete the
file. `repo connect` prints client configuration with the credential as a
placeholder.

Rotate without downtime: issue a second credential, deploy it, confirm it
with `AGCTL_TOKEN` set to it and `agctl auth whoami --json`, then revoke the
old one:

```sh
agctl credential list --service-account ACCOUNT_ID --json
agctl credential revoke CREDENTIAL_ID --service-account ACCOUNT_ID --yes --json
```

## Recipe: publish

Raw files or directories (objects whose digest already matches are skipped;
re-running after a partial failure is safe):

```sh
agctl publish raw ./dist --repo REPOSITORY_ID --to releases/1.0.0 --json
```

One Maven coordinate through a publish session (resume an interrupted run
with the same `--idempotency-key`):

```sh
agctl publish maven --repo REPOSITORY_ID --coordinate com.example:app:1.0.0 --pom app-1.0.0.pom --file app-1.0.0.jar --json
agctl publish status SESSION_ID --json
```

Other formats publish with their native clients; `agctl repo connect`
prints the configuration.

## Recipe: find and inspect artifacts

```sh
agctl artifact search --global --q app --match contains --json
agctl artifact search --repo REPOSITORY_ID --q releases/ --json
agctl artifact browse --repo REPOSITORY_ID --json
agctl artifact identities --repo REPOSITORY_ID --purpose distribution --json
agctl artifact show ARTIFACT_ID --repo REPOSITORY_ID --json
```

`artifact show` reports intelligence and quarantine as `recorded`, `none`,
`not_permitted`, or `unavailable`; only `recorded` carries data.

## Recipe: audit and health

```sh
agctl status --json
agctl audit list --actor service-account:ACCOUNT_ID --from 2026-01-01T00:00:00Z --json
agctl audit list --follow --max-events 50 --outcome access_denied --json
agctl runtime logs --level WARN --limit 50 --json
agctl diagnostics --json
```

`status` exits 1 when the Gateway is unhealthy (add `--strict` to fail on
degraded or unknown). Runtime logs are local to the process that answered and
are lost on restart; a `scope_changed` end means a restart or another
instance.

## Recipe: platform administration

```sh
agctl repo create npm-hosted --format npm --json
agctl group create npm-all --format npm --member REPOSITORY_ID --json
agctl group resolution GROUP_ID --json
agctl user create alice --role member --password-file ./alice-password --json
agctl user sessions revoke USER_ID --all --yes --json
agctl template apply TEMPLATE_ID --repo REPOSITORY_ID --dry-run --json
agctl access anonymous show --json
```

`member` grants no repository access by itself; repository access always
comes from `grant upsert` or `template apply`.

## When something is missing

If `agctl commands --json` does not list what you need, the CLI does not
offer it yet (planned rows are listed in `docs/cli-command-map.md`). Say so
instead of calling the HTTP API around the CLI, and do not edit production
data, credentials, or migrations to work around it.
