# Changelog

[简体中文](CHANGELOG.zh-CN.md) | [Documentation index](docs/README.md)

All user-visible changes to Artifact Gateway are recorded in this file.

Artifact Gateway follows semantic versioning. Pre-1.0 releases are usable
distributions whose contracts can still evolve. Changes are collected under
`Unreleased`; a release moves them to a dated version heading without rewriting
their meaning.

## Unreleased

- Repository grants can now be written one row at a time. `POST /repositories/{repositoryId}/grants` creates or replaces the single grant identified by its principal and resource prefix, and `DELETE` on the same path removes exactly that row, answering `404` when it is already gone. Neither operation takes an `If-Match` precondition, because the write is confined to one grant row and cannot clobber a concurrent change to another principal — the failure mode that made the whole-list `PUT` risky as grant lists grow. Both operations bump the grant-set version and are audited as `repository.grants.upsert` and `repository.grants.delete`; the whole-list replace stays for authorization templates and bulk edits.

- The console's grant editors now work one row at a time instead of replacing the whole grant set behind the scenes. The repository's access tab lists its grants as a table with per-row edit and remove actions, and both it and the per-user repository access panel write through the single-grant endpoints, so a concurrent change to another principal can no longer be silently clobbered and the `412` version-conflict retry loop is gone from both surfaces. An edit that moves an entry to a different resource prefix is an upsert of the new row followed by a delete of the old one, so the edit never forks one grant into two.

- The repository detail tabs no longer double their spacing: the gap between two tab labels was the 12px gutter plus 10px of padding on each side, and the first tab sat 10px inset from the content edge. Spacing now comes from a single 24px gutter, so labels sit exactly 24px apart and the first tab is flush with the summary above and the surface below.

- The audit log's operation column now reads as a localized label instead of the raw machine code: `repository.grants.upsert` shows as "Upsert repository grant" (写入仓库授权), plain traffic codes as "Read (GET)" (读取（GET）), and so on for the full set of codes the gateway writes. The raw code stays on the cell's tooltip and is still what the operation filter and the CSV export carry, and a code the console does not know — one written by a newer backend — degrades to showing the code itself rather than a blank.

- Grant rows now carry one shared identity helper instead of three hand-rolled keys. The repository table joined the principal and resource prefix with a space in one place and a dash in another, and a literal NUL byte sat in the file — git and grep treated it as binary — so two different grants could collapse into the same React key and removing one row could take another with it. Changing a row's principal or resource prefix now removes the old row before writing the new one, so a failed removal leaves the grant narrower, or unchanged, rather than wider than the administrator asked for.

- The rules for writing a grant are now one set of functions shared by the single-row endpoints, the whole-list replace, and the authorization template editor. The single-row endpoints had drifted apart: an upsert accepted a principal of any length and a prefix up to the repository format's path limit, while the delete refused a principal over 512 bytes or a prefix over 255 and rejected control characters, so a grant could be created and then be impossible to remove from either console surface. Writes now enforce exactly the shape the contract declares, and delete only requires enough to name a stored row, so a row written by an earlier revision stays removable. The console's principal and prefix fields carry the same limits.

## 0.4.0 - 2026-09-26

- The level migration ships with the report it owed the instances it cannot decide on its own. `make member-role-migration-report` reads a deployment's database and writes nothing, so it runs before the upgrade as an inventory and after it as a confirmation. It prints four sections, each row naming the identifier to act on: principals that still reach repositories only through `GATEWAY_REPOSITORY_READERS` or `GATEWAY_REPOSITORY_WRITERS` and hold no grant on the repository they read, with the number of decisions and the last one seen; grants held by API keys and service accounts, which have no user row to show them, with the credential's name and state; values the model cannot express, namely accounts whose level is not `none`, `member`, or `admin` and keys carrying more than one level or an unrecognized one; and grants that can no longer take effect, because their repository is missing or inactive or because they name a `user:`, `api-key:`, or `service-account:` principal that does not exist. A token actor without one of those prefixes is deliberately not listed. The upgrade check seeds one instance per class and asserts the report lists it and does not claim an actor that already holds a grant. It also keeps a pre-upgrade dump of its probe database, restores it, and asserts the restored database still carries the removed levels and not the converged constraints, so the documented rollback for this migration is verified rather than assumed.

- A member with no repository grants is told where repository access comes from. The repository catalog's empty state described how to create a repository, which a member cannot do and which is not the reason the list is empty; it now names the actual reason — access is granted per repository by a platform administrator — and a platform administrator still reads the creation copy.

- A repository's Console page follows the repository's own authority instead of the platform flag. The management tabs — access grants, retention, security admission, capacity, tombstones, promotion and replication, lifecycle jobs, signed snapshots, and settings — appear for that repository's administrators, publishing for a write grant, scanning for the independent intelligence scope, and the artifact and usage views for any read authority. The platform administrator keeps every tab, and a tab whose authority is missing is not offered even when the URL names it.

