# Console server state

The Console reads and writes Gateway data through TanStack Query (issue 271).
This directory owns the shared pieces; feature folders own their query hooks
(`features/repository/repositoryQueries.ts` is the reference).

## Rules

- **Read through a hook, not an effect.** Wrap the generated SDK call in
  `unwrap(...)` inside a `queryFn` and pass the query's `signal` to the SDK, so
  a superseded read is cancelled and can never overwrite a newer answer.
- **Errors stay SDK-shaped.** `unwrap` rethrows the SDK's own error value, so
  `ErrorBanner` and `isNotFound` read Problem documents and plain-text gateway
  answers exactly as before.
- **One key factory per resource** in `keys.ts`, broad to narrow
  (`repositoryKeys.all` → `list()` / `detail(id)` → `detail(id)/capacity`).
- **Mutations invalidate, they do not reload by hand.** After a create, update
  or delete, invalidate the narrowest key that covers every read it can have
  changed (`useInvalidateRepositories()` for catalog changes). Keep showing the
  previous data while the refetch runs.
- **Public, anonymous reads** set `meta: { authFailure: "ignore" }`: a 401/403
  is reported on the page and never ends a session.
- **Retries** happen once, and only for a network failure or a 5xx answer.
  Authorization, validation and not-found answers are final.
- **Authorization failures** go through `lib/auth`: 401 ends the expired
  session, clears credentials and drops its cache; 403 keeps the signed-in
  identity and shows the resource's permission error.
- **Identity scope.** `ConsoleQueryProvider` drops every cached answer when
  the token or the signed-in actor changes, so protected answers never outlive
  their principal. It must sit inside `AuthProvider`. Only an answer that is
  the same for every principal (public site settings) sets
  `meta: { sharedAcrossIdentities: true }` and stays; removing a query cancels
  its read, which would leave a long-lived reader waiting.
  The authentication settings save captures its protected query instance as a
  generation before awaiting the SDK write. It only writes the returned settings
  when that same instance still exists, so an old session cannot refill the cache.
- **Tests** wrap rendered pages in `TestQueryProvider` (`src/test/queryClient.tsx`):
  a fresh cache per render and no retries. Query results arrive a scheduler
  tick after the mocked request resolves, so wait with `findBy…` for anything
  that depends on a second read.

## Protocol reads

Package detail pages read the format's own client protocol (npm packument,
PyPI Simple JSON, Go module proxy, Cargo sparse index) rather than the
management API. `features/artifact-detail/protocolDetailQueries.ts` owns these
reads: keys come from `protocolKeys`, each read sends the signed-in token when
there is one, sets `authFailure: "ignore"` because the same pages render in
anonymous public browse, and fails with a `LocalizedError` that keeps the HTTP
status for the retry policy. Optional evidence (the Go zip digest from `HEAD`)
never fails the page. Refresh failures keep the loaded package content with a
warning and Retry. Cargo download errors and requests belong to the selected
package and identity.

## Data shared across pages

- **Writers on unmigrated pages.** A read whose data other pages still write
  without invalidating its key sets `staleTime: 0`, so returning to the page
  always shows their writes (the access directory, grant lists, sessions).
  Drop it once every writer invalidates.
- **One key per resource, everywhere.** Pages that show the same list share a
  key and a `queryFn`; a page that needs another shape uses `select`
  (`useGrantRows` over `useRepositoryGrantRecords`). A write updates or
  invalidates that one key, and every page follows.
- **Permission-gated reads use `enabled`.** Do not request what the signed-in
  identity cannot read: a refusal reaches the session handling above. Gate
  the query on the capability (`platformCapabilities(identity)`) instead.
- **Polling is `refetchInterval`, not an interval effect.** Return `false`
  when nothing on screen needs fresh data (an inactive tab, no active job),
  so a hidden section stops polling and a failed poll keeps the last answer.
- **Dialogs own their drafts.** An editor dialog keeps its form state, and the
  page mounts it with a new `key` per opening while keeping it mounted to
  close. Each opening then starts from its target, and closing still animates.

## Failure containment

