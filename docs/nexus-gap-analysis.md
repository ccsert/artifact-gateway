# Nexus Gap Analysis

Status: feature and experience comparison between Artifact Gateway and
Sonatype Nexus Repository Manager. This document owns the cross-product gap
view; the per-protocol compatibility baseline lives in
[the protocol compatibility matrix](protocol-compatibility.md) and the V1
delivery objective lives in
[the full repository goal](full-artifact-repository-goal.md).

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
| Identity and RBAC | Users, Roles, Privileges, Content Selectors, LDAP/SAML/Crowd/OIDC | Static tokens or single OIDC issuer/audience; env-var reader grants; free-text principals | High |
| Login and SSO entry | Login page, SAML/OIDC buttons, sessions | No login page; manual bearer-token paste in a header dialog | High |
| Global artifact search | Cross-repo component, checksum, class-name, tag search | Per-repository browse only; no cross-repo search | High |
| Upload and publish UI | UI upload for many formats, drag-and-drop | Maven publish wizard only; OCI, Raw, Conan have no upload UI | Medium |
| Repository editing | Rename, change endpoint, convert type | Create and delete only; no edit | Medium |
| User and role admin pages | Full Security section | Absent entirely | High |
| Task scheduler | User-created scheduled and manual tasks | Lifecycle-driven jobs only; no general scheduler or UI | Medium |
| Storage backend management | Multiple blob stores (file/S3/Azure), groups, compaction | Single MinIO/S3 store; no compaction UI | Medium |
| Security and vulnerability scanning | Repository Health Check, Firewall, IQ integration | None | Medium |
| Dashboard visualization | Trends, throughput, top-N charts | Instantaneous stat cards only; no charts | Low |
| Distribution job controls | Pause, retry, cancel, delete | Replication create and view only; promotion fire-and-forget | Medium |
| Notifications | Webhooks, email/SMTP | None | Low |
| API key governance | Scoped roles, expiry, last-used | Hard-coded `admin` role; no expiry; no last-used | Medium |

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

- Raw supports GET/HEAD with a single byte range; Nexus Raw Hosted provides
  broader PUT/listing and checksum behavior.
- Conan supports Conan 2 v2 REST; Conan 1, remote-to-remote copy, and general
  upstream index aggregation are unsupported.
- OCI and Conan lack garbage-collection visibility and resumable-upload
  recovery operator surfaces that Nexus exposes around Docker repositories.

## Console And UI Experience Gaps

The Console is a dark-themed React 19 single-page app with eight routes
(`console/src/app/router.tsx:12`). Its information architecture is a flat
sidebar of seven links plus a tabbed repository detail page
(`console/src/app/Layout.tsx:7`). The gaps against Nexus are substantial.

### Authentication Entry

There is no login route. Authentication is a bearer token entered through a
Set Token modal in the sticky header (`console/src/app/Layout.tsx:76`,
`TokenDialog`). There is no SSO button, no session management, no logout, and
no expiry indication. The token is persisted in `localStorage` under
`ag.console.token` (`console/src/lib/auth.tsx:5`).

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

Artifact browsing exists only inside a single repository detail Artifacts tab
(`console/src/pages/RepositoryDetail.tsx:79`). There is no global or
cross-repository search, no checksum or digest search box, no class-name
search, no tag search, no saved queries or filter presets, and no
download-popularity sorting. Groups can be created and reordered but there is
no view of the artifacts a Group would resolve; only legacy Proxy Groups offer
a try-fetch (`console/src/components/ProxyGroupDetail.tsx`).

### Upload And Publish UI

Only Maven has a publish UI: a three-step wizard
(`console/src/components/MavenPublishWizard.tsx:28`) that declares a
coordinate, uploads objects, and commits. OCI push, Raw upload, and Conan
upload have no Console surface; users must use native CLI clients. There is no
general drag-and-drop upload component.

### Repository Management Operations