- The V1 compatibility surface stays a platform-administrator operation. The `/api/v1/**` handlers do not take part in the platform/repository split: they predate per-repository authority, and a V1 Group member may carry no repository binding, so nothing attributes it to a repository an administrator could hold. The two tiers live on the v2 surface, and a V1 client that needs repository-scoped administration has to move to `/api/v2`.

- Scheduled tasks now follow the repository they target. A repository retention task is created, read, updated, run, and deleted by the administrators of that repository or by a platform administrator, the catalogue lists only the tasks a caller administers, and moving a task from one repository to another needs authority over both the repository it leaves and the one it reaches. A task with no repository, the audit retention job, remains a platform operation.

- Two repository management operations now belong to the repository's administrators instead of the platform administrator alone. Testing a repository's egress proxy and applying an authorization template to a repository's grants both act on exactly one repository, so a `member` holding `repositories:admin` on that repository may perform them, while the same caller is refused on any other repository with an audited denial. The authorization template catalogue, repository creation, and every other platform surface still require the `admin` level. In the same pass `GET /formats` dropped its administrator requirement for the authenticated caller its contract already documented: the format table carries no repository state. The remaining platform and repository tiers are follow-up work.

- Hosted groups follow the repositories they reference. Creating a group, replacing one, changing its members, and deleting it are now allowed for the administrators of every repository the change touches — the members it keeps or drops as well as the ones it adds — and for a platform administrator. A refusal names the member that is out of bounds and records a management audit entry. A member that is not bound to a repository belongs to the platform tier, because nothing attributes it to a repository. The answer is never cached, so removing a member, deleting a repository, or withdrawing a grant takes the group away from a repository administrator on the next request. Group browsing is unaffected.

- The cross-repository management views follow the same tier as the operations they describe. A repository administrator now sees the repository grants, capacity snapshots, and lifecycle jobs of the repositories it administers and nothing else, so a repository it cannot administer contributes no rows and is indistinguishable from one that does not exist; a platform administrator still sees all of them. The audit log is deliberately unchanged: it spans every repository and credential, and an audit view scoped to one administrator's repositories is a separate decision.

- Every `401` now carries a `WWW-Authenticate: Bearer` challenge, so a caller refused for missing credentials learns which scheme to use instead of guessing. Protocol handlers keep their own challenge and are never overwritten: OCI and Raw publish a protocol realm and Conan publishes `Basic`.

- The effective-access endpoint no longer reveals whether a repository exists to a caller that has no authority over it. It answered with a full permission matrix for any known identifier, which let any authenticated caller confirm a repository's existence and read its permission shape. It now answers only for a repository the caller holds read or intelligence authority over at the requested resource, and answers anything else exactly like a repository that does not exist. Intelligence authority counts because a scanning credential manages intelligence without read access; administrators are unaffected and can still evaluate any repository. The endpoint's OpenAPI description and the anonymous-access and Nexus gap documents now state this, replacing text that claimed the endpoint permits no impersonation and is administrator-only, neither of which was true.

- A deployment that configures no reader patterns now refuses an unmatched authenticated caller instead of admitting it. On 0.3.1 and earlier a legacy repository was readable by any authenticated caller whenever `GATEWAY_REPOSITORY_READERS` was empty, silently and under no configured policy; that fallback is gone and the refusal is an ordinary `403` on the legacy Maven, OCI, Raw, and Conan paths. `GATEWAY_LEGACY_READ_DEFAULT=allow` restores the earlier posture and logs a startup warning while it is set, so an upgrade can be staged, but it is a migration aid rather than a posture to keep: inventory the actors that read without a grant, give each an exact repository name or `prefix/*` pattern in `GATEWAY_REPOSITORY_READERS` or a per-repository grant, then unset the variable. There is no catch-all pattern, so every repository an actor needs has to be listed, and an unrecognized value fails closed. Administrators, configured reader patterns, a repository's own grants, anonymous access, and the pending and password-change blocks are unaffected. See the legacy group migration guide for the upgrade and rollback procedure.

- Existing `reader` and `writer` accounts are converted to the `member` level with equivalent per-repository grants on every repository that exists at upgrade time, so nobody loses or gains access on the repositories they already had. Repositories created after the upgrade are deliberately not covered: access to a new repository is an explicit grant rather than an inherited side effect. The migration does not mark any grant set as managed, so repositories keep serving their legacy static readers and writers exactly as before, and `member` is now accepted as an OIDC JIT default role at the database level.

- A grant set that names a principal now decides for that principal even when the set is not marked managed. Previously an explicit per-principal grant was ignored until an administrator replaced the repository's entire grant set, which made a targeted grant silently ineffective; principals the set does not name keep the legacy static policy, so nothing widens.