Wrap independent page sections in `SectionBoundary`
(`components/ui/SectionBoundary.tsx`). A render failure then shows an error and
Retry inside that section only, instead of replacing the page with the route
error page. Give it `resetKeys` (route id, selected task) so moving elsewhere
clears a previous failure, and `onReset` to refetch the section's data. Return
the refetch promise so Retry waits for its answer before rendering again;
completions from a previous section are ignored. Give shared source answers as
`recoveryKeys`, so sibling sections recover when that source is refreshed.
These data changes cannot clear a failure while Retry is waiting for its
refetch promises; all sources must finish first. Navigation `resetKeys` still
clear the failure immediately and invalidate a previous section's Retry.

Access Control keeps grant metrics and principal projections inside their
boundaries. Its directory uses a query selector for active repositories so a
bad directory answer becomes a query error, and Retry waits for every directory
source with `Promise.allSettled`. Grant refresh errors retain cached rows under
a warning with Retry. Service Account details refetch their credentials before
clearing a failed section. Credential queries reject malformed pages before
they reach the cache or page projections. An initial failure shows a local error
and Retry without a loading placeholder; a refresh failure retains cached rows
under a warning.

## Migration checklist

Components that read in `useEffect`, through the SDK or a plain `fetch`.
Migrate a page when it is next touched, together with its split under issue
275; a few entries only use effects for non-data work and leave the list once
checked. A `fetch` behind a button (delete, export, warm, download, publish) is
an action, not a cached read, and is not listed.

- [x] `app/CommandPalette.tsx`
- [x] `app/Layout.tsx`
- [x] `features/access-control/AccessControl.tsx`
- [x] `features/access-control/AuthorizationRolesPanel.tsx`
- [x] `features/access-control/AuthorizationTemplatesPanel.tsx`
- [x] `features/artifact-detail/ArtifactIntelligencePanel.tsx`
- [x] `features/artifact-detail/ArtifactQuarantinePanel.tsx`
- [x] `features/artifact-detail/CargoCrateDetail.tsx`
- [x] `features/artifact-detail/ConanArtifactDetail.tsx`
- [x] `features/artifact-detail/GoModuleDetail.tsx`
- [x] `features/artifact-detail/MavenArtifactDetail.tsx`
- [x] `features/artifact-detail/NpmPackageDetail.tsx`
- [x] `features/artifact-detail/ArtifactScanStatus.tsx`
- [x] `features/artifact-detail/OciImageDetail.tsx`
- [x] `features/artifact-detail/PyPIProjectDetail.tsx`
- [x] `features/audit/AuditRetention.tsx`
- [x] `features/audit/Audits.tsx`
- [x] `features/auth/Login.tsx`
- [x] `features/dashboard/Dashboard.tsx`
- [x] `features/groups/Groups.tsx`
- [x] `features/groups/pages/GroupBrowsePage.tsx`
- [x] `features/groups/pages/GroupResolutionDialog.tsx`
- [x] `features/identity/ApiKeys.tsx`
- [x] `features/identity/ServiceAccounts.tsx`
- [x] `features/identity/Users.tsx`
- [x] `features/identity/users/UserCreateDialog.tsx`
- [x] `features/identity/users/UserDetailsDrawer.tsx`
- [x] `features/identity/users/UserIdentitiesPanel.tsx`
- [x] `features/identity/users/UserRepositoryAccessPanel.tsx`
- [x] `features/identity/users/UserSessionsPanel.tsx`
- [x] `features/operations/HTTPErrorRatePanel.tsx` — its effect is an elapsed-time
      clock over props, not a read.
- [x] `features/operations/Operations.tsx`
- [x] `features/operations/RuntimeLogsPanel.tsx` — the log stream stays on
      `useRuntimeLogStream`: a followed stream with gap detection is not a cached
      read.
- [x] `features/operations/RuntimeNodesPanel.tsx`
- [x] `features/operations/ScheduledTasksPanel.tsx`
- [x] `features/operations/SystemDiagnosticsPanel.tsx`
- [x] `features/operations/WebhookDeliveriesPanel.tsx`
- [x] `features/public-browse/PublicBrowsePage.tsx`
- [x] `features/public-browse/usePublicVersionPages.ts` — each OCI tag, manifest
      and Conan revision page is a query; the hook keeps which pages were asked
      for and assembles them per coordinate.
