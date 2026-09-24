# Account Levels and Per-Repository Authority

[简体中文](0006-account-levels-and-repository-grants.zh-CN.md) · [Documentation index](../README.md)

Status: accepted

## Context

Repository authority came from one global role per account. Two properties made
that role a poor fit for restricting access:

- the role check ignored the target repository, so `reader` meant every
  repository and `writer` meant read and write on every repository;
- the role was evaluated *before* per-repository grants, so a global `writer`
  made those grants irrelevant for read and write. Confining an account to
  specific repositories was not expressible.

Administration had the same shape. A single `administrator` boolean gated the
management surface, and every credential source derived it from
`role == admin`, so repository administration could not be granted without
whole-system administration. The per-repository `repositories:admin` scope
already existed and was wired into each repository's management operations, but
it was reachable only as a grant, never as an account assignment.

Two related behaviours were documented postures rather than oversights: the
repository list was administrator-only, and a deployment that configured no
reader patterns admitted any authenticated caller. Both are qualified here, so
the documents move with the code.

## Considered designs

### 1. Keep four global roles and add an optional repository scope

An assignment would carry a repository subset, and an unscoped assignment would
keep today's meaning. Compatibility is best, but the model retains two coexisting
answers to "what may this account reach", which is the ambiguity being removed.
Readers would have to know which form an account was created with.

### 2. Keep four levels as a ceiling over grants

The level would bound the operation anywhere, and grants would bound where. This
gives a crisp "this account may never write" statement, but a `reader` ceiling
silently cuts off an existing `repositories:write` grant, a combination the
previous model allows and a deployment may hold. Migration would have to raise
levels and report the instances it changed.

### 3. Three levels with repository authority from grants only

The level answers "may this account exist at all", and a grant answers "where,
and how much". Rejected only by the cost of migrating existing global roles,
which the migration below absorbs.

## Decision

**Levels.** An account level is `none`, `member`, or `admin`. `member` carries no
implicit repository capability: a level alone admits no repository operation, so
the grant is the only source of repository authority. `admin` is the platform
administrator and keeps the existing management surface.

**Grants decide for the principals they name.** An explicit per-principal entry
is honoured even when the grant set is not marked managed; a set that does not
name the principal keeps the legacy static policy. This is what allows grants to
be materialized without marking every repository managed, which would have cut
off principals that only held a legacy static policy.

**`none` is a hard stop.** An account at `none` is refused even when explicitly
granted, so "pending approval" cannot be bypassed by a grant. Approval means
setting a level.

**Two administration tiers, one of them new.** A platform administrator is the
`admin` level. A repository administrator is a `member` holding
`repositories:admin` on one repository. Operations that change how a repository
behaves for every client — its configuration and its deletion — require that
scope rather than `write`, which only governs publish and delete-artifact.

**Migration.** Existing `reader` and `writer` accounts become `member` with
equivalent grants on the repositories that exist at upgrade time, and no grant
set is marked managed, so legacy static readers and writers keep working.

**The legacy read default is deny.** An unconfigured deployment refuses an
unmatched authenticated caller, which is what 0.3.1 and earlier admitted
silently. `GATEWAY_LEGACY_READ_DEFAULT=allow` restores the earlier posture as a
migration aid and logs a startup warning while it is set, so an operator can
stage an upgrade that has to inventory the actors reading without a grant rather
than have reads break unannounced. An unrecognized value fails closed.

## Consequences

Assigning a role no longer hands out every repository. Reach is a separate,
per-repository decision, and the catalog a caller sees is exactly the set its
grants allow, so an empty result no longer reveals whether a repository exists.

Repositories created after the migration are not covered by it: access to a new
repository is an explicit grant rather than an inherited side effect. That is a
deliberate narrowing rather than a migration gap, and it means the convenience of
"I can read everything" becomes a bulk assignment an administrator performs.

The `reader` and `writer` levels remain accepted for one release so an upgrade
does not fail an external automation that creates accounts with them. Removing
them is a separate change, as is narrowing the Console role pickers to the three
levels.

The management surface is still one platform tier. Splitting it into platform and
repository tiers is deferred: the seams are drawn in the issue tracker, and the
repository-scoped part already has a scope to build on.

The Console now has three grant editors. A per-user assignment view is deferred
until a shared editor is extracted, because a third inline copy would drift from
the other two. The write path also needs read-merge-write orchestration, since
replacing one repository's grants replaces the whole set.
