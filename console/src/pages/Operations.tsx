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
  Tooltip,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  cancelRepositoryLifecycleJob,
  listAuditRetentionJobs,
  listLifecycleJobs,
  listRuntimeNodes,
  retryRepositoryLifecycleJob,
  runRepositoryLifecycleJobNow,
} from "../client";
import type { LifecycleJob, RuntimeNode } from "../client";
import { PageHeader, Card, CardHeader } from "../components/Layout";
import { EmptyState, ErrorBanner, Loading } from "../components/Feedback";
import { FormatBadge, StateBadge } from "../components/Badge";
import { formatDate } from "../lib/format";
import {
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";

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

const KIND_LABELS: Record<string, string> = {
  retention: "制品保留",
  promotion: "制品晋级",
  replication: "仓库复制",
  reclaim: "对象回收",
  "audit-retention": "审计保留",
};

function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

export function OperationsPage() {
  const [repositories, setRepositories] = useState<
    Array<{ id: string; name: string }>
  >([]);
  const [rows, setRows] = useState<OperationRow[] | null>(null);
  const [nodes, setNodes] = useState<RuntimeNode[] | null>(null);
  const [nodeError, setNodeError] = useState<unknown>(null);
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
          progressMessage: isHistoricalJob ? "历史任务" : job.progressMessage,
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
      const runtimeResult = await listRuntimeNodes();
      if (runtimeResult.error) {
        setNodeError(runtimeResult.error);
      } else {
        setNodeError(null);
        setNodes(runtimeResult.data?.items ?? []);
      }
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (
        rows?.some((row) =>
          ["pending", "running", "retrying"].includes(row.state),
        )
      )
        void load();
    }, 15_000);
    return () => window.clearInterval(timer);
  }, [load, rows]);

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

  const nodeColumns: ColumnsType<RuntimeNode> = [
    {
      title: "实例",
      dataIndex: "instanceId",
      key: "instanceId",
      width: 190,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-200">{value}</span>
      ),
    },
    {
      title: "状态",
      dataIndex: "status",
      key: "status",
      width: 100,
      render: (value: RuntimeNode["status"]) => <StateBadge state={value} />,
    },
    {
      title: "角色",
      dataIndex: "roles",
      key: "roles",
      width: 150,
      render: (roles: string[]) => (
        <span className="text-xs text-zinc-400">{roles.join(" · ")}</span>
      ),
    },
    {
      title: "Worker 能力",
      key: "capabilities",
      width: 330,
      render: (_, node) => (
        <div className="flex flex-wrap items-center gap-1.5">
          {node.workerFormats.length > 0 ? (
            node.workerFormats.map((format) => (
              <FormatBadge key={format} format={format} />
            ))
          ) : (
            <span className="text-xs text-zinc-600">无格式 Worker</span>
          )}
          {node.workerKinds.length > 0 && (
            <span className="ml-1 text-[11px] text-zinc-500">
              {node.workerKinds.join(" · ")}
            </span>
          )}
        </div>
      ),
    },
    {
      title: "最近心跳",
      dataIndex: "lastSeenAt",
      key: "lastSeenAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
  ];

  const columns: ColumnsType<OperationRow> = [
    {
      title: "类型",
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
      title: "仓库",
      dataIndex: "repository",
      key: "repository",
      width: 150,
      render: (value: string | undefined) => (
        <span className="text-xs text-zinc-400">{value ?? "全局"}</span>
      ),
    },
    {
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (value: string) => <StateBadge state={value} />,
    },
    {
      title: "进度",
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
            {row.progressMessage ?? "未报告"}
          </span>
        ),
    },
    {
      title: "尝试",
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
      title: "创建时间",
      dataIndex: "createdAt",
      key: "createdAt",
      width: 170,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
    {
      title: "下次执行 / 完成时间",
      key: "schedule",
      width: 200,
      render: (_, row) => (
        <div className="whitespace-nowrap text-xs text-zinc-500">
          <div>
            {row.nextAttemptAt
              ? formatDate(row.nextAttemptAt)
              : formatDate(row.completedAt)}
          </div>
          {row.startedAt && (
            <div className="mt-1 text-[11px] text-zinc-600">
              开始 {formatDate(row.startedAt)}
            </div>
          )}
        </div>
      ),
    },
    {
      title: "任务 ID / 失败原因",
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
      title: "操作",
      key: "actions",
      fixed: "right",
      width: 120,
      render: (_, row) =>
        row.repositoryId ? (
          <Space size="small">
            {(row.state === "pending" || row.state === "retrying") && (
              <Tooltip title="立即加入执行队列">
                <Button
                  aria-label="立即加入执行队列"
                  size="small"
                  icon={<PlayCircleOutlined />}
                  loading={actingJob === row.id}
                  onClick={() => void controlJob(row, "run")}
                />
              </Tooltip>
            )}
            {(row.state === "failed" || row.state === "cancelled") && (
              <Tooltip title="重新执行任务">
                <Button
                  aria-label="重新执行任务"
                  size="small"
                  icon={<RedoOutlined />}
                  loading={actingJob === row.id}
                  onClick={() => void controlJob(row, "retry")}
                />
              </Tooltip>
            )}
            {(row.state === "pending" || row.state === "retrying") && (
              <Popconfirm
                title="取消此任务？"
                description="取消后仍可从任务中心重新执行。"
                okText="取消任务"
                cancelText="返回"
                onConfirm={() => controlJob(row, "cancel")}
              >
                <Tooltip title="取消任务">
                  <Button
                    aria-label="取消任务"
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
        title="任务中心"
        description="跨仓库查看晋级、复制、保留和对象回收任务的当前状态。"
        actions={
          <Button
            icon={<ReloadOutlined />}
            onClick={() => void load()}
            loading={loading}
          >
            刷新
          </Button>
        }
      />
      <MetricStrip
        items={[
          {
            label: "任务总数",
            value: rows?.length ?? "—",
            hint: "当前保留窗口",
          },
          {
            label: "运行中",
            value: running,
            hint: "正在处理",
            tone: running ? "success" : "default",
          },
          {
            label: "待处理",
            value: pending,
            hint: "等待 worker",
            tone: pending ? "warning" : "default",
          },
          {
            label: "失败",
            value: failed,
            hint: failed ? "需要检查失败原因" : "当前没有失败任务",
            tone: failed ? "danger" : "success",
          },
        ]}
      />
      <Card className="mt-4">
        <CardHeader
          title="运行节点"
          extra={
            <span className="text-xs text-zinc-500">
              {nodes ? `${nodes.length} 个实例` : "加载中"}
            </span>
          }
        />
        {nodeError ? (
          <ErrorBanner error={nodeError} onRetry={load} />
        ) : nodes === null ? (
          <div className="px-5 py-6">
            <Loading label="加载节点清单…" />
          </div>
        ) : nodes.length === 0 ? (
          <div className="px-5 py-6">
            <EmptyState
              title="暂未收到节点心跳"
              hint="节点启动后会自动出现在这里。"
            />
          </div>
        ) : (
          <Table<RuntimeNode>
            className="ag-console-table"
            rowKey={(node) => node.instanceId}
            size="small"
            dataSource={nodes}
            columns={nodeColumns}
            pagination={false}
            scroll={{ x: 960, y: 260 }}
          />
        )}
      </Card>
      <FilterBar
        className="mt-4 mb-4"
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
              清除筛选
            </Button>
          ) : undefined
        }
      >
        <FilterField label="状态" className="min-w-[160px]">
          <Select
            className="w-full"
            value={stateFilter}
            onChange={setStateFilter}
            options={[
              { value: "all", label: "全部状态" },
              { value: "pending", label: "pending" },
              { value: "running", label: "running" },
              { value: "retrying", label: "retrying" },
              { value: "completed", label: "completed" },
              { value: "failed", label: "failed" },
              { value: "cancelled", label: "cancelled" },
            ]}
          />
        </FilterField>
        <FilterField label="任务类型" className="min-w-[180px]">
          <Select
            className="w-full"
            value={kindFilter}
            onChange={setKindFilter}
            options={[
              { value: "all", label: "全部类型" },
              ...kindOptions.map((kind) => ({
                value: kind,
                label: kindLabel(kind),
              })),
            ]}
          />
        </FilterField>
        <FilterField label="仓库" className="min-w-[220px]">
          <Select
            className="w-full"
            value={repositoryFilter}
            onChange={setRepositoryFilter}
            showSearch={{ optionFilterProp: "label" }}
            options={[
              { value: "all", label: "全部仓库" },
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
        <Loading label="加载任务状态…" />
      ) : visibleRows.length === 0 ? (
        <Card>
          <EmptyState
            title="没有匹配的任务"
            hint="调整筛选条件，或等待任务产生后再刷新。"
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
    </div>
  );
}
