# Nexus Gap Analysis

Status: feature and experience comparison between Artifact Gateway and
Sonatype Nexus Repository Manager. This document owns the cross-product gap
view; the per-protocol compatibility baseline lives in
[the protocol compatibility matrix](protocol-compatibility.md) and the V1
delivery objective lives in
[the full repository goal](full-artifact-repository-goal.md). The concrete
Repository/Group/Hosted/Proxy Console improvement backlog lives in
[the repository Console experience roadmap](repository-console-experience-roadmap.md).

## Scope

Artifact Gateway is a complete artifact repository for its four supported
formats: OCI, Maven, Raw, and Conan. Nexus Repository Manager is a mature,
general-purpose artifact repository spanning twenty-plus package ecosystems.

This analysis intentionally **excludes the difference in supported package
ecosystem count** (Nexus supports npm, NuGet, PyPI, Helm, APT, YUM, Go, Cargo,
RubyGems, Git LFS, Composer, and more). The gaps below are the capabilities a
general artifact repository manager is expected to provide **regardless of how
many formats it admits**. Where a shared format has a depth gap, it is called
out separately in the Shared Format Depth section.

The baseline for the Artifact Gateway side is the completed V1 backend in
[the backend completion checklist](backend-completion-checklist.md) (lifecycle,
protocol, management API, distribution, and operations work is checked off) and
the Console described in `console/src/app/router.tsx`.

## Gap Summary

| Area | Nexus | Artifact Gateway | Severity |
| --- | --- | --- | --- |
| Identity and RBAC | Users, Roles, Privileges, Content Selectors, LDAP/SAML/Crowd/OIDC | Local users, reader/writer/admin roles, repository grants, OIDC role mapping; LDAP/SAML/content selectors remain out of scope | Medium |
| Login and SSO entry | Login page, SAML/OIDC buttons, sessions | Login page supports local credentials and bearer tokens; OIDC JWT validation and role mapping are available; auth-code SSO is not | Medium |
| Global artifact search | Cross-repo component, checksum, class-name, tag search | Server-side cross-repository coordinate/path search with permission filtering and deep links; checksum/class-name/saved queries remain future work | Medium |
| Upload and publish UI | UI upload for many formats, drag-and-drop | Maven publish wizard and Raw upload UI; OCI and Conan use native clients | Medium |
| Repository editing | Rename, change endpoint, convert type | Proxy endpoint/allowlist editing with optimistic concurrency; name/format/type remain immutable | Low |
| User and role admin pages | Full Security section | Users, API Keys, Access Control and repository Grants pages; LDAP/SAML/privilege designer remain future work | Medium |
| Task scheduler | User-created scheduled and manual tasks | Lifecycle-driven jobs only; no general scheduler or UI | Medium |
| Storage backend management | Multiple blob stores (file/S3/Azure), groups, compaction | Single MinIO/S3 store; no compaction UI | Medium |
| Security and vulnerability scanning | Repository Health Check, Firewall, IQ integration | None | Medium |
| Dashboard visualization | Trends, throughput, top-N charts | Instantaneous stat cards only; no charts | Low |
| Distribution job controls | Pause, retry, cancel, delete | Replication cancel/retry/run-now controls and lifecycle Jobs view; general scheduler remains future work | Low |
| Notifications | Webhooks, email/SMTP | None | Low |
| API key governance | Scoped roles, expiry, last-used | Reader/writer/admin roles, 90-day default and 365-day maximum expiry, revocation, last-used tracking | Low |

## Functional Gaps

### Identity And Authorization

Nexus models identity as Users assigned Roles that aggregate fine-grained
Privileges scoped to a repository, format, path, or content selector, with
external identity through LDAP, SAML, Crowd, OIDC, and Active Directory plus
external role mapping.

Artifact Gateway authenticates through static resolver/admin tokens for
break-glass or a single OIDC configuration that validates only
`GATEWAY_OIDC_ISSUER` and `GATEWAY_OIDC_AUDIENCE`. Authorization is a
`GATEWAY_REPOSITORY_READERS` string of `actor=repository-pattern` pairs. There
is no user table, no role model, no privilege hierarchy, and no content
selector. The management Grant resource (`console/src/pages/RepositoryDetail.tsx`
Grants tab) accepts a free-text principal string and a coarse
read/write/admin scope with an optional resource prefix.

- No user entity or lifecycle.
- No roles beyond a single implicit admin.
- No content-selector or path-regex level authorization.
- No LDAP, SAML, Crowd, or Active Directory integration; only OIDC.
- Anonymous access is a boolean switch on a Group and each member, not a
  managed anonymous role.

### Distribution And Integration