- Added a `member` account role that carries no repository capability of its own. Approving an SSO account or creating a local account with `member` grants no repository access at all; its reach comes only from per-repository grants, so one role assignment can no longer hand a user every repository. The role is available for users, API keys, OIDC role mappings and the OIDC JIT default, is offered first in the Console role pickers with an explicit label, and the effective-access explainer names it. The `reader` and `writer` roles it replaces are removed in the same release, so no deployment keeps a global role that covers every repository.

- The `reader` and `writer` account levels are removed. Assigning either value is now a `400` on user creation and update and on API key creation, rather than a silent downgrade; the Console pickers, the user-list filter, and the effective-access simulation offer only `none`, `member`, and `admin`; the OIDC role-mapping settings collapse from a reader list and a writer list into one member list, because a mapped external role approves an account instead of granting repository capability; and `users.role` and `api_keys.roles` are now constrained, so the database refuses the removed values too. An external realm role listed in either legacy mapping list stays mapped, now to `member`. Accounts and API keys that still named a removed level are narrowed to `member` during the upgrade — the level they already resolved to, since an unrecognized level granted nothing — while a key that also named `admin` keeps it, and a key with no level stays level-less. The OIDC JIT default role defaults to `member` instead of `reader`, and a stored default of `reader` or `writer` is migrated the same way. Because the migration replaces the reader and writer settings columns rather than leaving them in place, a rollback after this release restores the pre-upgrade database and the matching binary instead of only switching the image.

- A repository configuration change or deletion now requires repository `admin` scope instead of `write`. Updating a repository's endpoint, allowlist, egress, upstream credential, anonymous-read, or strict-publication settings, and disabling a repository entirely, change how that repository behaves for every client, so they now sit in the same tier as its grants, retention, security, and lifecycle operations rather than sharing the publish/delete-artifact tier. A principal holding only `write` scope is refused with `403`. The Console screens for these actions were already administrator-only.

- An account whose password must be changed can no longer reach repository surfaces. The block is enforced inside the authorizer, so management routes, artifact browse, global search, effective-access, publish sessions, protocol reads and writes, and Group member resolution all agree instead of only the administrator-gated routes. Affected requests answer `403 password_change_required` rather than a generic denial, and changing the password stays reachable because it is authenticated separately. The legacy static read and write policy paths apply the same block, so a permissive legacy configuration cannot admit such an account.

- Administrator-only management endpoints now answer an authenticated non-administrator with `403 access_denied` instead of `401`. A missing or invalid credential still returns `401`, and a session that must change its password returns `403 password_change_required`. Clients can now distinguish "sign in again" from "you are signed in but not allowed", which previously could not be told apart.

- Authorization changes are now audited. Replacing a repository's grants, creating, updating, or deleting an authorization role or template, and applying a template to a repository each record a management audit entry naming the acting principal and the changed resource. Previously these mutations left no trace, so `SECURITY.md` asked operators to review permission changes that were never recorded; rejected or conflicting requests are still not recorded as mutations.

## 0.3.1 - 2026-09-24

- Authorized `reader` and `writer` users can now browse repositories and artifacts in the Console. A `writer` can use Raw upload, the Maven publish wizard, and OCI/npm/PyPI publish guides where effective repository write access applies. OCI Docker/Podman publishing uses an administrator-issued service-account credential with a repository write grant. A `reader` has no upload or publish controls. The server filters repository discovery by read permission. Pending users remain blocked, while repository configuration, access grants, and user management remain administrator-only.

## 0.3.0 - 2026-09-24

- OIDC JIT provisioning can now register a user with the explicit `none` role. A first sign-in creates a passwordless local account and stable issuer/subject binding that appears as Pending in the Console Users page; it grants no repository access, including through legacy read defaults or managed grants. Administrators can assign or revoke global reader, writer, and admin roles there. Pending users see an authorization waiting page with a check-again action. Later sign-ins update safe SSO profile fields without overwriting the Gateway role; disabling an account or revoking its linked session still takes effect on the next request. The default JIT role remains reader for compatibility, so operators must choose `none` when enabling this onboarding policy.

## 0.2.0 - 2026-09-24

- Updated the indirect gRPC dependency to v1.83.1 to address GO-2026-6348.

- Added lifecycle artifact download usage. Every successful content download resolved through the Gateway — Hosted, Proxy, or Group, across Maven, OCI, Raw, Conan, npm, PyPI, Go, and APT — is folded at the single audit write point into a durable per-artifact aggregate (`artifact_usage_stats`), so counts accumulate for the artifact's whole lifecycle and survive audit log retention: download count, total bytes, first and last download time, and last actor, keyed by the artifact address clients resolved (a Group download counts for the Group). The management API serves `GET /api/v2/repositories/{repositoryId}/artifact-usage`, and each repository Console gains a Usage (使用统计) tab with lifetime totals and a per-artifact table. HEAD probes, 304 revalidations, publishes, and management operations never count. Repository retention now consumes the same aggregates as cleanup evidence: the dry-run JSON and CSV export attach each candidate's download count and last download time, and a new `keepDownloadedDays` policy field (default 0, preserving current behavior) exempts cleanup units downloaded within the window from the current round regardless of age or version-count reasons, with per-format matching for npm, PyPI, Go, Maven, Raw, OCI manifest pulls, and Conan v2 revisions.


