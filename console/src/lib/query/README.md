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
- **Retries** happen once, and only for a network failure or a 5xx answer.
  Authorization, validation and not-found answers are final.
- **Identity scope.** `ConsoleQueryProvider` clears the whole cache when the
  token or the signed-in actor changes, so protected answers never outlive
  their principal. It must sit inside `AuthProvider`.
- **Tests** wrap rendered pages in `TestQueryProvider` (`src/test/queryClient.tsx`):
  a fresh cache per render and no retries. Query results arrive a scheduler
  tick after the mocked request resolves, so wait with `findBy…` for anything
  that depends on a second read.

## Failure containment

Wrap independent page sections in `SectionBoundary`
(`components/ui/SectionBoundary.tsx`). A render failure then shows an error and
Retry inside that section only, instead of replacing the page with the route
error page. Give it `resetKeys` (route id, selected task) so moving elsewhere
clears a previous failure, and `onReset` to refetch the section's data.

## Migration checklist

Components that still read in `useEffect` and import the SDK. Migrate a page
when it is next touched, together with its split under issue 275; a few
entries only use effects for non-data work and leave the list once checked.

- [ ] `app/CommandPalette.tsx`
- [ ] `app/Layout.tsx`
- [ ] `features/access-control/AccessControl.tsx`
- [ ] `features/access-control/AuthorizationRolesPanel.tsx`
- [ ] `features/access-control/AuthorizationTemplatesPanel.tsx`
- [ ] `features/artifact-detail/ArtifactIntelligencePanel.tsx`
- [ ] `features/artifact-detail/ArtifactQuarantinePanel.tsx`
- [ ] `features/artifact-detail/ArtifactRowDetail.tsx`
- [ ] `features/artifact-detail/ArtifactScanStatus.tsx`
- [ ] `features/artifact-detail/OciImageDetail.tsx`
- [ ] `features/audit/AuditRetention.tsx`
- [ ] `features/audit/Audits.tsx`
- [ ] `features/auth/Login.tsx`
- [ ] `features/dashboard/Dashboard.tsx`
- [ ] `features/groups/Groups.tsx`
- [ ] `features/groups/pages/GroupBrowsePage.tsx`
- [ ] `features/groups/pages/GroupResolutionDialog.tsx`
- [ ] `features/identity/ApiKeys.tsx`
- [ ] `features/identity/ServiceAccounts.tsx`
- [ ] `features/identity/Users.tsx`
- [ ] `features/identity/users/UserCreateDialog.tsx`
- [ ] `features/identity/users/UserDetailsDrawer.tsx`
- [ ] `features/identity/users/UserIdentitiesPanel.tsx`
- [ ] `features/identity/users/UserRepositoryAccessPanel.tsx`
- [ ] `features/identity/users/UserSessionsPanel.tsx`
- [ ] `features/operations/HTTPErrorRatePanel.tsx`
- [ ] `features/operations/Operations.tsx`
- [ ] `features/operations/RuntimeLogsPanel.tsx`
- [ ] `features/operations/RuntimeNodesPanel.tsx`
- [ ] `features/operations/ScheduledTasksPanel.tsx`
- [ ] `features/operations/SystemDiagnosticsPanel.tsx`
- [ ] `features/operations/WebhookDeliveriesPanel.tsx`
- [ ] `features/public-browse/PublicBrowsePage.tsx`
- [ ] `features/repository/APTOperationsTab.tsx`
- [ ] `features/repository/ProxyMavenCacheSection.tsx`
- [ ] `features/repository/RepositoryArtifactSelect.tsx`
- [ ] `features/repository/RepositoryArtifactsTab.tsx`
- [ ] `features/repository/RepositoryBrowseTree.tsx`
- [ ] `features/repository/RepositoryCapacityTab.tsx`
- [ ] `features/repository/RepositoryDistributionTab.tsx`
- [ ] `features/repository/RepositoryGrantsTab.tsx`
- [ ] `features/repository/RepositoryLifecycleTabs.tsx`
- [ ] `features/repository/RepositoryRetentionTab.tsx`
- [ ] `features/repository/RepositoryScanningTab.tsx`
- [ ] `features/repository/RepositorySecurityTab.tsx`
- [ ] `features/repository/RepositoryUsageTab.tsx`
- [ ] `features/search/Search.tsx`
- [ ] `features/settings/Authentication.tsx`
- [ ] `features/settings/SiteSettings.tsx`
- [ ] `lib/preferences.tsx`
- [ ] `lib/siteSettings.tsx`