Artifact Gateway is comparatively strong here. Immutable promotion creates an
audited destination artifact without mutating the source, and checkpointed
replication persists checkpoints, retries, resumes, and SHA-256-verifies bytes.
The remaining gaps are integration surfaces Nexus provides:

- No staging workflow (Nexus Maven Staging Suite equivalent).
- No webhooks or event publication.
- No email or SMTP notification configuration.
- No routing rules (per-path allow/deny beyond the Proxy host allowlist).
- No external blob destination for replication; replication targets are other
  Artifact Gateway repositories.

### Storage Backend

Nexus supports multiple concurrent blob stores (file, S3, Azure Blob), blob
store groups, and an admin-managed compaction task. Artifact Gateway stores
bytes in a single MinIO-compatible (S3) object store. Reclamation is performed
by the Orphan Collector after a grace period and reference recheck, which is a
more rigorous lifecycle model than Nexus trash, but there is no operator UI to
inspect blob stores, run compaction, or attach a second store.

### Task Scheduling

Nexus exposes a Task Scheduler where an administrator creates scheduled or
manual tasks: rebuild index, compact blob store, purge, run cleanup policies,
rebuild repository browse, export. Artifact Gateway runs only lifecycle-driven
jobs (retention, reclaim, promotion, replication, audit retention). There is
no general task scheduler, no cron configuration, and no UI to create or run
operator tasks.

### Security And Quality Scanning

Nexus integrates repository health checks, component vulnerability scanning,
and malicious-component blocking through IQ Server and Firewall. Artifact
Gateway has no vulnerability scanning, no license or compliance reporting, no
malicious-component blocking, and no component popularity or download metadata.

### Shared Format Depth

Even within the four supported formats, depth gaps remain. Some are documented
in the Known Limitations of [release readiness](release-readiness.md); others
reflect the backend checklist having closed Hosted lifecycle while the
operator-facing surface is thinner:

- Raw supports authenticated PUT/DELETE, prefix listing, derived checksums, and
  resumable uploads in addition to GET/HEAD with a single byte range; conditional
  writes and non-HTTP tooling remain unsupported.
- Conan supports Conan 2 v2 Hosted publication, revision lifecycle, promotion,
  and replication; Conan 1, remote-to-remote copy, and general upstream index
  aggregation are unsupported.
- OCI and Conan lack garbage-collection visibility and resumable-upload
  recovery operator surfaces that Nexus exposes around Docker repositories.

## Console And UI Experience Gaps

The Console is a dark-themed React 19 single-page app with eight routes
(`console/src/app/router.tsx:12`). Its information architecture is a flat
sidebar of seven links plus a tabbed repository detail page
(`console/src/app/Layout.tsx:7`). The gaps against Nexus are substantial.

### Authentication Entry

The Console has a standalone `/login` route for local credentials and bearer
tokens, an auth guard, logout, and token switching. OIDC JWT validation and
role mapping are available on the backend; an interactive auth-code redirect
is still a deployment-specific follow-up. Browser bearer-token persistence
remains localStorage-based and should be fronted by an HTTPS reverse proxy in
production.

### Navigation And Information Architecture

The sidebar is a fixed `w-56` column with seven `NavLink` entries
(`console/src/app/Layout.tsx:145`). There is no global search bar, no command
palette, no user menu, and no collapsible section grouping. Nexus pairs a
global component search bar with a Browse and Settings split.

### Dashboard Visualization

The dashboard (`console/src/pages/Dashboard.tsx:10`) renders five instantaneous
`StatCard` tiles (repository count, group count, format distribution, total
storage bytes and object count, recent audit count) plus two summary tables.
There are no time-series charts, no throughput or request-rate panels, no
storage-growth history, no top-artifacts-by-size, and no chart library in the
stack.

### Search And Browse

The `/search` route performs permission-filtered cross-repository coordinate and
path search, supports server-side pagination, and links directly to the exact
repository artifact (including Maven SNAPSHOT build numbers). Repository
Artifacts tabs provide format-specific version selection and metadata. Checksum
search, saved queries, popularity sorting, and a richer Group resolution view
remain future work.

### Upload And Publish UI

Maven has a three-step publish wizard and Raw has an authenticated upload
surface. OCI and Conan publication intentionally use their native clients so
large/resumable protocol uploads are not duplicated in the Console.

### Repository Management Operations

Repositories can be created, inspected, edited where mutable (proxy endpoint
and egress allowlist), and deleted. Updates use `If-Match` optimistic
concurrency; format, type, and name remain immutable by design.

### User, Role, And Security Pages