- Added Go Proxy checksum database mirroring, closing the last known Go compatibility gap. A Go Proxy repository that lists a checksum database host in its egress allowlist now serves the go command's `/go/<repository>/sumdb/<name>/supported` probe and forwards `/latest`, `/lookup/`, and `/tile/` requests over allowlisted HTTPS egress. Signed tree bytes, status codes, and content types pass through verbatim and are never cached; the upstream credential is never attached; and mirrored requests follow the repository read policy, so the go command can verify module downloads against a checksum database it can only reach through the Gateway. The native Go E2E gate now proves a real `go` client verifies module downloads through the mirror.

- Added the npm registry identity endpoint. `GET /npm/<repository>/-/whoami` (Hosted, Proxy, and Group) answers an authenticated caller with `{"username": <Gateway principal subject>}` and challenges anonymous requests with `401`; it never consults repository authorization, so `npm whoami` and CI credential preflights work against any npm repository the client can reach. The native npm E2E gate now drives the real npm CLI `whoami` plus the anonymous rejection through the Nexus-compatible roots.

- Added upstream authentication credentials for Go Proxy Repositories. A Go Proxy Repository can store a `none`, `basic`, or `bearer` credential; the secret is sealed with `GATEWAY_SETTINGS_ENCRYPTION_KEY` under the `repository-upstream-auth` purpose, is never returned by the management API, and is applied only to the Go Proxy upstream fetch. `basic` and `bearer` require a secret, switching to `none` removes a stored credential, credentials are stripped on cross-host redirects, and every other format or Repository type rejects `upstreamAuth` with an explicit `400` instead of ignoring it.

- Added a Console disaster-recovery surface for APT signed snapshots. A repository administrator can export any visible or retired snapshot as a portable archive, receive its `sha256:` receipt in the same step for independent storage, and restore an archive against that stored receipt. Restore never derives the receipt from the uploaded bytes. APT Hosted remains an operator preview.

- The Console now exposes every Go lifecycle surface the Gateway already implements: Retention, Security admission, Promote/replicate, Lifecycle jobs, and Tombstones appear for Go Hosted Repositories, scheduled retention tasks accept Go targets, retention and promotion forms use Go module terminology, and the distributed-deployment and authorization-metrics documents list Go where it is accepted.

- Go Hosted Repositories now enforce the default-disabled Quarantine Read Policy. When an administrator enables the policy, a quarantined module version disappears from `@v/list`, cannot be selected by `/@latest`, and returns `403` for every `info`, `mod`, and `zip` representation; ordered Go Groups refuse to fall through past a quarantined higher-priority coordinate. Hosted reads stay compatible by default, administrators can now manage the Go policy through the same versioned management API and Console surface as other formats, and each denial records the standard `quarantine_read_policy` audit evidence.

- Added administrator-only exact APT archive recovery with independently saved backup digests and public-key trust, both signature and canonical package-index checks, atomic metadata visibility, quota reservations, concurrent replay and durable failed-attempt cleanup. Recovery needs no signer service; APT Hosted remains an operator preview.

- Added administrator-only deterministic APT signed-snapshot export and an offline archive integrity command, with published/retired snapshot coverage and Debian installation from exported bytes. APT Hosted remains an operator preview; trusted archive recovery uses independent backup receipts and public-key policy.

- Pin upgrade/restore rehearsals to verified image identities, use v0.1.0 as the upgrade baseline, and verify Group provenance and reader grants after restoration.

- Added Maven/Raw Group directories with per-member provenance, conflict evidence, exact SNAPSHOT builds, and scope-validated Group-only Maven cache entries. Signed navigation expires after configuration, authorization, or ordering changes. Refined the shared directory browser with clearer hierarchy, source links, compact evidence, collapse controls, and responsive file names.

- Maven Groups now try every eligible Proxy after Hosted misses and bind positive/negative cache results to the ordered authorized candidates and upstream settings. Member, permission, allowlist, or egress changes revalidate the result; legacy multi-member entries are refreshed, and a fallback after an upstream failure is not cached as a complete Group resolution.
- Isolated integration test, migration-check, and cleanup commands from the development Compose project, preventing a checkout `.env` or exported project name from redirecting test cleanup to running local services.
- Raw Groups now continue to later Proxy members after a 404, 410, or member-scoped negative-cache hit, while preserving terminal authorization, upstream, and checksum errors. Administrators can inspect the server's Hosted-first candidate order in the Groups Console, separately from configured positions and per-artifact resolution results.
- Added PostgreSQL-backed lazy directory browsing for Maven and Raw Proxy caches, with repository/upstream isolation, live cache evidence, exact timestamped SNAPSHOT nodes, and deep links that preserve asset paths and build numbers. Directory failures can be retried in place; Raw Proxy details no longer show Hosted deletion actions.
- Added a semantic Console theme system with four built-in packages, neutral
  text selection, restrained accent usage, and a reduced-motion-safe radial
  reveal anchored to the theme selector. Administrators can strictly validate,
  preview, install, replace, enable, and delete PostgreSQL-backed theme packages
  without rebuilding the Gateway; version checks, deletion protection, and
  audit records guard the managed lifecycle.
