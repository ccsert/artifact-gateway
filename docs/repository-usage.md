# Repository usage and Console pagination

[简体中文](repository-usage.zh-CN.md) | [Documentation index](README.md)

## Usage API

`GET /api/v2/repositories/{id}/artifact-usage` requires repository read access.
`limit` defaults to 100 and accepts 1–500; `offset` defaults to 0 and accepts
0–1,000,000. `q` is a case-sensitive literal address substring, trimmed at both
ends, with a maximum of 512 Unicode characters. Invalid text, NUL and invalid
numeric parameters return 400. `%`, `_` and backslash are literal characters.

The response contains one page of `items`, `totalCount` for all matching
(format, address) records, and `totals` for the entire repository regardless of
the filter. Counts are computed in SQL; the service does not fetch every address
to count or paginate. PostgreSQL reads all response parts in one read-only
repeatable-read transaction. The memory implementation uses one read lock.

Pages sort by address then format in bytewise ascending order, replacing the
former top-download ranking. Download increments do not reorder existing rows.
Each request is a live view; newly added addresses between page requests can
shift offset boundaries. Return to page one to restart a traversal. This is not a durable
cross-request snapshot or a cursor contract. Deep offsets still have database
cost, and whole-repository totals aggregate all its usage rows.

The Console requests 20 records initially and fetches only the selected page.
Submitting or clearing address search and changing page size reset to page one;
refresh reloads the current page. Changing repositories resets the view. Older
responses cannot overwrite a newer query. A removed last page falls back to the
last valid page with one additional bounded request. Refresh failures retain
previous data with an error and retry action. Empty matching results keep the
whole-repository total visible. Successful download counting, group attribution
and audit-retention independence remain unchanged.

## Shared pagination and inventory

`ConsoleTable` and `useConsolePagination` own the Ant Design 6 baseline: bottom
end placement, middle control size, default 20, choices 20/50/100, and
`1–20 of 100 items` (localized). Size changes reset to page one; controlled server
tables also reset their server query. Local filter owners reset their page.

| Surface | Data protocol | Shared presentation |
| --- | --- | --- |
| Users, repository usage | Server limit/offset and known total | Numbered pages, size choices and total |
| Vulnerability findings; APT packages, deletions, snapshots and export choices | Complete local result already returned by their APIs | Numbered local pages; local filters reset page |
| Repositories, groups, artifacts, global search, retention dry-run candidates, service accounts and credentials, lifecycle tombstones | Server cursor, unknown total | Bottom end load-more footer; no fabricated total or fetch loop |
| Audit records | Bounded server cursor pages, unknown total | Bottom end previous/next controls; page and current record count only, no total or fetch loop |
| Quota-rule repository picker | Server cursor, unknown total; appends only on request | Shared end-aligned load-more action beside the form; no fabricated total |
| Scanning jobs and repository lifecycle jobs | Latest 100 lifecycle jobs, filtered locally by job kind | Explicit bounded summary and `pagination=false`; never claim a complete history count |
| Runtime logs | Bounded older-log cursor | Same footer, explicit “Load older logs”, existing record/byte caps |
| Browse trees and protocol-native/public expandable tables | Branch/protocol cursors or nested projections | Preserve native branch/load-more controls and `pagination=false` |
| Dashboard top-ten/recent rows, grants, roles/templates, API keys, global lifecycle jobs (up to 500), scheduled tasks, operations, runtime nodes, diagnostics, webhook/delivery, audit retention and embedded detail tables | Bounded summaries or complete small lists | Explicit `pagination=false`; no artificial numbered pages |

Adding a table must choose its actual data protocol before enabling pagination.
Never set a loaded batch's length as a server total, or fetch all cursor pages to
simulate numbered pagination. This change does not redesign mobile layouts.