Nexus ships a broader Security section including LDAP, SAML and content
selectors. Artifact Gateway provides Users, API Keys, Access Control, grants,
anonymous policy, local roles and OIDC role mapping. API Keys expose bounded
roles, expiry, revocation and last-used timestamps; full privilege/content
selector design remains a deliberate gap.

### System Settings And Operations Pages

The following Nexus operator pages are absent: Blob Stores, Tasks, Routing
Rules, Email/SMTP, HTTP/SSL, Capabilities, System Information, and Support
Bundle generation. Artifact Gateway exposes no system-info, version, or
feature-flag screen.

### Artifact-Level Operations

Single-artifact deletion is only surfaced for OCI tags
(`console/src/components/OciImageDetail.tsx:151`); Maven, Conan, and Raw
artifact rows have no delete button. There is no UI download button, no
component tagging or starring, no favorites, and no rich asset-attribute view
(checksum, size, downloads). Tombstones can be restored
(`console/src/pages/RepositoryDetail.tsx` Tombstones tab) but cannot be
hard-purged through the UI despite a `reclaim` capability.

### Distribution Job Controls

Replication plans can be created, inspected, cancelled while pending, retried,
and run immediately through lifecycle controls. Checkpoint progress is fenced
by leases. Promotion and retention are represented as lifecycle jobs; a general
Nexus-style scheduler is not implemented.

### Notifications And Feedback

There is no toast system, no global job-status indicator, and no webhook or
email configuration. The Jobs tab auto-refreshes only while open, every ten
seconds (`console/src/pages/RepositoryDetail.tsx` Jobs tab), so job completion
is invisible once the user navigates away.

### Audit Experience

The audit page (`console/src/pages/Audits.tsx:9`) is view-only with filters by
repository, group, outcome, format, actor, operation, and an inclusive time
range. It exports the current server page as CSV and uses the signed
`/api/v2/audits/page` cursor endpoint, so the browser does not load the entire
audit table into memory. Saved searches and filter presets remain future work.
Audit-log retention is configurable and executable
(`console/src/pages/AuditRetention.tsx:14`), which is a point of parity.

### Internationalization And Responsive Design

UI strings are hard-coded Chinese with `lang="zh-CN"` and no i18n framework or
language toggle. The layout uses a fixed sidebar and `ml-56` content margin
with no mobile drawer, so it is not usable on small screens.

## Where Artifact Gateway Holds Its Own

This section keeps the comparison balanced. Artifact Gateway is not uniformly
behind; several design decisions are stricter or more modern than Nexus:

- **Lifecycle model.** Tombstone, grace period, reference recheck, and the
  Orphan Collector form a more rigorous deletion model than Nexus trash.
  See [the artifact lifecycle contract](artifact-lifecycle-contract.md).
- **Immutable promotion.** Promotion snapshots an immutable source identity,
  rechecks visibility, and writes an audited destination artifact without
  mutating the source.
- **Checkpointed replication.** Replication persists checkpoints, resumes,
  retries, and SHA-256-verifies bytes. Nexus OSS replication is a Pro feature
  and does not guarantee content integrity at the same level.
- **Contract discipline.** The Console client and the repository-management Go
  contract are generated from `api/openapi/native-hosted.yaml`; `make
  openapi-check` fails on drift. Nexus REST is hand-written.
- **Idempotency.** Repository creation and distribution operations carry an
  `Idempotency-Key`; Nexus has no equivalent.
- **Modern stack.** React 19, Vite, and Tailwind 4 against Nexus 3's older UI
  substrate.

## Delivered Progress

Work shipped against this analysis. Items are partial unless noted; see the
referenced commits.

- **Global cross-repository artifact search (P1).** A header search bar and a
  `/search` results page use the server-side `/api/v2/artifact-search` cursor
  endpoint, enforce per-repository read permissions, and preserve exact deep
  links including Maven SNAPSHOT build numbers.
- **Audit CSV export (P3).** The audits page exports the currently filtered
  records to a UTF-8 BOM CSV via a reusable `lib/csv.ts`. (`103e9117`)
- **Audit cursor paging (P1).** The audit Console uses a signed, filter-scoped
  cursor with server-side time bounds; PostgreSQL and Memory Store share the
  same descending timestamp/id ordering while the legacy array endpoint stays
  compatible.
- **Dashboard storage-by-format donut (P2).** The overview page renders a
  reusable SVG `Donut` of per-format storage usage from existing capacity
  data. (`b7a0beb8`)
- **Dashboard trends (P2).** A reusable SVG `Sparkline` renders recent
  repository-count and storage movement from a throttled localStorage sample.
  This is a stopgap for "no storage-growth history" until a metrics
  time-series endpoint exists. (`f0abdd12`)