- Replaced decorative empty-state artwork with compact semantic icons,
  actionable explanations, and responsive layouts.
- Added a lazy, server-owned directory tree for Maven and Raw Hosted
  repositories. Signed node IDs and cursors bind navigation to the repository
  and principal, Maven SNAPSHOT nodes retain the exact selected build, and Raw
  paths stay readable while copy and actions preserve their canonical value.
- Refined the sign-in, public catalog, empty-state, and theme surfaces with
  responsive geometry checks and reduced-motion-safe visual behavior.
- Licensed Artifact Gateway under the MIT License and documented the project
  and contribution licensing terms in both README languages.
- Improved Raw usability across the Console: Unicode and space-containing paths
  are displayed as readable names in repository browse, public browse, global
  search, retention previews, and distribution selectors while canonical
  coordinates remain unchanged for protocol actions. Search now preserves
  literal percent signs, download snippets choose a readable shell-quoted file
  name, and file/checksum responses advertise it with `Content-Disposition`.
- Hardened Raw path validation with a 4096-byte encoded limit and rejection of
  control and bidirectional-formatting characters. Upload validation no longer
  silently trims spaces or removes a leading slash, and reports actionable
  errors before sending invalid paths.
- Reused an existing authenticated browser session when moving from public
  browse to the management Console. The public entry now opens the Console
  directly, and `/login` redirects an already authenticated user to the
  requested management location.
- Clarified that a Raw path is a Nexus-compatible mutable reference while
  `path + digest` is the immutable governance and distribution snapshot.

## 0.1.0 - 2026-08-24

- Published the first reproducible, versioned Gateway/healthcheck binary
  archives with migrations and an environment template, plus the Console static
  bundle, resolved OpenAPI contracts, checksums, GHCR images, and CI-qualified
  `main` snapshots. Release binaries and images report the same version and Git
  revision.

- Added a Nexus-style `/repository/<name>/...` migration root for Maven, npm,
  PyPI, Raw, and Go Hosted/Proxy/Group traffic. Real Maven/Gradle, npm,
  twine/pip, Raw HTTP, and Go clients now retain their Nexus base paths;
  generated npm tarball, Raw pagination/upload, PyPI publication, and Go
  publication URLs remain on that root. PyPI accepts Twine uploads at the
  repository root, and Go Hosted accepts Nexus 3.93+'s version-only ZIP upload
  after deriving and authorizing the module identity. The legacy canonical
  Maven prefix reserves the exact target name `maven` to prevent ambiguous
  cross-repository routing.

- Added Go Hosted repositories with authenticated single-ZIP publication,
  canonical module and `go.mod` validation, atomically derived `.info`/`.mod`
  representations, content-addressed PostgreSQL/RustFS persistence, idempotent
  replay, immutable-coordinate conflict rejection, publication scanning,
  management tombstone/restore with protocol, Group, search, and scan-identity
  visibility enforcement, repository retention planning, a 24-hour recovery
  window followed by reference-safe object reclamation and capacity release,
  immutable promotion of the complete three-representation version snapshot,
  checkpointed replication to target-specific verified objects with final
  snapshot and quarantine revalidation,
  Hosted-first mixed Groups, and a real
  `go mod download` acceptance gate.

- Added stable Service Accounts for Jenkins, CI robots, scanners, and
  third-party applications, with one-time expiring credentials, overlapping
  zero-downtime rotation, immediate account disable, Bearer and native-client
  Basic authentication, Repository Grant integration, generated management
  APIs, audit evidence, a bilingual Console workflow, and an isolated release
  gate. Redesigned the public artifact catalog with a clear read-only boundary,
  source and format summaries, repository search, format filters, and
  Hosted/Proxy/Group guidance. The administrator surface now also explains its
  global, Repository, and Group/member gates and blast radius without changing
  the default-deny read-only policy.
- Added each OCI manifest's immutable creation timestamp to repository browse
  responses so consumers can select the newest publication without inferring
  order from tags or digest text.
- Switched the runtime, local Compose, Kubernetes, integration tests, and
  configuration contract fully to RustFS using the official AWS SDK for Go v2;
  removed MinIO services, SDK dependencies, migration tooling, and cutover
  bypasses while retaining fail-closed detection of legacy resources.
