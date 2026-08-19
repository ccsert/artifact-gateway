import { useCallback, useEffect, useMemo, useState } from "react";
import {
  CheckCircleOutlined,
  CopyOutlined,
  ReloadOutlined,
  WarningOutlined,
} from "@ant-design/icons";
import { Alert, Button, Descriptions, Space, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { getDiagnostics } from "../client";
import type {
  DiagnosticDependency,
  DiagnosticQueueStat,
  DiagnosticScanner,
  Diagnostics,
} from "../client";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { FormatBadge, StateBadge } from "./Badge";
import { MetricStrip } from "./ConsolePrimitives";
import { ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader } from "./Layout";

const dependencyLabels: Record<string, [string, string]> = {
  postgresql: ["PostgreSQL", "PostgreSQL"],
  "object-storage": ["对象存储", "Object storage"],
  runtime: ["运行依赖", "Runtime dependencies"],
};

const scannerStatusLabels: Record<
  DiagnosticScanner["status"],
  [string, string]
> = {
  healthy: ["健康", "Healthy"],
  degraded: ["需关注", "Degraded"],
  unhealthy: ["异常", "Unhealthy"],
  unreachable: ["不可达", "Unreachable"],
  unknown: ["未知", "Unknown"],
  not_configured: ["未配置", "Not configured"],
};

const scannerStatusTone: Record<DiagnosticScanner["status"], string> = {
  healthy: "text-emerald-400",
  degraded: "text-amber-400",
  unhealthy: "text-rose-400",
  unreachable: "text-rose-400",
  unknown: "text-amber-400",
  not_configured: "text-zinc-500",
};

type Localize = (chinese: string, english: string) => string;

function scannerOperationalDetail(
  scanner: DiagnosticScanner,
  text: Localize,
  locale: string,
): string {
  if (scanner.status === "not_configured") {
    return text("未启用外部扫描器", "External scanner is not configured");
  }
  if (scanner.status === "unknown") {
    return text(
      "健康探针未配置或不可用 · 漏洞库新鲜度未知",
      "Health reporting unavailable · Database freshness unknown",
    );
  }
  const databaseDetail =
    scanner.databaseFreshness === "unknown"
      ? text(
          "漏洞库未报告更新时间",
          "Vulnerability database update time unavailable",
        )
      : text(
          `漏洞库${scanner.databaseFreshness === "fresh" ? "新鲜" : "已过期"} · ${formatDate(scanner.databaseUpdatedAt, locale)}`,
          `Vulnerability database ${scanner.databaseFreshness} · ${formatDate(scanner.databaseUpdatedAt, locale)}`,
        );
  if (scanner.status === "unreachable") {
    return text(
      `健康检查失败 · ${databaseDetail}`,
      `Health check failed · ${databaseDetail}`,
    );
  }
  if (scanner.status === "unhealthy") {
    return text(
      `扫描器报告异常 · ${databaseDetail}`,
      `Scanner reports unhealthy · ${databaseDetail}`,
    );
  }
  if (scanner.status === "degraded" && scanner.databaseFreshness !== "stale") {
    return text(
      `扫描器报告降级 · ${databaseDetail}`,
      `Scanner reports degraded · ${databaseDetail}`,
    );
  }
  return databaseDetail;
}

export function SystemDiagnosticsPanel() {
  const { locale, text } = usePreferences();
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await getDiagnostics();
      if (result.error) throw result.error;
      setDiagnostics(result.data ?? null);
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const queueTotal = useMemo(
    () =>
      (diagnostics?.queues ?? []).reduce(
        (total, queue) => total + queue.count,
        0,
      ),
    [diagnostics],
  );
  const failedQueues = (diagnostics?.queues ?? []).reduce(
    (total, queue) => total + (queue.state === "failed" ? queue.count : 0),
    0,
  );
  const unavailable = (diagnostics?.dependencies ?? []).filter(
    (dependency) => dependency.status !== "reachable",
  ).length;
  const scannerNeedsAttention =
    diagnostics?.scanner !== undefined &&
    diagnostics.scanner.status !== "healthy" &&
    diagnostics.scanner.status !== "not_configured";

  const queueColumns: ColumnsType<DiagnosticQueueStat> = [
    {
      title: text("任务类型", "Job type"),
      dataIndex: "kind",
      key: "kind",
      width: 150,
      render: (kind: string) => (
        <span className="font-mono text-xs">{kind}</span>
      ),
    },
    {
      title: text("格式", "Format"),
      dataIndex: "format",
      key: "format",
      width: 110,
      responsive: ["sm"],
      render: (format: string) => <FormatBadge format={format} />,
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (state: string) => <StateBadge state={state} />,
    },
    {
      title: text("数量", "Count"),
      dataIndex: "count",
      key: "count",
      width: 80,
      align: "right",
    },
    {
      title: text("最早入队", "Oldest queued"),
      dataIndex: "oldestCreatedAt",
      key: "oldestCreatedAt",
      width: 190,
      responsive: ["lg"],
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {value ? formatDate(value, locale) : "—"}
        </span>
      ),
    },
  ];

  if (error) return <ErrorBanner error={error} onRetry={load} />;
  if (!diagnostics)
    return <Loading label={text("加载系统诊断…", "Loading diagnostics…")} />;

  const scanner = diagnostics.scanner;
  const scannerCritical =
    scanner !== undefined &&
    ["unhealthy", "unreachable"].includes(scanner.status);
  const scannerStatusLabel = scanner
    ? scannerStatusLabels[scanner.status]
    : null;
  const scannerDetail = scanner
    ? scannerOperationalDetail(scanner, text, locale)
    : "";
  const unavailableDependencies = diagnostics.dependencies.filter(
    (dependency) => dependency.status !== "reachable",
  );
  const failedQueueEntries = diagnostics.queues.filter(
    (queue) => queue.state === "failed",
  );
  const nodeIssues = diagnostics.nodes.issues ?? [];
  const nodeStatusNeedsAttention = diagnostics.nodes.status !== "healthy";
  const needsAttention =
    unavailableDependencies.length > 0 ||
    failedQueueEntries.length > 0 ||
    scannerNeedsAttention ||
    nodeStatusNeedsAttention;
  const attentionCount =
    unavailableDependencies.length +
    failedQueueEntries.length +
    nodeIssues.length +
    (scannerNeedsAttention ? 1 : 0) +
    (nodeStatusNeedsAttention && nodeIssues.length === 0 ? 1 : 0);

  return (
    <div className="ag-page-stack ag-system-diagnostics">
      <Card className="ag-diagnostics-snapshot">
        <div className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold tracking-tight text-zinc-100">
              {text("诊断快照", "Diagnostic snapshot")}
            </h2>
            <p className="mt-1 text-xs text-zinc-500">
              {text("生成于", "Generated")}{" "}
              {formatDate(diagnostics.generatedAt, locale)}
            </p>
          </div>
          <Space size="small" wrap>
            <Tooltip
              title={text(
                "复制脱敏诊断 JSON",
                "Copy sanitized diagnostics JSON",
              )}
            >
              <Button
                aria-label={text(
                  "复制脱敏诊断 JSON",
                  "Copy sanitized diagnostics JSON",
                )}
                icon={<CopyOutlined />}
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(
                      JSON.stringify(diagnostics, null, 2),
                    );
                    setCopied(true);
                    window.setTimeout(() => setCopied(false), 1500);
                  } catch {
                    // Clipboard access can be unavailable in an insecure local context.
                  }
                }}
              >
                {copied ? text("已复制", "Copied") : text("复制", "Copy")}
              </Button>
            </Tooltip>
            <Button
              aria-label={text("刷新诊断", "Refresh diagnostics")}
              icon={<ReloadOutlined />}
              loading={loading}
              onClick={() => void load()}
            >
              {text("刷新", "Refresh")}
            </Button>
          </Space>
        </div>
      </Card>
      {(unavailable > 0 ||
        diagnostics.nodes.status !== "healthy" ||
        failedQueues > 0 ||
        scannerNeedsAttention) && (
        <Alert
          type={
            unavailable > 0 ||
            diagnostics.nodes.status === "critical" ||
            failedQueues > 0 ||
            scannerCritical
              ? "error"
              : "warning"
          }
          showIcon
          title={text("系统运行状态需要关注", "System health needs attention")}
          description={text(
            "先处理下列依赖、扫描器、节点能力或失败队列问题，再执行维护操作。",
            "Resolve the dependency, scanner, node capability, or failed queue issues below before maintenance.",
          )}
        />
      )}
      <MetricStrip
        items={[
          {
            label: text("依赖可用", "Dependencies ready"),
            value: diagnostics.dependencies.length - unavailable,
            hint: text(
              `${diagnostics.dependencies.length} 项检查`,
              `${diagnostics.dependencies.length} checks`,
            ),
            tone: unavailable ? "danger" : "success",
          },
          {
            label: text("在线节点", "Online nodes"),
            value: diagnostics.nodes.online,
            hint: text(
              `${diagnostics.nodes.stale} 陈旧 · ${diagnostics.nodes.offline} 离线`,
              `${diagnostics.nodes.stale} stale · ${diagnostics.nodes.offline} offline`,
            ),
            tone:
              diagnostics.nodes.status === "healthy" ? "success" : "warning",
          },
          {
            label: text("活跃队列", "Queued work"),
            value: queueTotal,
            hint: text(
              "待处理、运行、重试和失败",
              "Pending, running, retrying, and failed",
            ),
            tone: queueTotal ? "warning" : "default",
          },
          {
            label: text("失败任务", "Failed work"),
            value: failedQueues,
            hint: failedQueues
              ? text("需要人工检查", "Needs review")
              : text("当前无失败", "No failures"),
            tone: failedQueues ? "danger" : "success",
          },
        ]}
      />

      {needsAttention && (
        <Card>
          <CardHeader
            title={text("需要处理", "Needs attention")}
            extra={
              <span className="text-xs text-zinc-500">
                {text(`${attentionCount} 项`, `${attentionCount} items`)}
              </span>
            }
          />
          <div className="divide-y divide-zinc-800/60">
            {unavailableDependencies.map((dependency) => (
              <div key={dependency.name} className="ag-diagnostic-issue-row">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-zinc-200">
                    {text("依赖不可用", "Dependency unavailable")} ·{" "}
                    {dependency.name}
                  </div>
                  <div className="mt-1 break-words text-xs leading-5 text-zinc-500">
                    {dependency.detail}
                  </div>
                </div>
                <StateBadge state={dependency.status} />
              </div>
            ))}
            {scannerNeedsAttention && scanner && scannerStatusLabel && (
              <div className="ag-diagnostic-issue-row">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-zinc-200">
                    {text("扫描器可信度", "Scanner trust")} · {scanner.name}
                  </div>
                  <div className="mt-1 break-words text-xs leading-5 text-zinc-500">
                    {scannerDetail}
                  </div>
                </div>
                <span
                  className={`shrink-0 text-xs ${scannerStatusTone[scanner.status]}`}
                >
                  {text(scannerStatusLabel[0], scannerStatusLabel[1])}
                </span>
              </div>
            )}
            {nodeIssues.map((issue) => (
              <div key={issue.code} className="ag-diagnostic-issue-row">
                <div className="min-w-0">
                  <div className="font-mono text-xs text-amber-300">
                    {issue.code}
                  </div>
                  <div className="mt-1 break-words text-xs leading-5 text-zinc-400">
                    {issue.message}
                  </div>
                </div>
                <StateBadge state={diagnostics.nodes.status} />
              </div>
            ))}
            {nodeStatusNeedsAttention && nodeIssues.length === 0 && (
              <div className="ag-diagnostic-issue-row">
                <div className="min-w-0">
                  <div className="text-sm font-medium text-zinc-200">
                    {text("运行节点状态异常", "Runtime node health degraded")}
                  </div>
                  <div className="mt-1 text-xs leading-5 text-zinc-500">
                    {text(
                      `${diagnostics.nodes.stale} 个陈旧，${diagnostics.nodes.offline} 个离线`,
                      `${diagnostics.nodes.stale} stale and ${diagnostics.nodes.offline} offline`,
                    )}
                  </div>
                </div>
                <StateBadge state={diagnostics.nodes.status} />
              </div>
            )}
            {failedQueueEntries.map((queue) => (
              <div
                key={`${queue.kind}-${queue.format}-${queue.state}`}
                className="ag-diagnostic-issue-row"
              >
                <div className="min-w-0">
                  <div className="text-sm font-medium text-zinc-200">
                    {text("失败队列", "Failed queue")} · {queue.kind}
                  </div>
                  <div className="mt-1 text-xs leading-5 text-zinc-500">
                    {queue.format} ·{" "}
                    {text(`${queue.count} 个任务`, `${queue.count} jobs`)}
                  </div>
                </div>
                <StateBadge state={queue.state} />
              </div>
            ))}
          </div>
        </Card>
      )}

      <div className="ag-diagnostics-detail-grid">
        <Card className="ag-diagnostics-identity-card">
          <CardHeader
            title={text("构建与运行身份", "Build and runtime identity")}
          />
          <Descriptions
            className="px-5 py-4"
            size="small"
            column={{ xs: 1, sm: 1, md: 2, lg: 2, xl: 1, xxl: 2 }}
            items={[
              {
                key: "version",
                label: text("版本", "Version"),
                children: diagnostics.build.version,
              },
              {
                key: "revision",
                label: text("修订", "Revision"),
                children: (
                  <span className="font-mono text-xs">
                    {diagnostics.build.revision}
                  </span>
                ),
              },
              { key: "go", label: "Go", children: diagnostics.build.goVersion },
              {
                key: "instance",
                label: text("实例", "Instance"),
                children: (
                  <span className="font-mono text-xs">
                    {diagnostics.runtime.instanceId || "—"}
                  </span>
                ),
              },
              {
                key: "roles",
                label: text("角色", "Roles"),
                children: diagnostics.runtime.roles.join(" · ") || "—",
              },
              {
                key: "worker-formats",
                label: text("Worker 格式", "Worker formats"),
                children: diagnostics.runtime.workerFormats.join(" · ") || "—",
              },
              {
                key: "worker-kinds",
                label: text("Worker 任务", "Worker jobs"),
                children: diagnostics.runtime.workerKinds.join(" · ") || "—",
              },
              {
                key: "modified",
                label: text("工作区", "Workspace"),
                children: diagnostics.build.modified
                  ? text("有未提交修改", "Modified")
                  : text("干净", "Clean"),
              },
            ]}
          />
        </Card>
        <Card className="ag-diagnostics-dependencies-card">
          <CardHeader
            title={text("依赖与扫描器", "Dependencies and scanner")}
          />
          <div className="divide-y divide-zinc-800/60">
            {diagnostics.dependencies.map(
              (dependency: DiagnosticDependency) => {
                const labels = dependencyLabels[dependency.name];
                const reachable = dependency.status === "reachable";
                return (
                  <div
                    key={dependency.name}
                    className="ag-diagnostic-status-row"
                  >
                    <div className="min-w-0">
                      <div className="text-sm text-zinc-200">
                        {labels ? text(labels[0], labels[1]) : dependency.name}
                      </div>
                      <div className="mt-1 break-words text-xs leading-5 text-zinc-500">
                        {dependency.detail}
                      </div>
                    </div>
                    <span
                      className={`shrink-0 text-xs ${
                        reachable ? "text-emerald-400" : "text-amber-400"
                      }`}
                    >
                      {reachable ? (
                        <CheckCircleOutlined />
                      ) : (
                        <WarningOutlined />
                      )}
                      <span className="ml-2">
                        {reachable
                          ? text("可用", "Reachable")
                          : dependency.status === "not_configured"
                            ? text("未配置", "Not configured")
                            : text("不可用", "Unreachable")}
                      </span>
                    </span>
                  </div>
                );
              },
            )}
            {diagnostics.dependencies.length === 0 && (
              <div className="px-5 py-5 text-sm text-zinc-500">
                {text("没有依赖检查结果", "No dependency checks reported")}
              </div>
            )}
            {scanner && scannerStatusLabel && (
              <div className="ag-diagnostic-status-row">
                <div className="min-w-0">
                  <div className="flex min-w-0 flex-wrap items-baseline gap-2 text-sm text-zinc-200">
                    <span>
                      {text("制品扫描器", "Artifact scanner")} · {scanner.name}
                    </span>
                    {scanner.version && (
                      <span className="font-mono text-[11px] text-zinc-500">
                        {scanner.version}
                      </span>
                    )}
                  </div>
                  <div className="mt-1 break-words text-xs leading-5 text-zinc-500">
                    {scannerDetail}
                  </div>
                  {scanner.formats.length > 0 && (
                    <Tooltip title={scanner.formats.join(" · ")}>
                      <div className="mt-1 break-words font-mono text-[11px] leading-5 text-zinc-600">
                        {text("覆盖格式", "Formats")}:{" "}
                        {scanner.formats.join(" · ")}
                      </div>
                    </Tooltip>
                  )}
                </div>
                <span
                  className={`shrink-0 text-xs ${scannerStatusTone[scanner.status]}`}
                >
                  {scanner.status === "healthy" ? (
                    <CheckCircleOutlined />
                  ) : (
                    <WarningOutlined />
                  )}
                  <span className="ml-2">
                    {text(scannerStatusLabel[0], scannerStatusLabel[1])}
                  </span>
                </span>
              </div>
            )}
          </div>
        </Card>
      </div>

      <Card>
        <CardHeader title={text("后台队列", "Background queues")} />
        <Table<DiagnosticQueueStat>
          className="ag-console-table"
          rowKey={(queue) => `${queue.kind}-${queue.format}-${queue.state}`}
          size="small"
          dataSource={diagnostics.queues}
          columns={queueColumns}
          locale={{
            emptyText: text("当前没有活跃队列", "No active queue entries"),
          }}
          pagination={false}
          scroll={
            diagnostics.queues.length > 8 ? { x: 500, y: 320 } : { x: 500 }
          }
        />
      </Card>
    </div>
  );
}
