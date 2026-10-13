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

Components that still read in `useEffect` and import the SDK. Migrate a page
when it is next touched, together with its split under issue 275; a few
entries only use effects for non-data work and leave the list once checked.

- [x] `app/CommandPalette.tsx`
- [x] `app/Layout.tsx`
- [x] `features/access-control/AccessControl.tsx`
- [x] `features/access-control/AuthorizationRolesPanel.tsx`
- [x] `features/access-control/AuthorizationTemplatesPanel.tsx`
- [x] `features/artifact-detail/ArtifactIntelligencePanel.tsx`
- [x] `features/artifact-detail/ArtifactQuarantinePanel.tsx`
- [x] `features/artifact-detail/ConanArtifactDetail.tsx`
- [x] `features/artifact-detail/MavenArtifactDetail.tsx`
- [x] `features/artifact-detail/ArtifactScanStatus.tsx`
- [x] `features/artifact-detail/OciImageDetail.tsx`
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
- [ ] `features/public-browse/usePublicVersionPages.ts` — OCI/Conan incremental
      state was extracted unchanged under issue 275; migrate its reads separately.
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
- [ ] `lib/preferences.tsx`
- [x] `lib/siteSettings.tsx`

Quota alerts and email notifications (`features/system`) keep their own
`useQuotaSnapshot` and `useQuotaAction` for now. Their denial handling differs
from the shared layer: a password-change or permission denial blocks the whole
workspace until an explicit refresh succeeds, and an older read or a late
mutation never lands after a newer one. Their own tests pin this. Move them
onto queries in a separate change that keeps those rules.