- Added the first APT H3 signing hardening slice: remote signers require a
  public-only OpenPGP keyring matching one or two pinned fingerprints, and
  Gateway cryptographically verifies both signatures before visibility. This
  allows a controlled rotation overlap, derives signer identity and algorithm
  from verified keys, validates the complete keyring before preflight can pass,
  and records structured immutable signing evidence without changing
  authorization-reason semantics; the
  OpenPGP dependency chain now includes the CIRCL secp384r1 fix from v1.6.3.
  Repository administrators can now compare the configured old/next trust
  window with the latest visible snapshot in a generated API and bilingual
  Console view, while bounded outcome and latency metrics support operational
  alerting without high-cardinality signer or repository labels. During a
  rolling upgrade, a Console connected to an older Gateway now identifies the
  missing signing-state endpoint as an unavailable feature instead of
  incorrectly reporting that the repository does not exist.
  A dedicated external-signer gate now provisions signer-owned keys, mounts
  them read-only for serving, uses a signer-specific TLS CA, and proves old,
  overlap, new, rejection, and retirement behavior with clean Debian clients.
- Prioritized Cargo sparse-registry planning after APT H3 and deferred NuGet
  repository implementation while retaining its tested parser foundation.
- Kept access evaluation and repository grant editing focused on usable
  authorization principals by hiding disabled users and revoked or expired API
  keys from both principal pickers.

### Added

- Added a preparation-stage bilingual project and documentation entry point,
  an architecture-faithful README hero, a tested local-link documentation gate,
  and `make dev-bootstrap` for idempotent generation of the six credentials
  required by the local PostgreSQL/RustFS development stack.
- Added bilingual, reviewable Mermaid diagrams for the system boundary,
  standalone and split-role deployment, publication visibility, and durable
  background work, generated icon-rich system and deployment architecture
  overviews, plus an evidence-backed guide to the PostgreSQL-native locking,
  queueing, notification, JSONB, search-index, and observability features that
  keep the control plane lightweight.
- Added a reproducible isolated-Docker performance baseline covering stripped
  Go binaries, the distroless runtime image, quiet Gateway/PostgreSQL/RustFS
  memory, authenticated PostgreSQL metadata reads, and 64 KiB Raw reads through
  RustFS, with bilingual evidence, explicit limitations, and a one-command
  runner that removes its containers, volumes, and ephemeral credentials.
- Began the maintainability plan by extracting recursive public OCI metadata
  reads, tag pagination, and protocol-specific repository setup snippets from
  the large Console browse page behind tested pure module seams. Added Chinese
  architecture, contributing, protocol-compatibility, and recovery entry
  points, then completed substantive Chinese companions for every site
  document and added a tested framework-neutral navigation map.

- A bounded Cargo C0 parser foundation that validates official publish framing
  and complete `.crate` gzip/tar archives, derives collision-safe immutable
  crate/version identity from normalized `Cargo.toml`, and translates current
  publish metadata into checksum-owned sparse-index rows. Official
  `cargo package` and `cargo publish` tests exercise the byte boundary without
  admitting Cargo to the public format catalog.
- A pinned, non-root Traefik Ingress for the Docker Desktop Kubernetes overlay,
  exposing the complete same-origin Console, API, and artifact surface at
  `artifact-gateway.localhost` with bounded resources and least-privilege RBAC.
- A bounded NuGet `.nupkg`/`.nuspec` parser with normalized, case-insensitive
  immutable package identity, plus an explicit staged roadmap that keeps the
  format undiscoverable until its executable protocol gates are complete.
- Idempotent local RustFS credential bootstrapping with retained rollback copies
  and a fail-closed guard for unsupported legacy object-store resources.
- A hardened Kustomize base and one-command local Kubernetes deployment with
  Gateway, Console, PostgreSQL, RustFS, idempotent migrations, persistent local
  volumes, health checks, manifest validation, and same-origin protocol routes.
- A staged APT Hosted roadmap covering native publication, atomic signed
  repository snapshots, external signing, lifecycle, scanning, quarantine,
  promotion, replication, and real APT client acceptance gates.
- Streaming Debian binary metadata parsing for gzip, xz, zstd, and uncompressed
  control archives, with server-derived package/version/architecture identity.
- The completed APT Hosted H1 pre-visibility foundation: idempotent quota-
  reserving publication sessions, explicit management-only Hosted provisioning,
  repository-scoped management and binary-safe generated
  OpenAPI clients, streaming `.deb` staging, explicit package/suite/component/
  architecture records, transactionally durable audit evidence, reference-
  checked RustFS orphan collection with heartbeat-fenced lifecycle-job retries
  and cross-instance integration coverage, immutable repository-snapshot records,
  and a private-key-free signer port. Installable Hosted publication remains
  gated on atomic signed snapshots.
