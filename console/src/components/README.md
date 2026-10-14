# Console components

Shared building blocks for Console pages. Reach for these before writing page
markup: they carry the spacing, states and interaction rules `DESIGN.md`
asks for, so a page cannot drift into its own variant.

## Pages and lists — `ui/ListPage.tsx`

| Component                               | Use it for                                                                                                                                                                                                                                               |
| --------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ListPage`                              | The frame of a page: `PageHeader` (title, description, actions) and the page's sections in the standard rhythm. Dialogs go last among its children.                                                                                                      |
| `ListStates`                            | The four states of a list read: the error (or `unavailable` when the gateway does not serve the endpoint) when there is nothing to show, the spinner before the first answer, `empty` for a loaded empty list, else the content under any later failure. |
| `FilterCard`                            | The card that holds a list: its first row is the filters, with **Clear filters** while any filter is set; the table goes in `children`.                                                                                                                  |
| `useUrlFilters` (`ui/useUrlFilters.ts`) | Filters kept in the URL search params, so a filtered view survives reload and can be shared. Defaults stay out of the URL; `set` writes several keys at once; `reset` clears them all.                                                                   |

```tsx
const FILTERS = { q: "", state: "active" as "all" | KeyState };

const filters = useUrlFilters(FILTERS, { state: STATES });
return (
  <ListPage title={t("identity.apiKeys")} actions={<CreateKey />}>
    <ListStates rows={keys} error={error} onRetry={load} empty={emptyCopy}>
      <MetricStrip items={summary} />
      <FilterCard active={filters.active} onClear={filters.reset} filters={fields}>
        <ConsoleTable rowKey="id" dataSource={visible} columns={columns} />
      </FilterCard>
    </ListStates>
    <ConfirmDanger … />
  </ListPage>
);
```

`ApiKeys`, `Groups` and `Users` are the reference pages.

## Dialogs — `ui/Modal.tsx`

| Component       | Use it for                                                                                                                                                                          |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Modal`         | Every dialog. Width tiers (form 520, `wide` 1152 for tables), the body height cap, and `busy`, which blocks escape, mask and close while a request is in flight.                    |
| `FormModal`     | A form with one submit action: Enter submits, the dialog locks while `onSubmit` runs, and a thrown error shows above the fields. `onSubmit` throws to fail (`unwrap` the SDK call). |
| `ConfirmDanger` | Confirming a destructive action on one target. Open while `target` is set; runs `onConfirm(target)`, closes on success, and hands a failure to `onError` or shows it in the dialog. |
| `ConfirmDialog` | A confirmation that is not a single-target action. Reversible inline edits use antd `Popconfirm` next to the row instead.                                                           |
| `useDisclosure` | Open/closed state for a dialog trigger.                                                                                                                                             |

`DetailDrawer` (`ui/DetailDrawer.tsx`) is the side panel for one record's
details and edits: pass `busy` while a change is saved and it cannot be
dismissed, and `error` shows the last failure above the content.
`UserDetailsDrawer` is the reference.

## Content and feedback

- `ui/Layout.tsx`: `PageHeader`, `Card`, `CardHeader`, `Field` (label, hint
  and error wiring for one form control), `Pagination` (cursor "load more").
- `ui/ConsolePrimitives.tsx`: `MetricStrip`, `FilterBar` / `FilterField`,
  `ConsoleTable` (themed table, density, shared empty state),
  `CopyableValue` and `useClipboardAction`.
- `ui/Feedback.tsx`: `Loading`, `ErrorBanner` (translates `LocalizedError`),
  `Notice` (something that happened, or a standing explanation),
  `EmptyState`, `isNotFound`.
- `ui/Badge.tsx`: `Badge`, `StateBadge`, `StatusText`, `FormatBadge`,
  `RepositoryTypeBadge`.
- `ui/SectionBoundary.tsx`: keeps one failing section from taking the page
  down; it shows the error and a retry in place.
- `ui/formFeedback.ts`: `fieldFeedback` (aria wiring for a field's error) and
  `useFormValidationFocus` (focus the first invalid field after submit).

## Navigation

Pages are declared once in `src/app/routes.tsx`: path, who may open it
(`signedIn`, `repositories`, `admin`) and, for sidebar pages, label, icon and
group. The router, the sidebar, the command palette and the access redirects
all read that table.
