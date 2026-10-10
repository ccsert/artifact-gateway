# Console end-to-end tests

Two suites protect the Console. Both are merge gates for the Console refactor
series (#284).

| Suite           | Config                        | Backend                                   | Command               |
| --------------- | ----------------------------- | ----------------------------------------- | --------------------- |
| Behaviour       | `playwright.config.ts`        | Real Gateway; specs mock what they assert | `make console-e2e`    |
| Visual baseline | `playwright.visual.config.ts` | Fully mocked, no Gateway                  | `make console-visual` |

## Visual baseline

`e2e/visual/` screenshots the main surfaces in Gateway Dark and Gateway Light
at 1440×900 with a frozen clock. Every Gateway request is served from
`visual/fixtures.ts`, whose payloads are checked against the generated
`src/client` types. A request without a fixture fails the test, so a page that
starts calling a new endpoint cannot leak an error state into the baseline.

Screenshots only compare reliably with identical fonts and Chromium, so both
targets run inside the pinned `mcr.microsoft.com/playwright` image (Docker
required). The image version comes from the lockfile; each run uses an independent
anonymous dependency volume, reclaimed by Docker when the container exits:

```bash
make console-visual          # compare against the committed baseline
make console-visual-update   # accept intentional changes
```

Rules for pull requests:

- Structural refactors must not change any screenshot.
- Intentional design changes update the baseline in the same PR, and the PR
  description lists each changed surface with the reason.
- On CI failure, the `console-visual-diff` artifact holds expected, actual, and
  diff images.

To cover a new surface, add it to `surfaces` in `visual/pages.visual.spec.ts`,
add any missing fixtures, and run `make console-visual-update`.

## Critical flow map

The behaviour each refactor must preserve, and where it is proven.

| Flow                                         | Specs                                                                                                                  |
| -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| Sign-in modes, theme and language on sign-in | `theme-consistency`, `user-governance`, `oidc-keycloak`                                                                |
| Identity and role-based navigation           | `identity-authorization`, `repository-administrator`                                                                   |
| App shell and navigation rail                | `app-shell-layout`, `route-error-page`                                                                                 |
| Dashboard data, windows, partial failure     | `admin-dashboard`, `dashboard-charts-layout`, `dashboard-loading`                                                      |
| Repository list, pagination, empty states    | `admin-list-behavior`, `empty-state`                                                                                   |
| Repository detail tabs, grants, settings     | `repository-detail-layout`, `repository-administrator`, `repository-usage-pagination`                                  |
| Artifact detail and version selection        | `public-version-selection` (Maven, OCI, Conan), `repository-detail-layout` (npm, Cargo), `oci-size-display`            |
| Proxy and group directory browse             | `proxy-directory-browse`, `group-directory-browse`, `groups-resolution-layout`                                         |
| Retention, tombstone restore, lifecycle jobs | `artifact-workflows`                                                                                                   |
| Public browse and search deep links          | `artifact-workflows`, `global-search`, `public-version-selection`                                                      |
| Access control and authorization templates   | `access-control-theme`, `authorization-template-dialog`                                                                |
| Audit log and retention                      | `audit-layout`, `audits-date-filter`, `admin-list-behavior`                                                            |
| Service accounts and API keys                | `service-accounts-layout`, `admin-list-behavior`, `artifact-workflows`                                                 |
| Operations, runtime nodes, logs, diagnostics | `runtime-nodes-health`, `runtime-logs-layout`, `runtime-log-window`, `http-error-rate-layout`, `local-capacity-layout` |
| Quota alerts and email notifications         | `quota-alerts`, `email-notifications`                                                                                  |
| Site settings and themes                     | `site-settings`, `gateway-theme-compatibility`, `theme-motion`                                                         |
| APT signed snapshots                         | `apt-snapshot-operations`                                                                                              |

### Known gaps

Flows without an end-to-end proof yet. Add the spec before refactoring the
page that owns the flow. This PR establishes the first regression baseline;
these gaps keep #270 open until its complete flow acceptance is met.

- Create a repository (form validation through first artifact view).
- Write a repository grant (create, edit, revoke) from Access Control.
- Issue and rotate a service account credential; create an API key.
- Sign out and session expiry.
- Artifact detail for PyPI, Go, Raw, and APT.