- The completed APT Hosted H2 operator preview: deterministic `Packages` and gzip
  indices, Release checksum closure, Acquire-By-Hash objects, signed
  `InRelease`/`Release.gpg` assets, repository-global immutable pool paths,
  audited PostgreSQL visibility switching, Hosted GET/HEAD/range reads, and
  durable reference-checked cleanup of interrupted snapshot objects, a
  generated idempotent snapshot-publish API, loopback reference signer with an
  isolated persistent private key, Console/search/capacity projection, and a
  real signed Debian update/install gate. The gate now also proves exact
  PostgreSQL/RustFS recovery by publishing a later mutation, restoring the
  original signing evidence and every signed/index/package byte, and installing
  with the signer offline. Hosted remains unadvertised until H3 production key
  custody and rotation are complete.
- A pinned RustFS-only object-store baseline for Compose, integration tests,
  and Kubernetes, including streaming, metadata, Range, lifecycle, backup, and
  recovery contract coverage.
- One-command local development lifecycle targets for starting the complete
  stack, checking the Console and Gateway paths, and stopping only the
  checkout-managed Console.
- Administrator-only sanitized system diagnostics covering build identity,
  runtime roles, dependency reachability, node health, and repository job
  queues, with a bilingual Console view and copyable support JSON.
- Role-based `api`, `scheduler`, and `worker` deployments with format- and
  job-specific worker filters.
- PostgreSQL-backed runtime node heartbeats and an administrator inventory of
  node roles and Worker capabilities.
- Server-side aggregate repository management and cross-repository search APIs.
- Per-repository outbound proxy configuration and connectivity checks.
- npm Proxy repositories with verified read-through metadata and tarball
  caching, stale-if-error reads, negative caching, and offline installs.
- npm Group registries that merge Hosted and Proxy package versions with
  Hosted-first conflict resolution, anonymous/grant filtering, and Group-local
  tarball URLs.
- PyPI Hosted, Proxy, and Group repositories with native `pip` upload and
  install flows, normalized project browsing, anonymous/grant filtering, and
  read-through package caching.
- npm and PyPI artifact lifecycle operations covering retention, tombstones,
  restore, object collection, promotion, and replication.
- Go Module Proxy and Group repositories with standard `GOPROXY` endpoints,
  verified read-through caching, offline resolution, anonymous/grant filtering,
  search, capacity accounting, deep-linked Console browsing, and a real Go CLI
  acceptance gate.
- APT Proxy and ordered Group repositories with byte-preserving `dists/` and
  `pool/` reads, conditional metadata revalidation, stale-if-error and range
  responses, anonymous/grant filtering, search, capacity accounting, and
  deep-linked Console browsing.
- Configurable external artifact scanning with durable, idempotent management
  jobs; isolated workers; native Raw, Maven, OCI, npm, PyPI, Go, and Conan asset
  resolution; and optimistic security-intelligence merging.
- An optional non-root Trivy reference-scanner Compose profile with loopback-
  only transport, immutable asset verification, persistent CycloneDX reports,
  license evidence, vulnerability findings, health metadata, and a persistent
  vulnerability database cache.
- Per-repository scan-on-publication policies for Maven, OCI, Raw, npm, PyPI,
  and Conan Hosted repositories, with idempotent lifecycle jobs, audit records,
  and capability-aware Console controls.
- Per-artifact scan status, manual rescan controls, and bounded reconciliation
  for publications that missed automatic scanning or need a failed scan retried.
- Bounded per-vulnerability scanner findings with severity-count consistency,
  immutable evidence persistence, and searchable bilingual Console details.
- Versioned per-artifact quarantine and release controls with optimistic
  concurrency, audit evidence, admission-time enforcement, and worker-time
  protection against queued promotion or replication publication.
- Versioned Hosted quarantine-read policies, disabled by default, with
  protocol-level GET/HEAD denial, aggregate npm/PyPI and Conan closure
  semantics, metadata filtering, Group anti-bypass behavior, PostgreSQL
  persistence, OpenAPI clients, and Console controls.
- Durable administrator-managed Webhook subscriptions for Artifact quarantine
  and release events, with transactional outbox persistence, encrypted HMAC
  secrets, SSRF-safe HTTPS delivery, bounded retry/dead-letter replay, cluster
  leases, OpenAPI clients, audits, and Console operations visibility.
- Local-user governance with profile metadata, case-insensitive identities,
  failed-sign-in lockout, last-sign-in and password-change timestamps,
  mandatory password changes, administrator password resets, revocable
  versioned sessions, and last-active-administrator protection.

### Changed

- Routed APT requests through both the Vite development proxy and the
  production Console container so direct package-client paths cannot fall
  through to the SPA.
- Replaced default coordinate-and-digest entry in repository scanning,
  promotion, and replication workflows with a shared searchable immutable-
  artifact picker backed by protocol-owned canonical identity queries, including
  historical npm/PyPI versions, locally cached Proxy assets, and Conan revisions,
  while retaining advanced exact-identity input as a recovery path.
