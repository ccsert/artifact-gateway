import { Button } from "antd";
import type { ReactNode } from "react";
import { usePreferences } from "../../lib/preferences";
import { EmptyState, ErrorBanner, isNotFound, Loading } from "./Feedback";
import { FilterBar } from "./ConsolePrimitives";
import { Card, PageHeader } from "./Layout";

/**
 * The frame of a list page: the header, then the page's sections in the
 * standard vertical rhythm. Dialogs and drawers go last among the children.
 */
export function ListPage({
  title,
  description,
  actions,
  children,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="ag-page-stack">
      <PageHeader title={title} description={description} actions={actions} />
      {children}
    </div>
  );
}

interface StateCopy {
  title: string;
  hint?: string;
}

/**
 * The four states of a list read, in the order the page shows them:
 *
 * - a failure with nothing to show is the error (or `unavailable`, when the
 *   connected gateway does not serve the endpoint at all);
 * - nothing loaded yet is the spinner;
 * - a loaded, empty list is `empty`;
 * - otherwise the content, under the error when a later read failed while
 *   earlier rows stay on screen.
 */
export function ListStates({
  rows,
  loading = false,
  error,
  onRetry,
  empty,
  unavailable,
  children,
}: {
  /** `null` until the first read answers. */
  rows: readonly unknown[] | null | undefined;
  /** A read is in flight; only shown while there is nothing to show. */
  loading?: boolean;
  error: unknown;
  onRetry?: () => void;
  empty: StateCopy;
  unavailable?: StateCopy;
  children: ReactNode;
}) {
  const hasError = error !== null && error !== undefined;
  const nothing = !rows || rows.length === 0;
  if (hasError && nothing) {
    return unavailable && isNotFound(error) ? (
      <Card>
        <EmptyState title={unavailable.title} hint={unavailable.hint} />
      </Card>
    ) : (
      <ErrorBanner error={error} onRetry={onRetry} />
    );
  }
  if (!rows || (loading && rows.length === 0)) return <Loading />;
  if (rows.length === 0) {
    return (
      <Card>
        <EmptyState title={empty.title} hint={empty.hint} />
      </Card>
    );
  }
  return (
    <>
      {hasError ? <ErrorBanner error={error} onRetry={onRetry} /> : null}
      {children}
    </>
  );
}

/**
 * A card whose first row is the list's filters, with one "Clear filters"
 * action while any filter differs from its default; the table follows.
 */
export function FilterCard({
  filters,
  active,
  onClear,
  className,
  children,
}: {
  filters: ReactNode;
  /** Some filter differs from its default. */
  active: boolean;
  onClear: () => void;
  className?: string;
  children: ReactNode;
}) {
  const { t } = usePreferences();
  return (
    <Card className={className}>
      <FilterBar
        className="border-x-0 border-t-0 rounded-none"
        actions={
          active ? (
            <Button type="text" onClick={onClear}>
              {t("ui.clearFilters")}
            </Button>
          ) : undefined
        }
      >
        {filters}
      </FilterBar>
      {children}
    </Card>
  );
}
