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

Artifact Gateway is a complete artifact repository across OCI, Maven, Raw,
Conan, npm, and PyPI Hosted/Proxy/Group paths, plus a read-only Go
Proxy/Group path. Nexus Repository Manager is a mature, general-purpose
artifact repository spanning twenty-plus package ecosystems.

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
| Identity and RBAC | Users, Roles, Privileges, Content Selectors, LDAP/SAML/Crowd/OIDC | Local users, API keys, global reader/writer/admin roles, repository grants with resource prefixes, effective-access explanation, and OIDC role mapping; reusable privilege templates and LDAP/SAML remain future work | Medium |
| Login and SSO entry | Login page, SAML/OIDC buttons, sessions | Local credentials, bearer tokens, and database-configured OIDC authorization-code SSO with encrypted client secrets; session inventory, back-channel logout, and IdP-initiated logout remain future work | Low |
| Global artifact search | Cross-repo component, checksum, class-name, tag search | Server-side cross-repository coordinate/path search with permission filtering and deep links; checksum/class-name/saved queries remain future work | Medium |
| Upload and publish UI | UI upload for many formats, drag-and-drop | Maven publish wizard and Raw upload UI; OCI and Conan use native clients | Medium |
| Repository editing | Rename, change endpoint, convert type | Proxy endpoint/allowlist editing with optimistic concurrency; name/format/type remain immutable | Low |
| User and role admin pages | Full Security section | Users, API Keys, Access Control and repository Grants pages; LDAP/SAML/privilege designer remain future work | Medium |
| Task scheduler | User-created scheduled and manual tasks | Administrator-defined fixed-interval repository/audit retention schedules, manual dispatch, enable/disable controls, and dispatch history; cron and broader task types remain future work | Low |
| Storage backend management | Multiple blob stores (file/S3/Azure), groups, compaction | Single MinIO/S3 store; no compaction UI | Medium |
| Security and vulnerability scanning | Repository Health Check, Firewall, IQ integration | Configurable external multi-asset scanner with durable manual jobs, bounded transport, optimistic intelligence merge, versioned admission policies, and promotion-time evidence propagation; scan-on-publication, vulnerability databases, and malicious-component blocking remain future work | Medium |
| Dashboard visualization | Trends, throughput, top-N charts | Capacity-by-format visualization and locally sampled repository/storage trends; server-side time series, throughput, and top-N analytics remain future work | Low |
| Distribution job controls | Pause, retry, cancel, delete | Replication cancel/retry/run-now controls, lifecycle Jobs view, and repository-level intelligence reconciliation; general scheduler remains future work | Low |
| Notifications | Webhooks, email/SMTP | None | Low |
| API key governance | Scoped roles, expiry, last-used | Optional global role, repository-scoped `repositories:intelligence` writer scope for CI/scanners, 90-day default and 365-day maximum expiry, revocation, last-used tracking | Low |

## Functional Gaps

### Identity And Authorization

Nexus models identity as Users assigned Roles that aggregate fine-grained
Privileges scoped to a repository, format, path, or content selector, with
external identity through LDAP, SAML, Crowd, OIDC, and Active Directory plus
external role mapping.

Artifact Gateway supports break-glass static tokens, local users, API keys,
and a runtime PostgreSQL-backed OIDC configuration. The OIDC authorization-code
flow uses HttpOnly sessions, encrypted client secrets, discovery/JWKS
validation, and reader/writer/admin role mapping. Local users and API keys have
bounded global roles, expiry/revocation where applicable, and last-used
tracking.

Repository grants add read/write/admin permissions for users, API keys, or
external actors and may be narrowed by a canonical resource prefix. The
administrator-only effective-access endpoint evaluates a concrete actor,
global role, repository, and resource through the same authorization chain
used by protocol requests, then explains the source and reason for every
decision.

Authorization templates now provide reusable named grant sets with canonical
resource prefixes, versioned edits, administrator-only management, and an
optimistic-concurrency-protected apply operation. Remaining gaps are richer
selector composition beyond a canonical prefix, external directory protocols
such as LDAP/SAML, and server-side session inventory/revocation. Anonymous
access remains an explicit global-and-repository policy rather than a managed
anonymous role.

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
rebuild repository browse, export. Artifact Gateway provides an
administrator-managed task catalog for repository retention and audit
retention. Tasks use bounded fixed intervals, may be enabled or disabled, can
be dispatched manually, and retain a history linked to the existing lifecycle
or audit-cleanup job. PostgreSQL `SKIP LOCKED` claiming prevents duplicate
scheduled dispatch across Scheduler replicas, while downtime recovery emits
only one run instead of a catch-up storm. Arbitrary commands and SQL are not
accepted. Cron expressions, index rebuilds, blob compaction, exports, and
additional task types remain gaps.

### Security And Quality Scanning

Nexus integrates repository health checks, component vulnerability scanning,
and malicious-component blocking through IQ Server and Firewall. Artifact
Gateway stores administrator-supplied signatures, SBOM references, provenance,
license identifiers, and vulnerability summaries behind a separate
`repositories:intelligence` write scope. Versioned admission policies can
require that evidence before promotion, and the promotion worker propagates
immutable evidence to the target without overwriting target-owned records.
Automatic scanner execution, vulnerability databases, malicious-component
blocking, compliance reports, and component popularity/download metadata
remain future work.

### Shared Format Depth

