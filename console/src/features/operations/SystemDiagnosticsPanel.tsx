import { useMemo } from "react";
import { CopyOutlined, ReloadOutlined } from "@ant-design/icons";
import { Alert, Button, Space, Tooltip } from "antd";
import { formatDate } from "../../lib/format";
import { getConsoleBuildVersion } from "../../lib/buildVersion";
import { usePreferences } from "../../lib/preferences";
import {
  MetricStrip,
  useClipboardAction,
} from "../../components/ui/ConsolePrimitives";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { LocalCapacityPanel } from "./LocalCapacityPanel";
import { HTTPErrorRatePanel } from "./HTTPErrorRatePanel";
import {
  DiagnosticsAttentionCard,
  DiagnosticsIdentityCard,
} from "./DiagnosticsCards";
import {
  DiagnosticsDependenciesCard,
  DiagnosticsQueueCard,
} from "./DiagnosticsDetailCards";
import { scannerNeedsAttentionFor } from "./diagnosticsScanner";
import { isAuthorizationFailure, useDiagnostics } from "./systemQueries";

/** The Gateway's diagnostic snapshot: health summary, then detail cards. */
export function SystemDiagnosticsPanel() {
  const { locale, text } = usePreferences();
  const consoleVersion = getConsoleBuildVersion();
  const diagnosticsQuery = useDiagnostics();
  const error = diagnosticsQuery.error;
  // An authorization refusal must not leave a previous principal's snapshot
  // on screen; any other refresh failure keeps it under a warning.
  const diagnostics = isAuthorizationFailure(error)
    ? null
    : (diagnosticsQuery.data ?? null);
  const loading = diagnosticsQuery.isFetching;
  const load = () => void diagnosticsQuery.refetch();
  const { copiedValue, copy } = useClipboardAction();
  const copied = copiedValue === "diagnostics";

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
    diagnostics !== null && scannerNeedsAttentionFor(diagnostics);

  if (error && !diagnostics)
    return <ErrorBanner error={error} onRetry={load} />;
  if (!diagnostics)
    return <Loading label={text("加载系统诊断…", "Loading diagnostics…")} />;

  const scanner = diagnostics.scanner;
  const scannerCritical =
    scanner !== undefined &&
    ["unhealthy", "unreachable"].includes(scanner.status);
  return (
    <div className="ag-page-stack ag-system-diagnostics">
      {error != null && (
        <ErrorBanner
          error={error}
          onRetry={load}
          tone="warning"
          title={text(
            "刷新失败，仍显示旧诊断快照",
            "Refresh failed; showing the previous diagnostic snapshot",
          )}
        />
      )}
      <Card className="ag-diagnostics-snapshot">
        <div className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold tracking-tight text-fg-strong">
              {text("诊断快照", "Diagnostic snapshot")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
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
                onClick={() =>
                  void copy(JSON.stringify(diagnostics, null, 2), "diagnostics")
                }
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

      <DiagnosticsAttentionCard diagnostics={diagnostics} />

      <LocalCapacityPanel capacity={diagnostics.localCapacity} />
      <HTTPErrorRatePanel rate={diagnostics.httpErrorRate} />
      <div className="ag-diagnostics-detail-grid">
        <DiagnosticsIdentityCard
          diagnostics={diagnostics}
          consoleVersion={consoleVersion}
        />
        <DiagnosticsDependenciesCard diagnostics={diagnostics} />
        <DiagnosticsQueueCard diagnostics={diagnostics} />
      </div>
    </div>
  );
}