Repositories can be created and deleted
(`console/src/pages/Repositories.tsx:197`). The generated management client
exposes only `listRepositories`, `createRepository`, `deleteRepository`, and
`getRepository` (`console/src/client/sdk.gen.ts:149`); there is no edit, rename,
endpoint change, allowlist edit, or hosted-to-proxy conversion.

### User, Role, And Security Pages

Nexus ships a full Security section: Users, Roles, Privileges, Anonymous, LDAP,
and Capabilities. Artifact Gateway has none of these pages. The only
identity-adjacent surface is API Keys, where every key is created with the
hard-coded role `admin` (`console/src/pages/ApiKeys.tsx:20`), with no scoping,
no expiry, and no last-used timestamp.

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

Replication plans can be created and their checkpoint progress viewed
(`console/src/pages/RepositoryDetail.tsx` Distribute tab), but there is no
pause, cancel, retry, abort, or delete control. Promotion is fire-and-forget:
there is no promotion list and no status tracking.

### Notifications And Feedback

There is no toast system, no global job-status indicator, and no webhook or
email configuration. The Jobs tab auto-refreshes only while open, every ten
seconds (`console/src/pages/RepositoryDetail.tsx` Jobs tab), so job completion
is invisible once the user navigates away.

### Audit Experience

The audit page (`console/src/pages/Audits.tsx:9`) is view-only with filters by
repository, group, outcome, and format. There is no CSV or other export, no
saved searches, and no saved filter presets. Audit-log retention is
configurable and executable (`console/src/pages/AuditRetention.tsx:14`), which
is a point of parity.

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
  `/search` results page fan the existing per-repository `artifact-search`
  endpoint across every readable repository and aggregate hits. Server-side
  cross-repo search remains a future enhancement. (`6dc6dace`)
- **Audit CSV export (P3).** The audits page exports the currently filtered
  records to a UTF-8 BOM CSV via a reusable `lib/csv.ts`. (`103e9117`)
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
- **Login page (P0).** A standalone `/login` route verifies the pasted bearer
  against the management API before persisting it, an auth guard redirects
  unauthenticated visits with a return path, and the header gains a logout
  button. The token-paste dialog remains for switching credentials. The OIDC
  single-sign-on (auth-code) flow remains a backend follow-up. (`66eb4684`)
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
- **API key scoping beyond `admin`** is partly delivered: `reader`/`writer`/
  `admin` roles now exist and are enforced (see `01651d84`). A full
  user/role/privilege management surface (users, teams, content selectors,
  role assignment UI) remains future work and overlaps with the P0 RBAC item.

## Prioritized Backlog

A suggested sequence for closing the gaps, scoped so each item is
independently deliverable.

1. **P0 Login page and OIDC/SSO entry.** Replace the token-paste dialog with a
   real login route and IdP redirect. The current flow is not usable in
   production.
2. **P0 User, role, and privilege management.** Introduce a user/role model
   and the corresponding Security pages. Lack of RBAC is the main enterprise
   adoption blocker.
3. **P1 Global artifact search and cross-repository browse.** Surface the
   existing `searchRepositoryArtifacts` and coordinate-list endpoints behind a
   global search bar and a browse view.
4. **P1 Repository editing and artifact-level operations.** Add an
   `updateRepository` operation and per-row delete and download UI.
5. **P2 Dashboard charts and trends.** Add time-series metrics, throughput,
   and storage-growth visualization.
6. **P2 Task scheduler, blob store management, and system settings pages.**
7. **P2 OCI, Raw, and Conan upload UI and a shared upload component.**
8. **P3 Distribution controls** (pause, retry, cancel, delete), tombstone
   purge, API key scoping and expiry, webhooks and email, i18n, and mobile
   responsiveness.

This backlog is advisory. The authoritative delivery objective and completion
criteria remain [the full repository goal](full-artifact-repository-goal.md);
this document records the competitive surface that goal does not yet cover.