- **API key roles — first RBAC slice (P0/P3).** A coarse global `Role`
  (reader/writer/admin) on the principal is honored by the authorizer and the
  legacy Authenticator methods before per-repository grants, so API keys can be
  issued with bounded scope instead of always `admin`. Reader grants read,
  writer grants read+write, admin grants all; the empty role leaves existing
  behavior unchanged. Includes OpenAPI enum values, a create-key role picker,
  and authorization + end-to-end tests. (`01651d84`)
- **Replication cancel (P3).** `DELETE /api/v2/repositories/{id}/replications/{planId}`
  moves a pending or failed replication plan to a terminal `cancelled` state so
  the worker stops retrying it; running plans are not cancellable (409) and the
  record is retained for audit. Adds the store method (Postgres + Memory), the
  `cancelled` state, a console cancel button, and black-box tests. (`481d7c5b`)
- **Raw upload UI (P2).** The Raw repository artifacts tab gains an upload
  button that PUTs a chosen file to `/raw/<repository>/<path>` with the bearer
  token; the server computes the sha256 digest. This is the first publish UI
  for a non-Maven format. (`7317e072`)
- **Login page (P0).** A standalone `/login` route supports local credentials
  and bearer verification, an auth guard redirects unauthenticated visits with
  a return path, and the header provides logout and credential switching. OIDC
  JWT validation and reader/writer/admin role mapping are configured by
  environment; interactive auth-code SSO remains a deployment-specific gap.
- **Access-control overview (P0/P3).** A central `/access` page aggregates every
  repository's managed grant set into one filterable view (principal, repository,
  scope, resource prefix), pairs it with a reader/writer/admin role-capability
  reference, and deep-links each row to the repository's Grants tab. OIDC role
  mapping is now implemented; a full privilege/content-selector designer
  remains future work.
- **Local user management (P0).** A `users` table with bcrypt password hashing,
  a UserStore (Postgres + Memory), admin-only `/api/v2/users` CRUD (the hash is
  never returned), `POST /auth/login` that mints a stateless 12-hour session
  token, and per-request recheck of role and active state. Includes a console
  Users page and a username/password login mode. (`535c51e3` backend,
  `bf22725a` console)
- **Proxy repository editing (P1).** `PATCH /api/v2/repositories/{id}` with an
  `If-Match` guard updates a proxy repository's upstream endpoint and egress
  allowlist; the store, Postgres/Memory implementations, generated contracts,
  console edit dialog, and black-box tests are included. Proxy routing reads
  the fields per request, so edits hot-apply. Rename, format, and type remain
  immutable. (`5bf1db09`)

The OpenAPI contract generation pipeline was verified reproducible (regenerating
`internal/admin/openapi/generated.go`, `console/src/client`, and
`management-runtime-v1.json` from source produces no diff), which de-risks the
backend API additions listed below.

### Findings that constrain later work

- **Tombstone hard-purge on demand** is out of scope by design: the artifact
  lifecycle contract requires a tombstone, a grace period, and a reference
  recheck before any bytes are reclaimed, so an operator "purge now" button
  would violate that safety model.
- **API key governance** is delivered for the current RBAC model: `reader`/
  `writer`/`admin` roles are enforced, keys expire by default after 90 days
  (maximum 365), revocation and last-used timestamps are recorded, and local
  user accounts with the same roles are managed via `/api/v2/users`. Full
  privilege/content-selector design remains future work.
- **Hosted lifecycle coverage** is delivered for Maven, OCI, Raw and Conan:
  retention policies expose dry-run, protection patterns, version caps, CSV
  export and queued jobs; replication uses fenced, resumable checkpoints.

## Prioritized Backlog

A suggested sequence for closing the gaps, scoped so each item is
independently deliverable.

1. **P0 Interactive OIDC SSO.** Add an auth-code redirect/session adapter for
   deployments that require browser-managed IdP sessions; JWT validation and
   environment-driven role mapping are already available.
2. **P1 Privilege/content-selector management.** Add path-aware privilege
   composition beyond the current repository grant prefixes.
3. **P1 Rich global search.** Add checksum/class-name/tag indexes, saved queries,
   and a deeper Group resolution view.
4. **P1 Artifact operations.** Add download/metadata actions for every format
   and promotion status history.
5. **P2 Dashboard charts and trends.** Add time-series metrics, throughput,
   and storage-growth visualization.
6. **P2 Task scheduler, blob store management, and system settings pages.**
7. **P3 Notifications and ecosystem breadth.** Add webhooks/email and additional
   package formats; tombstone hard-purge remains intentionally out of scope.

This backlog is advisory. The authoritative delivery objective and completion
criteria remain [the full repository goal](full-artifact-repository-goal.md);
this document records the competitive surface that goal does not yet cover.
