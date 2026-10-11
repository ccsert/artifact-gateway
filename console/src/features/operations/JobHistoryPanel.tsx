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

/**
 * Background jobs across repositories. Reads on mount, so a deep link to the
 * tab loads too, and polls every 15 seconds while a job is active and the
 * tab is the one on screen.
 */
export function JobHistoryPanel({ active }: { active: boolean }) {
  const { locale, text } = usePreferences();
  const compactLayout = useCompactOperationLayout();
  const kindLabel = (kind: string) => {
    const labels = KIND_LABELS[kind];
    return labels ? text(labels[0], labels[1]) : kind;
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
      title: text("任务", "Job"),
      key: "identity",
      width: 250,
      render: (_, row) => (
        <OperationIdentity row={row} kindLabel={kindLabel} text={text} />
      ),
    },
    {
      title: text("状态与进度", "Status and progress"),
      key: "status",
      width: 250,
      render: (_, row) => <OperationStatus row={row} text={text} />,
    },
    {
      title: text("时间线", "Timeline"),
      key: "timeline",
      width: 200,
      render: (_, row) => (
        <OperationTimeline row={row} locale={locale} text={text} />
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      width: 140,
      render: (_, row) => (
        <OperationActions
          row={row}
          acting={actingJob === row.id}
          detailsOpen={expandedJobKey === operationRowKey(row)}
          text={text}
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
              label: text("任务总数", "Jobs"),
              value: rows?.length ?? "—",
              hint: text("当前保留窗口", "Current retention window"),
            },
            {
              label: text("运行中", "Running"),
              value: rows ? running : "—",
              hint: text("正在处理", "In progress"),
              tone: running ? "success" : "default",
            },
            {
              label: text("待处理", "Pending"),
              value: rows ? pending : "—",
              hint: text("等待 worker", "Waiting for a worker"),
              tone: pending ? "warning" : "default",
            },
            {
              label: text("失败", "Failed"),
              value: rows ? failed : "—",
              hint: failed
                ? text("需要检查失败原因", "Review failure details")
                : text("当前没有失败任务", "No failed jobs"),
              tone: failed ? "danger" : "success",
            },
          ]}
        />
      </div>
      <Card>
        <div className="ag-operation-records-header">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold tracking-tight text-fg-strong">
              {text("执行记录", "Job history")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
              {rows
                ? text(
                    `显示 ${visibleRows.length} / ${rows.length} 条任务`,
                    `Showing ${visibleRows.length} of ${rows.length} jobs`,
                  )
                : text("正在读取任务记录", "Loading job records")}
            </p>
          </div>
          <Button
            icon={<ReloadOutlined />}
            onClick={() => void load()}
            loading={loading}
          >
            {text("刷新", "Refresh")}
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
                {text("清除筛选", "Clear filters")}
              </Button>
            ) : undefined
          }
        >
          <FilterField
            label={text("状态", "Status")}
            className="ag-operation-filter-field min-w-[150px]"
          >
            <Select
              className="w-full"
              value={stateFilter}
              onChange={setStateFilter}
              options={[
                {
                  value: "all",
                  label: text("全部状态", "All statuses"),
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
            label={text("任务类型", "Job type")}
            className="ag-operation-filter-field min-w-[180px]"
          >
            <Select
              className="w-full"
              value={kindFilter}
              onChange={setKindFilter}
              options={[
                {
                  value: "all",
                  label: text("全部类型", "All types"),
                },
                ...kindOptions.map((kind) => ({
                  value: kind,
                  label: kindLabel(kind),
                })),
              ]}
            />
          </FilterField>
          <FilterField
            label={text("仓库", "Repository")}
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
                  label: text("全部仓库", "All repositories"),
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
            <Loading label={text("加载任务状态…", "Loading job status…")} />
          </div>
        ) : visibleRows.length === 0 ? (
          <div className="px-5 py-8">
            <EmptyState
              title={text("没有匹配的任务", "No matching jobs")}
              hint={text(
                "调整筛选条件，或等待任务产生后再刷新。",
                "Adjust the filters or refresh after new jobs are created.",
              )}
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
                text={text}
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
                <OperationDetailsPanel row={row} text={text} />
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
