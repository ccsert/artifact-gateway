# Go Module Path Follows the Source Repository

[简体中文](0008-go-module-path.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

## Context

`go install <module>/cmd/agctl@<tag>` is one of the three agctl install
channels (#128). Through `v0.9.0`, `go.mod` declared
`github.com/artifact-gateway/artifact-gateway`, but the source lives at
`github.com/ccsert/artifact-gateway`. No repository is served at the declared
path, so the Go toolchain answered `repository not found`; requesting the
repository path instead failed with a module path mismatch (#347).

## Considered designs

1. **Move the module path to the repository path.** Rewrite `go.mod` and every
   import to `github.com/ccsert/artifact-gateway`. The toolchain resolves it
   directly from GitHub and the public module proxy, with no extra hosting.
2. **Serve a vanity path.** Keep `github.com/artifact-gateway/artifact-gateway`
   and make it resolvable. A `github.com/...` path cannot carry `go-import`
   metadata, so this needs that GitHub organization and repository to exist,
   which the project does not control today.
3. **Drop the channel.** Leave the path unchanged and document `go install` as
   unsupported.

## Decision

Option 1. The module path is `github.com/ccsert/artifact-gateway`, and
`scripts/release-version-check.sh` keeps the documented install command equal to
the path `go.mod` declares and, when the repository is known, equal to that
repository.

## Consequences

- `go install github.com/ccsert/artifact-gateway/cmd/agctl@<tag>` works from the
  first tag that contains this change. Earlier tags, including `v0.9.0`, still
  declare the old path and cannot be installed this way.
- A module-mode install reports the tag as its version. The Go toolchain does
  not record VCS data for `pkg@version` builds, so `agctl version` shows the
  revision as `unknown`; the release archives and image inject both.
- External code that imported the old path must switch to the new one. The old
  path never resolved, so no published consumer could depend on it.
- If the repository later moves to an organization, the path moves again by the
  same mechanical rewrite, and this ADR is superseded.
