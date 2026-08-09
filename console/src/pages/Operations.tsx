import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ClearOutlined,
  CloseOutlined,
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
};

const KIND_LABELS: Record<string, [string, string]> = {
  retention: ["制品保留", "Artifact retention"],
  promotion: ["制品晋级", "Artifact promotion"],
  replication: ["仓库复制", "Repository replication"],
  reclaim: ["对象回收", "Object reclamation"],
  "audit-retention": ["审计保留", "Audit retention"],
};

export function OperationsPage() {
  const { locale, text } = usePreferences();
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

  useEffect(() => {
    void load();
  }, [load]);

  const hasActiveJobs = useMemo(
    () =>
      rows?.some((row) =>
        ["pending", "running", "retrying"].includes(row.state),
      ) ?? false,
    [rows],
  );

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (hasActiveJobs) void load();
    }, 15_000);
    return () => window.clearInterval(timer);
  }, [hasActiveJobs, load]);

  const controlJob = async (
    row: OperationRow,
    action: "run" | "retry" | "cancel",
  ) => {
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

  const columns: ColumnsType<OperationRow> = [
    {
      title: text("类型", "Type"),
      key: "kind",
      width: 140,
      render: (_, row) => (
        <div className="flex items-center gap-2 text-sm text-zinc-200">
          <SyncOutlined className="text-zinc-500" />
          {kindLabel(row.kind)}
        </div>
      ),
    },
    {
      title: text("仓库", "Repository"),
      dataIndex: "repository",
      key: "repository",
      width: 150,
      render: (value: string | undefined) => (
        <span className="text-xs text-zinc-400">
          {value ?? text("全局", "Global")}
        </span>
      ),
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: text("进度", "Progress"),
      key: "progress",
      width: 220,
      render: (_, row) =>
        row.progressTotal !== undefined && row.progressTotal > 0 ? (
          <div>
            <Progress
              percent={Math.round(
                ((row.progressCurrent ?? 0) / row.progressTotal) * 100,
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
            {row.progressMessage && (
              <div
                className="mt-1 max-w-40 truncate text-[11px] text-zinc-500"
                title={row.progressMessage}
              >
                {row.progressMessage}
              </div>
            )}
          </div>
        ) : (
          <span className="text-xs text-zinc-600">
            {row.progressMessage ?? text("未报告", "Not reported")}
          </span>
        ),
    },
    {
      title: text("尝试", "Attempts"),
      key: "attempts",
      width: 100,
      render: (_, row) => (
        <span className="whitespace-nowrap text-xs text-zinc-400">
          {row.attempts === undefined
            ? "—"
            : `${row.attempts} / ${row.maxAttempts}`}
        </span>
      ),
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 170,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("下次执行 / 完成时间", "Next run / completed"),
      key: "schedule",
      width: 200,
      render: (_, row) => (
        <div className="whitespace-nowrap text-xs text-zinc-500">
          <div>
            {row.nextAttemptAt
              ? formatDate(row.nextAttemptAt, locale)
              : formatDate(row.completedAt, locale)}
          </div>
          {row.startedAt && (
            <div className="mt-1 text-[11px] text-zinc-600">
              {text("开始", "Started")} {formatDate(row.startedAt, locale)}
            </div>
          )}
        </div>
      ),
    },
    {
      title: text("任务 ID / 失败原因", "Job ID / failure reason"),
      key: "details",
      width: 280,
      render: (_, row) => (
        <div className="font-mono text-[11px] text-zinc-500">
          <div className="truncate" title={row.id}>
            {row.id}
          </div>
          {row.lastError && (
            <div className="mt-1 truncate text-rose-300" title={row.lastError}>
              {row.lastError}
            </div>
          )}
        </div>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 120,
      render: (_, row) =>
        row.repositoryId ? (
          <Space size="small">
            {(row.state === "pending" || row.state === "retrying") && (
              <Tooltip
                title={text(
                  "立即加入执行队列",
                  "Queue for immediate execution",
                )}
              >
                <Button
                  aria-label={text(
                    "立即加入执行队列",
                    "Queue for immediate execution",
                  )}
                  size="small"
                  icon={<PlayCircleOutlined />}
                  loading={actingJob === row.id}
                  onClick={() => void controlJob(row, "run")}
                />
              </Tooltip>
            )}
            {(row.state === "failed" || row.state === "cancelled") && (
              <Tooltip title={text("重新执行任务", "Retry job")}>
                <Button
                  aria-label={text("重新执行任务", "Retry job")}
                  size="small"
                  icon={<RedoOutlined />}
                  loading={actingJob === row.id}
                  onClick={() => void controlJob(row, "retry")}
                />
              </Tooltip>
            )}
            {(row.state === "pending" || row.state === "retrying") && (
              <Popconfirm
                title={text("取消此任务？", "Cancel this job?")}
                description={text(
                  "取消后仍可从任务中心重新执行。",
                  "You can retry it later from Operations.",
                )}
                okText={text("取消任务", "Cancel job")}
                cancelText={text("返回", "Back")}
                onConfirm={() => controlJob(row, "cancel")}
              >
                <Tooltip title={text("取消任务", "Cancel job")}>
                  <Button
                    aria-label={text("取消任务", "Cancel job")}
                    danger
                    size="small"
                    icon={<CloseOutlined />}
                    loading={actingJob === row.id}
                  />
                </Tooltip>
              </Popconfirm>
            )}
          </Space>
        ) : (
          <span className="text-xs text-zinc-600">—</span>
        ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={text("任务中心", "Operations")}
        description={text(
          "配置周期性维护计划，并跨仓库查看后台任务的执行状态。",
          "Schedule recurring maintenance and track background jobs across repositories.",
        )}
      />
      <Tabs
        defaultActiveKey="schedules"
        items={[
          {
            key: "schedules",
            label: text("计划任务", "Scheduled tasks"),
            children: <ScheduledTasksPanel />,
          },
          {
            key: "jobs",
            label: text("执行记录", "Job history"),
            children: (
              <>
                <MetricStrip
                  items={[
                    {
                      label: text("任务总数", "Jobs"),
                      value: rows?.length ?? "—",
                      hint: text("当前保留窗口", "Current retention window"),
                    },
                    {
                      label: text("运行中", "Running"),
                      value: running,
                      hint: text("正在处理", "In progress"),
                      tone: running ? "success" : "default",
                    },
                    {
                      label: text("待处理", "Pending"),
                      value: pending,
                      hint: text("等待 worker", "Waiting for a worker"),
                      tone: pending ? "warning" : "default",
                    },
                    {
                      label: text("失败", "Failed"),
                      value: failed,
                      hint: failed
                        ? text("需要检查失败原因", "Review failure details")
                        : text("当前没有失败任务", "No failed jobs"),
                      tone: failed ? "danger" : "success",
                    },
                  ]}
                />
                <RuntimeNodesPanel />
                <FilterBar
                  className="mt-4 mb-4"
                  actions={
                    <Space>
                      {(stateFilter !== "all" ||
                        kindFilter !== "all" ||
                        repositoryFilter !== "all") && (
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
                      )}
                      <Button
                        icon={<ReloadOutlined />}
                        onClick={() => void load()}
                        loading={loading}
                      >
                        {text("刷新", "Refresh")}
                      </Button>
                    </Space>
                  }
                >
                  <FilterField
                    label={text("状态", "Status")}
                    className="min-w-[160px]"
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
                    className="min-w-[180px]"
                  >
                    <Select
                      className="w-full"
                      value={kindFilter}
                      onChange={setKindFilter}
                      options={[
                        { value: "all", label: text("全部类型", "All types") },
                        ...kindOptions.map((kind) => ({
                          value: kind,
                          label: kindLabel(kind),
                        })),
                      ]}
                    />
                  </FilterField>
                  <FilterField
                    label={text("仓库", "Repository")}
                    className="min-w-[220px]"
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
                  <ErrorBanner error={error} onRetry={load} />
                ) : !rows ? (
                  <Loading
                    label={text("加载任务状态…", "Loading job status…")}
                  />
                ) : visibleRows.length === 0 ? (
                  <Card>
                    <EmptyState
                      title={text("没有匹配的任务", "No matching jobs")}
                      hint={text(
                        "调整筛选条件，或等待任务产生后再刷新。",
                        "Adjust the filters or refresh after new jobs are created.",
                      )}
                    />
                  </Card>
                ) : (
                  <Card>
                    <Table<OperationRow>
                      className="ag-console-table"
                      rowKey={(row) => `${row.kind}-${row.id}`}
                      size="middle"
                      dataSource={visibleRows}
                      columns={columns}
                      pagination={false}
                      virtual
                      scroll={{ x: 1520, y: 520 }}
                    />
                  </Card>
                )}
              </>
            ),
          },
          {
            key: "diagnostics",
            label: text("系统诊断", "System diagnostics"),
            children: <SystemDiagnosticsPanel />,
          },
        ]}
      />
    </div>
  );
}
