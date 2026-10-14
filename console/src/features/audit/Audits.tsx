import { useSearchParams } from "react-router-dom";
import { useState } from "react";
import { Button, Space } from "antd";
import {
  ClearOutlined,
  DownloadOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { useQueryClient } from "@tanstack/react-query";
import { PageHeader, Card } from "../../components/ui/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  isNotFound,
} from "../../components/ui/Feedback";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useAccessRepositories } from "../access-control/accessQueries";
import { auditOperationLabel } from "./auditOperations";
import { auditOutcomeLabel } from "./auditOutcomes";
import {
  AuditRecordDetail,
  auditColumns,
  type AuditTableRow,
} from "./auditColumns";
import { exportAuditCsv } from "./auditCsv";
import { AuditFilters } from "./AuditFilters";
import {
  auditQuery,
  auditPagesOptions,
  emptyAuditFilters,
  useAuditGroupOptions,
  useAuditPages,
  type AuditFilters as Filters,
} from "./auditQueries";

/** Distinct, non-empty values of one record field, as select options. */
function distinct(values: (string | undefined)[], sort = true) {
  const unique = Array.from(
    new Set(values.filter((value): value is string => Boolean(value))),
  );
  return sort ? unique.sort() : unique;
}

export function AuditsPage() {
  const { locale, t } = usePreferences();
  const queryClient = useQueryClient();
  // `?actor=` opens the log filtered to one principal (linked from a
  // service account); a later change of that parameter starts over.
  const [params] = useSearchParams();
  const urlActor = params.get("actor") ?? "";
  const [filters, setFilters] = useState<Filters>(() => ({
    ...emptyAuditFilters,
    actor: urlActor,
  }));
  const [seededActor, setSeededActor] = useState(urlActor);
  if (seededActor !== urlActor) {
    setSeededActor(urlActor);
    setFilters({ ...emptyAuditFilters, limit: filters.limit, actor: urlActor });
  }
  const updateFilters = (change: Partial<Filters>) =>
    setFilters((current) => ({ ...current, ...change }));
  const audit = useAuditPages(filters);
  const repositoriesQuery = useAccessRepositories();
  const groupOptions = useAuditGroupOptions();
  const repositoryOptions = (repositoriesQuery.data?.items ?? []).map(
    (repository) => repository.name,
  );

  // The page shown and the open row belong to one filter set.
  const scope = JSON.stringify(auditQuery(filters));
  const [view, setView] = useState<{
    scope: string;
    page: number;
    expanded: string | null;
  }>({ scope, page: 1, expanded: null });
  const pages = audit.data?.pages ?? [];
  const page =
    view.scope === scope ? Math.min(view.page, Math.max(pages.length, 1)) : 1;
  const expanded =
    view.scope === scope && view.page === page ? view.expanded : null;
  const showPage = (next: number) =>
    setView({ scope, page: next, expanded: null });
  const setExpanded = (key: string | null) =>
    setView({ scope, page, expanded: key });

  const records = pages[page - 1]?.items ?? null;
  const error = audit.error;
  const refreshing = audit.isFetching;
  const hasNext =
    !audit.isPlaceholderData && (page < pages.length || audit.hasNextPage);

  // Re-read only the first page. Query commits it atomically on success;
  // a failed refresh retains every cached page and the current position.
  const refresh = async () => {
    const options = auditPagesOptions(filters);
    const queryKey = options.queryKey;
    await queryClient.cancelQueries({ queryKey });
    try {
      await queryClient.fetchInfiniteQuery({ ...options, pages: 1 });
      setView((current) =>
        current.scope === scope ? { scope, page: 1, expanded: null } : current,
      );
    } catch {
      // The query owns the error and Retry; keep the displayed page.
    }
  };
  // A page that fails to load keeps the current one on screen.
  const nextPage = async () => {
    if (page < pages.length) return showPage(page + 1);
    const result = await audit.fetchNextPage();
    if (!result.isError && (result.data?.pages.length ?? 0) > page)
      showPage(page + 1);
  };

  const shown = records ?? [];
  const tableRows: AuditTableRow[] = shown.map((record, index) => ({
    key: String(index),
    rowIndex: index,
    record,
  }));
  const option = (label: (value: string) => string) => (value: string) => ({
    value,
    label: label(value),
  });
  const hasFilters = Object.entries(filters).some(([key, value]) =>
    key === "limit"
      ? false
      : key === "dateRange"
        ? (value as Filters["dateRange"]).some(Boolean)
        : Boolean(value),
  );

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("audit.auditLog")}
        description={t(
          "audit.gatewayAccessAndAuthorizationDecisionsNewestFirst",
        )}
      />
      <Card>
        <AuditFilters
          filters={filters}
          onChange={updateFilters}
          repositoryOptions={repositoryOptions}
          groupOptions={groupOptions}
          outcomeOptions={distinct(shown.map((a) => a.outcome)).map(
            option((value) => auditOutcomeLabel(value, t)),
          )}
          formatOptions={distinct(
            shown.map((a) => a.format),
            false,
          ).map(option((value) => value.toUpperCase()))}
          operationOptions={distinct(shown.map((a) => a.operation)).map(
            option((value) => auditOperationLabel(value, t)),
          )}
          actorOptions={distinct(shown.map((a) => a.actor)).map(
            option((value) => value),
          )}
          actions={
            <Space size="small">
              <Button
                icon={<ClearOutlined />}
                disabled={!hasFilters}
                onClick={() =>
                  setFilters({ ...emptyAuditFilters, limit: filters.limit })
                }
              >
                {t("audit.clear")}
              </Button>
              <Button
                icon={<ReloadOutlined />}
                loading={refreshing}
                onClick={() => void refresh()}
              >
                {t("audit.refresh")}
              </Button>
              <Button
                icon={<DownloadOutlined />}
                disabled={!shown.length}
                onClick={() => exportAuditCsv(shown, t)}
              >
                {t("audit.exportCSV")}
              </Button>
            </Space>
          }
        />
      </Card>
      {error !== null && records !== null && (
        <ErrorBanner error={error} onRetry={refresh} />
      )}
      {records === null ? (
        error !== null ? (
          isNotFound(error) ? (
            <Card>
              <EmptyState
                title={t("audit.auditLogIsUnavailable")}
                hint={t("audit.theCurrentBackendDoesNotExposeTheAudit")}
              />
            </Card>
          ) : (
            <ErrorBanner error={error} onRetry={refresh} />
          )
        ) : (
          <Loading />
        )
      ) : shown.length === 0 ? (
        <Card>
          <EmptyState
            title={t("audit.noMatchingAuditRecords")}
            hint={t("audit.clearFiltersOrExpandTheLoadWindow")}
          />
        </Card>
      ) : (
        <Card>
          <ConsoleTable<AuditTableRow>
            rowKey="key"
            dataSource={tableRows}
            columns={auditColumns(t, locale)}
            pagination={false}
            scroll={{
              x: 1080,
              y: "clamp(240px, calc(100vh - 610px), 520px)",
            }}
            rowClassName="cursor-pointer"
            expandable={{
              expandedRowKeys: expanded === null ? [] : [expanded],
              expandedRowRender: ({ record }) => (
                <AuditRecordDetail record={record} t={t} />
              ),
              expandRowByClick: true,
              onExpandedRowsChange: (keys) => {
                const key = keys[keys.length - 1];
                setExpanded(key === undefined ? null : String(key));
              },
            }}
          />
          <div className="ag-pagination-footer flex items-center justify-end gap-3 border-t border-line px-4 py-3 text-xs text-fg-tertiary">
            <span>
              {t("audit.pageRecords", { page, length: shown.length })}
            </span>
            <Space size="small">
              <Button
                disabled={page <= 1}
                onClick={() => showPage(Math.max(1, page - 1))}
              >
                {t("audit.previous")}
              </Button>
              <Button
                disabled={!hasNext || audit.isFetchingNextPage}
                loading={audit.isFetchingNextPage}
                onClick={() => void nextPage()}
              >
                {t("audit.next")}
              </Button>
            </Space>
          </div>
        </Card>
      )}
    </div>
  );
}
