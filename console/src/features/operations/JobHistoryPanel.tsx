import { useMemo, useState } from "react";
import { ClearOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button, Select } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Card } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  ConsoleTable,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import {
  KIND_LABELS,
  operationRowKey,
  type JobAction,
  type OperationRow,
} from "./operationModel";
import {
  OperationActions,
  OperationDetailsPanel,
  OperationIdentity,
  OperationMobileCard,
  OperationStatus,
  OperationTimeline,
  useCompactOperationLayout,
} from "./OperationJobParts";
import { useControlJob, useOperationJobs } from "./operationQueries";
import "./operations.css";

/**
 * Background jobs across repositories. Reads on mount, so a deep link to the
 * tab loads too, and polls every 15 seconds while a job is active and the
 * tab is the one on screen.
 */
export function JobHistoryPanel({ active }: { active: boolean }) {
  const { locale, t } = usePreferences();
  const compactLayout = useCompactOperationLayout();
  const kindLabel = (kind: string) => {
    const labels = KIND_LABELS[kind];
    return labels ? t(labels) : kind;
  };
  const jobs = useOperationJobs({ poll: active });
  const rows = jobs.rows;
  const loading = jobs.isFetching;
  const control = useControlJob();
  const actingJob = control.isPending
    ? (control.variables?.row.id ?? null)
    : null;
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? jobs.error;
  const repositories = jobs.repositories;
  const [stateFilter, setStateFilter] = useState("all");
  const [kindFilter, setKindFilter] = useState("all");
  const [repositoryFilter, setRepositoryFilter] = useState("all");
  const [expandedJobKey, setExpandedJobKey] = useState<string | null>(null);

  const load = () => {
    setActionError(null);
    return jobs.refetch();
  };

  const controlJob = async (row: OperationRow, action: JobAction) => {
    if (!row.repositoryId) return;
    setActionError(null);
    try {
      await control.mutateAsync({ row, action });
    } catch (nextError) {
      setActionError(nextError);
    }
  };

  const visibleRows = useMemo(
    () =>
      (rows ?? []).filter(
        (row) =>
          (stateFilter === "all" || row.state === stateFilter) &&
          (kindFilter === "all" || row.kind === kindFilter) &&
          (repositoryFilter === "all" || row.repositoryId === repositoryFilter),
      ),
    [kindFilter, repositoryFilter, rows, stateFilter],
  );
  const running = (rows ?? []).filter((row) => row.state === "running").length;
  const failed = (rows ?? []).filter((row) => row.state === "failed").length;
  const pending = (rows ?? []).filter(
    (row) => row.state === "pending" || row.state === "retrying",
  ).length;
  const kindOptions = Array.from(
    new Set((rows ?? []).map((row) => row.kind)),
  ).sort();
  const toggleJobDetails = (row: OperationRow) => {
    const rowKey = operationRowKey(row);
    setExpandedJobKey((current) => (current === rowKey ? null : rowKey));
  };

  const columns: ColumnsType<OperationRow> = [
    {
      title: t("operations.job"),
      key: "identity",
      width: 250,
      render: (_, row) => (
        <OperationIdentity row={row} kindLabel={kindLabel} t={t} />
      ),
    },
    {
      title: t("operations.statusAndProgress"),
      key: "status",
      width: 250,
      render: (_, row) => <OperationStatus row={row} t={t} />,
    },
    {
      title: t("operations.timeline"),
      key: "timeline",
      width: 200,
      render: (_, row) => <OperationTimeline row={row} locale={locale} t={t} />,
    },
    {
      title: t("operations.actions"),
      key: "actions",
      width: 140,
      render: (_, row) => (
        <OperationActions
          row={row}
          acting={actingJob === row.id}
          detailsOpen={expandedJobKey === operationRowKey(row)}
          t={t}
          onInspect={toggleJobDetails}
          onAction={(target, action) => void controlJob(target, action)}
        />
      ),
    },
  ];

  return (
    <div className="ag-page-stack">
      <div className="ag-operation-summary">
        <MetricStrip
          items={[
            {
              label: t("operations.jobs"),
              value: rows?.length ?? "—",
              hint: t("operations.currentRetentionWindow"),
            },
            {
              label: t("operations.running"),
              value: rows ? running : "—",
              hint: t("operations.inProgress"),
              tone: running ? "success" : "default",
            },
            {
              label: t("operations.pending"),
              value: rows ? pending : "—",
              hint: t("operations.waitingForAWorker"),
              tone: pending ? "warning" : "default",
            },
            {
              label: t("operations.failed"),
              value: rows ? failed : "—",
              hint: failed
                ? t("operations.reviewFailureDetails")
                : t("operations.noFailedJobs"),
              tone: failed ? "danger" : "success",
            },
          ]}
        />
      </div>
      <Card>
        <div className="ag-operation-records-header">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold tracking-tight text-fg-strong">
              {t("operations.jobHistory")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
              {rows
                ? t("operations.showingOfJobs", {
                    length: visibleRows.length,
                    length2: rows.length,
                  })
                : t("operations.loadingJobRecords")}
            </p>
          </div>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => void load()}
            loading={loading}
          >
            {t("operations.refresh")}
          </Button>
        </div>
        <FilterBar
          embedded
          className="ag-operation-filters"
          actions={
            stateFilter !== "all" ||
            kindFilter !== "all" ||
            repositoryFilter !== "all" ? (
              <Button
                type="text"
                icon={<ClearOutlined />}
                onClick={() => {
                  setStateFilter("all");
                  setKindFilter("all");
                  setRepositoryFilter("all");
                }}
              >
                {t("operations.clearFilters")}
              </Button>
            ) : undefined
          }
        >
          <FilterField
            label={t("operations.status")}
            className="ag-operation-filter-field min-w-[150px]"
          >
            <Select
              className="w-full"
              value={stateFilter}
              onChange={setStateFilter}
              options={[
                {
                  value: "all",
                  label: t("operations.allStatuses"),
                },
                { value: "pending", label: "pending" },
                { value: "running", label: "running" },
                { value: "retrying", label: "retrying" },
                { value: "completed", label: "completed" },
                { value: "failed", label: "failed" },
                { value: "cancelled", label: "cancelled" },
              ]}
            />
          </FilterField>
          <FilterField
            label={t("operations.jobType")}
            className="ag-operation-filter-field min-w-[180px]"
          >
            <Select
              className="w-full"
              value={kindFilter}
              onChange={setKindFilter}
              options={[
                {
                  value: "all",
                  label: t("operations.allTypes"),
                },
                ...kindOptions.map((kind) => ({
                  value: kind,
                  label: kindLabel(kind),
                })),
              ]}
            />
          </FilterField>
          <FilterField
            label={t("operations.repository")}
            className="ag-operation-filter-field min-w-[220px]"
          >
            <Select
              className="w-full"
              value={repositoryFilter}
              onChange={setRepositoryFilter}
              showSearch={{ optionFilterProp: "label" }}
              options={[
                {
                  value: "all",
                  label: t("operations.allRepositories"),
                },
                ...repositories.map((repo) => ({
                  value: repo.id,
                  label: repo.name,
                })),
              ]}
            />
          </FilterField>
        </FilterBar>
        {error ? (
          <div className="p-4">
            <ErrorBanner error={error} onRetry={load} />
          </div>
        ) : !rows ? (
          <div className="px-5 py-8">
            <Loading label={t("operations.loadingJobStatus")} />
          </div>
        ) : visibleRows.length === 0 ? (
          <div className="px-5 py-8">
            <EmptyState
              title={t("operations.noMatchingJobs")}
              hint={t("operations.adjustTheFiltersOrRefreshAfterNewJobs")}
            />
          </div>
        ) : compactLayout ? (
          <div className="ag-operation-mobile-list">
            {visibleRows.map((row) => (
              <OperationMobileCard
                key={operationRowKey(row)}
                row={row}
                locale={locale}
                kindLabel={kindLabel}
                acting={actingJob === row.id}
                detailsOpen={expandedJobKey === operationRowKey(row)}
                t={t}
                onInspect={toggleJobDetails}
                onAction={(target, action) => void controlJob(target, action)}
              />
            ))}
          </div>
        ) : (
          <ConsoleTable<OperationRow>
            className="ag-operation-desktop-table"
            rowKey={operationRowKey}
            dataSource={visibleRows}
            columns={columns}
            expandable={{
              expandedRowKeys: expandedJobKey ? [expandedJobKey] : [],
              showExpandColumn: false,
              expandedRowRender: (row) => (
                <OperationDetailsPanel row={row} t={t} />
              ),
            }}
            pagination={false}
            virtual
            scroll={{ x: 840, y: 520 }}
          />
        )}
      </Card>
    </div>
  );
}