Even within the supported formats, depth gaps remain. Some are documented
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

The Console is a desktop-focused React 19 and Ant Design 6 single-page app with
grouped, collapsible navigation, global artifact search, public browsing, and
format-aware repository detail tabs. It supports Chinese/English and
light/dark themes. The remaining gaps are concentrated in richer artifact
intelligence and operator tooling rather than basic navigation.

### Authentication Entry

The Console `/login` route supports local credentials, bearer tokens, and a
dynamically configured OIDC authorization-code flow. OIDC settings, encrypted
client-secret replacement, discovery testing, callback handling, role mapping,
and logout are administered without restarting the Gateway. Manually entered
bearer tokens remain localStorage-based; production deployments should use
HTTPS and prefer the HttpOnly OIDC session path.

### Navigation And Information Architecture

The sidebar groups Runtime, Governance, and Management destinations and can be
collapsed with its state persisted locally. The application header provides
permission-filtered global artifact search, theme and language controls, and
credential actions. A command palette and a richer identity/session menu are
still absent.

### Dashboard Visualization

The dashboard combines current repository/storage/audit summaries with a
capacity-by-format donut and lightweight repository/storage sparklines. The
sparklines use throttled browser samples, so server-side time-series storage,
request throughput, cache hit rate, and top-artifact analytics remain gaps.

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

The following Nexus operator pages are absent: Blob Stores, Routing Rules,
Email/SMTP, HTTP/SSL, Capabilities, and configurable feature flags. The
Operations page exposes scheduled retention tasks, background job history, and
an administrator-only diagnostics view with sanitized build identity,
dependency reachability, runtime-node health, and repository queue evidence.
The diagnostics JSON can be copied into a support record without exposing
credentials; downloadable log/database bundles remain future work.

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
by leases. Promotion and retention are represented as lifecycle jobs.
Repository and audit retention can also be dispatched by the fixed-interval
task scheduler; promotion/replication schedules and arbitrary Nexus task types
are not exposed, while cron scheduling remains a future capability.

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

### Internationalization And Desktop Scope

The Console provides Chinese/English copy, Ant Design locale switching,
localized dates and numbers, and light/dark themes. The product intentionally
targets desktop operator workflows; mobile navigation and compact data-table
reflow are not release requirements.

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
  links including Maven SNAPSHOT build numbers. The search recognizes full or
  bare SHA-256 values, returns a `matchKind`, locates historical visible
  OCI/Conan versions, and includes bounded signature/SBOM/license/vulnerability
  summaries so operators can triage risk without opening every artifact.
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
  a return path, and the header provides logout and credential switching. The
  OIDC authorization-code flow, runtime PostgreSQL configuration, encrypted
  client secret, discovery test, HttpOnly session, and reader/writer/admin role
  mapping can be administered from the Console. (`da12dbc5`, `773a54cf`)
- **Access-control overview (P0/P3).** A central `/access` page aggregates every
  repository's managed grant set into one filterable view (principal, repository,
  scope, resource prefix), pairs it with a reader/writer/admin role-capability
  reference, and deep-links each row to the repository's Grants tab. An
  administrator can evaluate a current or simulated principal against a
  concrete resource and inspect the source/reason for every permission
  decision. The page also manages reusable authorization templates and applies
  them to selected repositories with `If-Match` protection. (`f5f2f9c9`)
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
- **Hosted lifecycle coverage** is delivered for Maven, OCI, Raw, Conan, npm
  and PyPI:
  retention policies expose dry-run, protection patterns, version caps, CSV
  export and queued jobs; replication uses fenced, resumable checkpoints.

## Prioritized Backlog

A suggested sequence for closing the gaps, scoped so each item is
independently deliverable.

1. **P1 Automatic artifact security evidence.** Checksum/digest indexes and
   exact global digest search are delivered. A format-neutral artifact
   intelligence contract now stores administrator-supplied signatures, SBOM
   references, provenance, license identifiers, and vulnerability summaries by
   immutable repository/format/coordinate/digest identity; versioned admission
   policies evaluate promotions, and immutable evidence propagation is visible
   through lifecycle operations. The Console renders these facts in
   format-aware detail views. A bounded, streaming HTTP scanner adapter now
   defines the multi-object scanner contract, verifies every streamed asset,
   and validates summary responses. Durable scan scheduling, format-specific
   asset resolution, vulnerability databases, automatic evidence persistence,
   and per-finding vulnerability detail remain future work.
2. **P1 Privilege/content-selector management.** Extend the delivered reusable
   grant templates with selector composition beyond the current repository
   grant prefixes, retaining effective-access simulation as the preview and
   diagnostics tool.
3. **P1 System diagnostics and support bundle.** Sanitized build, runtime-node,
   dependency, queue, and runtime-role evidence is delivered; add downloadable
   logs and bounded database evidence without exposing credentials.
4. **P2 Server-side dashboard trends.** Add time-series metrics, throughput,
   cache-hit rate, and storage-growth visualization.
5. **P2 Broaden scheduled task types, blob store management, and system settings pages.**
6. **P3 Notifications and ecosystem breadth.** Add webhooks/email and additional
   package formats; tombstone hard-purge remains intentionally out of scope.

This backlog is advisory. The authoritative delivery objective and completion
criteria remain [the full repository goal](full-artifact-repository-goal.md);
this document records the competitive surface that goal does not yet cover.