- [x] `features/repository/APTOperationsTab.tsx`
- [x] `features/repository/ProxyMavenCacheSection.tsx`
- [x] `features/repository/RepositoryArtifactSelect.tsx`
- [x] `features/repository/RepositoryArtifactsTab.tsx`
- [x] `features/repository/RepositoryBrowseTree.tsx` — the tree loads one node
      page at a time with retry and de-duplication; it is not a cached read.
- [x] `features/repository/RepositoryCapacityTab.tsx`
- [x] `features/repository/RepositoryDistributionTab.tsx`
- [x] `features/repository/RepositoryGrantsTab.tsx`
- [x] `features/repository/RepositoryJobsTab.tsx`
- [x] `features/repository/RepositoryTombstonesTab.tsx`
- [x] `features/repository/RepositoryRetentionTab.tsx`
- [x] `features/repository/RepositoryScanningTab.tsx`
- [x] `features/repository/RepositorySecurityTab.tsx`
- [x] `features/repository/RepositoryUsageTab.tsx`
- [x] `features/search/Search.tsx`
- [x] `features/settings/Authentication.tsx`
- [x] `features/settings/SiteSettings.tsx`
- [x] `lib/preferences.tsx` — its effects sync the document language, stored
      preferences and theme fallback; it reads no server data.
- [x] `lib/siteSettings.tsx`

Quota alerts and email notifications (`features/system`) read through
`useQuotaSnapshot`, a query (`quotaKeys`) that keeps their stricter rules: a
password-change or permission denial blocks the whole workspace until an
explicit refresh succeeds and never ends the session (`authFailure:
"ignore"`); `update` cancels a read in flight before applying a write, and a
refresh it overtook reports failure, so an older read or a late write never
lands after a newer one; returning to the page re-reads at once and replaces a
read in flight, which the query's own focus refetch would not. Their writes
stay on `useQuotaAction`, which runs one action at a time and drops a result
that arrives after its component unmounted.

Snapshot refreshes explicitly cancel even initial reads before refetching. Each
completion is checked against its request generation and cached query instance;
a later refresh cannot validate an older cancelled read, and an old identity
cannot write into a replacement snapshot.

## Reads outside queries

`scripts/read-inventory.mjs` lists every Gateway read in `src`: each generated
SDK operation that issues GET or HEAD, and each `fetch` without another
method. The type checker follows names to their declarations, so a read
counts as a query read when it runs in a `queryFn`, in an argument of a hook
whose `queryFn` calls it (`useQuotaSnapshot`, `useDashboardResource`), or in
code those reach (format listers through their registry). SDK aliases and
namespace calls resolve to their declarations. A helper also called by an
ordinary action is listed outside queries, even if a query calls it too.
Unknown fetch methods are conservatively treated as possible reads.

```sh
node scripts/read-inventory.mjs          # summary and the reads outside queries
node scripts/read-inventory.mjs --json   # every read with its file, line and context
```

`src/test/readInventory.test.ts` fails when a read runs outside a query and
is not one of these exceptions, or when an exception no longer exists:

- **Documented exceptions.** The authentication bootstrap (`/auth/session`
  sits above the query provider and decides its identity), the repository
  browse tree (one node page at a time with retry and de-duplication) and the
  followed runtime log stream (gap detection, not a cached read).
- **One step of an action.** A read the person's own action needs right
  then: finding the Maven artifact id before deleting it, exchanging credentials
  for an OCI registry token (also shared with queries), the publish session
  while uploading, the `.crate` download, a proxy warm-up fetch, the APT
  snapshot export, current grants before applying a template or removing a
  user's access, and verifying a pasted token at sign-in.

Explicit re-reads (refresh the latest version after a conflict) use
`useExplicitRead`; reads opened on demand (a plan's progress, an access
evaluation) are queries keyed by what was asked for.

The quota editor retains its opening catalogue and cursor while the parent
snapshot refreshes, preserving loaded choices and the draft. Explicit reads
apply a result only while their query instance and component generation still
match; cancellation cannot reapply a previous cached success.

This list records the remaining non-query scope; it does not revise issue
284's acceptance criterion. That issue remains open until the exception scope
is explicitly accepted or the remaining reads are migrated.