- Added a discoverable repository Scanning workspace for manual immutable-
  artifact scans, capability and configuration guidance, historical backfill,
  and recent job status.
- Updated retention controls to use Maven, OCI, Conan, Raw, npm, PyPI, and Go
  cleanup-unit terminology instead of Maven fallback copy.
- Reorganized the repository security tab into separate quarantine-read and
  promotion-admission guardrails with format-aware scope, explicit saved and
  unsaved states, and contextual scanner availability.
- Corrected the repository scanning and security layouts with frameless tab
  surfaces, desktop alert and guardrail grids, and overflow-free single-column
  behavior on narrower viewports.
- Unified scan and promotion-intelligence lifecycle execution around shared
  claim, lease, metrics, terminal-state, polling, and PostgreSQL notification
  semantics, so queued work starts promptly without sacrificing polling-based
  recovery.
- Reduced Console repository request fan-out and split large vendor bundles for
  faster navigation and initial loading.
- Compacted repository detail navigation and added restrained interface motion.
- Streamed artifact uploads and bounded database, HTTP, and background-worker
  resource usage.
- Added package-level Go coverage floors and Console lint, formatting,
  accessibility, component-test, and coverage gates.
- Added session-aware distributed node inventory, graceful offline state,
  retention cleanup, and cluster capability health summaries.
- Reworked Console user management around server-side search, filtering, and
  pagination plus a focused account drawer for profile and security actions.
- Isolated artifact publication locks in a bounded, observable PostgreSQL pool
  and made multi-file object and coordinate locks share one backend session.
- Hardened the Raw large-object byte plane with bounded-buffer temporary
  staging, streaming cache publication and reads, true upstream `HEAD`,
  renewable per-request locks, and configurable per-Gateway staging admission
  with `503`, `Retry-After`, and a bounded rejection metric. Raw and OCI
  resumable uploads now persist immutable offset chunks and assemble them once
  at completion instead of rewriting every prior byte for each PATCH; durable
  Raw reclaim now removes residual chunks from completed, cancelled, or expired
  sessions while preserving their PostgreSQL trace. The performance report now
  includes warm 64 MiB Hosted reads and a controlled HTTPS Raw Proxy cold miss
  with verified single-flight and offline cache replay.

### Fixed

- Made Maven Hosted publication Nexus-compatible by default: successful
  standard Maven/Gradle uploads are directly readable without a companion
  integration. Added the default-disabled `mavenStrictPublication` repository
  switch for teams that prefer Gateway coordinate commits and atomic
  per-coordinate visibility, plus bilingual guidance and real-client coverage
  for both modes.
- Generated Maven-compatible SNAPSHOT metadata using the latest timestamped
  version value and separate extension/classifier fields, so standard Maven
  clients can resolve POM, JAR, sources, and javadoc assets while older
  immutable builds remain directly addressable; generated metadata now also
  serves SHA-512, SHA-256, SHA-1, and MD5 sidecars through Hosted and Group
  routes for warning-free client verification.
- Served npm package-version metadata through Hosted, Proxy, and Group routes,
  including cold proxy resolution and group tarball URL rewriting, so Corepack
  can install pinned package-manager versions through Artifact Gateway.
- Replaced expired native Maven staging sessions on the next authenticated PUT
  so interrupted publishes can retry without remaining permanently blocked by
  the expired coordinate lock.
- Resolved npm Proxy and Group tarballs directly from a cold `package-lock.json`
  URL before any packument request, accepted canonical single-root manifest
  layouts used by legacy scoped packages plus harmless dot segments emitted by
  official packages, retained valid versions when unrelated legacy metadata
  lacks modern integrity, removed dist-tags that target skipped versions,
  requested bounded install metadata and accepted large public packuments,
  retained online-to-offline `npm ci` caching, and emitted one member-owned
  terminal audit for metadata failures.
- Honored repeated OCI `Accept` request headers when selecting manifest media
  types, matching Docker clients that send one header field per supported type.
- Prevented OpenAPI contract checks from reinstalling dependencies underneath
  a running Vite Console, and replaced the default lazy-route exception page
  with a bilingual recovery screen.

- Resolved public artifact deep links even when the target coordinate is beyond
  the first browse page.
- Fixed PostgreSQL OCI upload expiry scans so scheduled reclaim jobs are
  created and completed reliably across worker instances.
- Stabilized generated OpenAPI output, dependency installation, integration
  readiness, and lifecycle ordering in CI.
- Included PyPI and Go object usage in aggregate repository capacity views.
- Prevented PyPI promotion, replication, and restore from publishing or
  restoring a partial version when its file membership changes mid-operation;
  parked replication plans now refresh checkpoints on exact replay.
- Corrected user-management audits so the actor is the administrator performing
  the action and self-service password changes use their own audit resource.

### Security

- Updated the Go toolchain and release images to 1.26.6 so release-readiness
  checks run on the patched standard library baseline.
