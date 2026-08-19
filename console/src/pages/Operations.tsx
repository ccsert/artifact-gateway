import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ClearOutlined,
  CloseOutlined,
  InfoCircleOutlined,
  PlayCircleOutlined,
  RedoOutlined,
  ReloadOutlined,
  SyncOutlined,
} from "@ant-design/icons";
import {
  Button,
  Popconfirm,
  Progress,
  Select,
  Space,
  Table,
  Tabs,
  Tooltip,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  cancelRepositoryLifecycleJob,
  listAuditRetentionJobs,
  listLifecycleJobs,
  retryRepositoryLifecycleJob,
  runRepositoryLifecycleJobNow,
} from "../client";
import type { LifecycleJob } from "../client";
import { Card, PageHeader } from "../components/Layout";
import { EmptyState, ErrorBanner, Loading } from "../components/Feedback";
import { StateBadge } from "../components/Badge";
import { formatDate } from "../lib/format";
import { RuntimeNodesPanel } from "../components/RuntimeNodesPanel";
import { ScheduledTasksPanel } from "../components/ScheduledTasksPanel";
import { SystemDiagnosticsPanel } from "../components/SystemDiagnosticsPanel";
import { WebhookDeliveriesPanel } from "../components/WebhookDeliveriesPanel";
import { LifecycleJobDetails } from "../components/LifecycleJobDetails";
import {
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";

type OperationRow = {
  id: string;
  kind: string;
  state: string;
  repository?: string;
  repositoryId?: string;
  createdAt: string;
  startedAt?: string;
  completedAt?: string;
  nextAttemptAt?: string;
  attempts?: number;
  maxAttempts?: number;
  progressCurrent?: number;
  progressTotal?: number;
  progressMessage?: string;
  lastError?: string;
  details?: LifecycleJob["details"];
};

type Localize = (chinese: string, english: string) => string;
type JobAction = "run" | "retry" | "cancel";

function operationRowKey(row: OperationRow) {
  return `${row.kind}-${row.id}`;
}

const KIND_LABELS: Record<string, [string, string]> = {
  retention: ["制品保留", "Artifact retention"],
  promotion: ["制品晋级", "Artifact promotion"],
  replication: ["仓库复制", "Repository replication"],
  reclaim: ["对象回收", "Object reclamation"],
  intelligence: ["情报同步", "Intelligence sync"],
  "audit-retention": ["审计保留", "Audit retention"],
};

function useCompactOperationLayout() {
  const [compact, setCompact] = useState(
    () => window.matchMedia("(max-width: 1120px)").matches,
  );

  useEffect(() => {
    const media = window.matchMedia("(max-width: 1120px)");
    const update = () => setCompact(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  return compact;
}

function OperationIdentity({
  row,
  kindLabel,
  text,
}: {
  row: OperationRow;
  kindLabel: (kind: string) => string;
  text: Localize;
}) {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 items-center gap-2 text-sm font-medium text-zinc-200">
        <SyncOutlined className="shrink-0 text-zinc-500" />
        <span className="truncate" title={kindLabel(row.kind)}>
          {kindLabel(row.kind)}
        </span>
      </div>
      <div
        className="mt-1 truncate text-xs text-zinc-400"
        title={row.repository ?? text("全局", "Global")}
      >
        {row.repository ?? text("全局", "Global")}
      </div>
      <div
        className="mt-1 truncate font-mono text-[11px] text-zinc-600"
        title={row.id}
      >
        {row.id}
      </div>
    </div>
  );
}

function OperationStatus({ row, text }: { row: OperationRow; text: Localize }) {
  const hasProgress = row.progressTotal !== undefined && row.progressTotal > 0;
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <StateBadge state={row.state} />
        <span className="text-[11px] text-zinc-500">
          {row.attempts === undefined
            ? text("未报告尝试次数", "Attempts not reported")
            : text(
                `${row.attempts} / ${row.maxAttempts} 次尝试`,
                `${row.attempts} / ${row.maxAttempts} attempts`,
              )}
        </span>
      </div>
      {hasProgress ? (
        <Progress
          className="mt-2"
          percent={Math.round(
            ((row.progressCurrent ?? 0) / (row.progressTotal ?? 1)) * 100,
          )}
          size="small"
          status={
            row.state === "failed"
              ? "exception"
              : row.state === "completed"
                ? "success"
                : "normal"
          }
          format={() => `${row.progressCurrent ?? 0}/${row.progressTotal}`}
        />
      ) : row.progressMessage ? (
        <div className="mt-2 line-clamp-2 text-xs leading-5 text-zinc-500">
          {row.progressMessage}
        </div>
      ) : null}
      {hasProgress && row.progressMessage && (
        <div className="mt-1 line-clamp-2 text-[11px] leading-4 text-zinc-500">
          {row.progressMessage}
        </div>
      )}
      {row.lastError && (
        <div className="mt-2 line-clamp-2 break-words text-xs leading-5 text-rose-300">
          {row.lastError}
        </div>
      )}
    </div>
  );
}

function OperationTimeline({
  row,
  locale,
  text,
}: {
  row: OperationRow;
  locale: string;
  text: Localize;
}) {
  const primaryLabel = row.nextAttemptAt
    ? text("下次执行", "Next run")
    : row.completedAt
      ? text("完成", "Completed")
      : text("创建", "Created");
  const primaryTime = row.nextAttemptAt ?? row.completedAt ?? row.createdAt;
  return (
    <div className="space-y-1 text-xs text-zinc-500">
      <div>
        <span className="text-zinc-600">{primaryLabel}</span>
        <span className="ml-2 whitespace-nowrap">
          {formatDate(primaryTime, locale)}
        </span>
      </div>
      {row.startedAt && (
        <div>
          <span className="text-zinc-600">{text("开始", "Started")}</span>
          <span className="ml-2 whitespace-nowrap">
            {formatDate(row.startedAt, locale)}
          </span>
        </div>
      )}
      {primaryTime !== row.createdAt && (
        <div>
          <span className="text-zinc-600">{text("创建", "Created")}</span>
          <span className="ml-2 whitespace-nowrap">
            {formatDate(row.createdAt, locale)}
          </span>
        </div>
      )}
    </div>
  );
}

function OperationActions({
  row,
  acting,
  detailsOpen,
  text,
  onInspect,
  onAction,
}: {
  row: OperationRow;
  acting: boolean;
  detailsOpen: boolean;
  text: Localize;
  onInspect: (row: OperationRow) => void;
  onAction: (row: OperationRow, action: JobAction) => void;
}) {
  return (
    <Space size="small">
      <Tooltip
        title={text(
          detailsOpen ? "收起任务详情" : "查看任务详情",
          detailsOpen ? "Hide job details" : "View job details",
        )}
      >
        <Button
          aria-label={text("查看任务详情", "View job details")}
          aria-expanded={detailsOpen}
          type={detailsOpen ? "primary" : "default"}
          size="small"
          icon={<InfoCircleOutlined />}
          onClick={() => onInspect(row)}
        />
      </Tooltip>
      {row.repositoryId &&
        (row.state === "pending" || row.state === "retrying") && (
          <Tooltip
            title={text("立即加入执行队列", "Queue for immediate execution")}
          >
            <Button
              aria-label={text(
                "立即加入执行队列",
                "Queue for immediate execution",
              )}
              size="small"
              icon={<PlayCircleOutlined />}
              loading={acting}
              onClick={() => onAction(row, "run")}
            />
          </Tooltip>
        )}
      {row.repositoryId &&
        (row.state === "failed" || row.state === "cancelled") && (
          <Tooltip title={text("重新执行任务", "Retry job")}>
            <Button
              aria-label={text("重新执行任务", "Retry job")}
              size="small"
              icon={<RedoOutlined />}
              loading={acting}
              onClick={() => onAction(row, "retry")}
            />
          </Tooltip>
        )}
      {row.repositoryId &&
        (row.state === "pending" || row.state === "retrying") && (
          <Popconfirm
            title={text("取消此任务？", "Cancel this job?")}
            description={text(
              "取消后仍可从任务中心重新执行。",
              "You can retry it later from Operations.",
            )}
            okText={text("取消任务", "Cancel job")}
            cancelText={text("返回", "Back")}
            onConfirm={() => onAction(row, "cancel")}
          >
            <Tooltip title={text("取消任务", "Cancel job")}>
              <Button
                aria-label={text("取消任务", "Cancel job")}
                danger
                size="small"
                icon={<CloseOutlined />}
                loading={acting}
              />
            </Tooltip>
          </Popconfirm>
        )}
    </Space>
  );
}

function OperationDetailsPanel({
  row,
  text,
}: {
  row: OperationRow;
  text: Localize;
}) {
  return (
    <div className="ag-operation-details">
      <dl className="ag-operation-details-meta">
        <div>
          <dt>{text("任务 ID", "Job ID")}</dt>
          <dd className="font-mono">{row.id}</dd>
        </div>
        <div>
          <dt>{text("仓库", "Repository")}</dt>
          <dd>{row.repository ?? text("全局任务", "Global job")}</dd>
        </div>
      </dl>
      {row.details && <LifecycleJobDetails details={row.details} />}
      {row.lastError && (
        <div className="break-words rounded-md border border-rose-900/50 bg-rose-950/20 px-4 py-2 text-xs leading-5 text-rose-300">
          {row.lastError}
        </div>
      )}
      {!row.details && !row.lastError && (
        <p className="text-xs leading-5 text-zinc-500">
          {text(
            "此任务没有报告额外的执行详情。",
            "This job did not report additional execution details.",
          )}
        </p>
      )}
    </div>
  );
}

function OperationMobileCard({
  row,
  locale,
  kindLabel,
  acting,
  detailsOpen,
  text,
  onInspect,
  onAction,
}: {
  row: OperationRow;
  locale: string;
  kindLabel: (kind: string) => string;
  acting: boolean;
  detailsOpen: boolean;
  text: Localize;
  onInspect: (row: OperationRow) => void;
  onAction: (row: OperationRow, action: JobAction) => void;
}) {
  return (
    <article className="ag-operation-mobile-card">
      <OperationIdentity row={row} kindLabel={kindLabel} text={text} />
      <div className="mt-4 border-t border-zinc-800/60 pt-4">
        <OperationStatus row={row} text={text} />
      </div>
      <div className="mt-4 border-t border-zinc-800/60 pt-4">
        <OperationTimeline row={row} locale={locale} text={text} />
      </div>
      <div className="mt-4 flex justify-end border-t border-zinc-800/60 pt-3">
        <OperationActions
          row={row}
          acting={acting}
          detailsOpen={detailsOpen}
          text={text}
          onInspect={onInspect}
          onAction={onAction}
        />
      </div>
      {detailsOpen && (
        <div className="mt-4 border-t border-zinc-800/60 pt-4">
          <OperationDetailsPanel row={row} text={text} />
        </div>
      )}
    </article>
  );
}

export function OperationsPage() {
  const { locale, text } = usePreferences();
  const compactLayout = useCompactOperationLayout();
  const kindLabel = (kind: string) => {
    const labels = KIND_LABELS[kind];
    return labels ? text(labels[0], labels[1]) : kind;
  };
  const [repositories, setRepositories] = useState<
    Array<{ id: string; name: string }>
  >([]);
  const [rows, setRows] = useState<OperationRow[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [stateFilter, setStateFilter] = useState("all");
  const [kindFilter, setKindFilter] = useState("all");
  const [repositoryFilter, setRepositoryFilter] = useState("all");
  const [actingJob, setActingJob] = useState<string | null>(null);
  const [expandedJobKey, setExpandedJobKey] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState("schedules");

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [lifecycleResult, auditRetention] = await Promise.all([
        listLifecycleJobs({ query: { limit: 500 } }),
        listAuditRetentionJobs(),
      ]);
      if (lifecycleResult.error) throw lifecycleResult.error;
      if (auditRetention.error) throw auditRetention.error;
      const lifecycleRecords = lifecycleResult.data ?? [];
      setRepositories(
        Array.from(
          new Map(
            lifecycleRecords.map((record) => [
              record.repositoryId,
              { id: record.repositoryId, name: record.repositoryName },
            ]),
          ).values(),
        ).sort((a, b) => a.name.localeCompare(b.name)),
      );
      const lifecycle: OperationRow[] = lifecycleRecords.map((record) => {
        const job: LifecycleJob = record.job;
        const isHistoricalJob =
          job.state === "completed" && job.attempts === 0 && !job.progressTotal;
        return {
          id: job.id,
          kind: job.kind,
          state: job.state,
          repository: record.repositoryName,
          repositoryId: record.repositoryId,
          createdAt: job.createdAt,
          startedAt: job.startedAt,
          completedAt: job.completedAt,
          nextAttemptAt: job.nextAttemptAt,
          attempts: isHistoricalJob ? undefined : job.attempts,
          maxAttempts: isHistoricalJob ? undefined : job.maxAttempts,
          progressCurrent: job.progressCurrent,
          progressTotal: job.progressTotal,
          progressMessage: isHistoricalJob
            ? text("历史任务", "Historical job")
            : job.progressMessage,
          lastError: job.lastError,
          details: job.details,
        };
      });
      const auditRows: OperationRow[] = (auditRetention.data ?? []).map(
        (job) => ({
          id: job.id,
          kind: "audit-retention",
          state: job.state,
          createdAt: job.createdAt,
          startedAt: job.startedAt,
          completedAt: job.completedAt,
          lastError: job.lastError,
        }),
      );
      setRows(
        [...lifecycle, ...auditRows].sort((a, b) =>
          b.createdAt.localeCompare(a.createdAt),
        ),
      );
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, [text]);

  const hasActiveJobs = useMemo(
    () =>
      rows?.some((row) =>
        ["pending", "running", "retrying"].includes(row.state),
      ) ?? false,
    [rows],
  );

  useEffect(() => {
    if (activeTab !== "jobs") return;
    const timer = window.setInterval(() => {
      if (hasActiveJobs) void load();
    }, 15_000);
    return () => window.clearInterval(timer);
  }, [activeTab, hasActiveJobs, load]);

  const controlJob = async (row: OperationRow, action: JobAction) => {
    if (!row.repositoryId) return;
    setActingJob(row.id);
    setError(null);
    try {
      const options = {
        path: { repositoryId: row.repositoryId, lifecycleJobId: row.id },
      };
      const result =
        action === "run"
          ? await runRepositoryLifecycleJobNow(options)
          : action === "retry"
            ? await retryRepositoryLifecycleJob(options)
            : await cancelRepositoryLifecycleJob(options);
      if (result.error) {
        setError(result.error);
        return;
      }
      await load();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setActingJob(null);
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
      <PageHeader
        title={text("任务中心", "Operations")}
        description={text(
          "配置周期性维护计划，并跨仓库查看后台任务的执行状态。",
          "Schedule recurring maintenance and track background jobs across repositories.",
        )}
      />
      <Tabs
        activeKey={activeTab}
        onChange={(key) => {
          setActiveTab(key);
          if (key === "jobs" && rows === null && !loading) void load();
        }}
        items={[
          {
            key: "schedules",
            label: text("计划任务", "Scheduled tasks"),
            children: <ScheduledTasksPanel />,
          },
          {
            key: "webhooks",
            label: text("Webhook 投递", "Webhook delivery"),
            children: <WebhookDeliveriesPanel />,
          },
          {
            key: "jobs",
            label: text("执行记录", "Job history"),
            children: (
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
                      <h2 className="text-sm font-semibold tracking-tight text-zinc-100">
                        {text("执行记录", "Job history")}
                      </h2>
                      <p className="mt-1 text-xs text-zinc-500">
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
                      <Loading
                        label={text("加载任务状态…", "Loading job status…")}
                      />
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
                          onAction={(target, action) =>
                            void controlJob(target, action)
                          }
                        />
                      ))}
                    </div>
                  ) : (
                    <Table<OperationRow>
                      className="ag-console-table ag-operation-desktop-table"
                      rowKey={operationRowKey}
                      size="middle"
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
            ),
          },
          {
            key: "diagnostics",
            label: text("系统诊断", "System diagnostics"),
            children: (
              <div className="ag-page-stack ag-diagnostics-tab">
                <SystemDiagnosticsPanel />
                <RuntimeNodesPanel />
              </div>
            ),
          },
        ]}
      />
    </div>
  );
}
