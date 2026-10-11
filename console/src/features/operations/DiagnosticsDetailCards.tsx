import { CheckCircleOutlined, WarningOutlined } from "@ant-design/icons";
import { Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import type {
  DiagnosticDependency,
  DiagnosticQueueStat,
  Diagnostics,
} from "../../client";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { FormatBadge, StateBadge } from "../../components/ui/Badge";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { EmptyState } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import {
  scannerOperationalDetail,
  scannerStatusLabels,
  scannerStatusTone,
} from "./diagnosticsScanner";

const dependencyLabels: Record<string, [string, string]> = {
  postgresql: ["PostgreSQL", "PostgreSQL"],
  "object-storage": ["对象存储", "Object storage"],
  runtime: ["运行依赖", "Runtime dependencies"],
};

export function DiagnosticsDependenciesCard({
  diagnostics,
}: {
  diagnostics: Diagnostics;
}) {
  const { locale, text } = usePreferences();
  const scanner = diagnostics.scanner;
  const scannerStatusLabel = scanner
    ? scannerStatusLabels[scanner.status]
    : null;
  const scannerDetail = scanner
    ? scannerOperationalDetail(scanner, text, locale)
    : "";
  return (
    <Card className="ag-diagnostics-dependencies-card">
      <CardHeader title={text("依赖与扫描器", "Dependencies and scanner")} />
      <div className="divide-y divide-line">
        {diagnostics.dependencies.map((dependency: DiagnosticDependency) => {
          const labels = dependencyLabels[dependency.name];
          const reachable = dependency.status === "reachable";
          return (
            <div key={dependency.name} className="ag-diagnostic-status-row">
              <div className="min-w-0">
                <div className="text-sm text-fg">
                  {labels ? text(labels[0], labels[1]) : dependency.name}
                </div>
                <div className="mt-1 break-words text-xs leading-5 text-fg-tertiary">
                  {dependency.detail}
                </div>
              </div>
              <span
                className={`shrink-0 text-xs ${
                  reachable
                    ? "text-[var(--ag-status-success)]"
                    : "text-[var(--ag-status-warning)]"
                }`}
              >
                {reachable ? <CheckCircleOutlined /> : <WarningOutlined />}
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
        })}
        {diagnostics.dependencies.length === 0 && (
          <div className="px-5 py-5 text-sm text-fg-tertiary">
            {text("没有依赖检查结果", "No dependency checks reported")}
          </div>
        )}
        {scanner && scannerStatusLabel && (
          <div className="ag-diagnostic-status-row">
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-baseline gap-2 text-sm text-fg">
                <span>
                  {text("制品扫描器", "Artifact scanner")} · {scanner.name}
                </span>
                {scanner.version && (
                  <span className="font-mono text-xs text-fg-tertiary">
                    {scanner.version}
                  </span>
                )}
              </div>
              <div className="mt-1 break-words text-xs leading-5 text-fg-tertiary">
                {scannerDetail}
              </div>
              {scanner.formats.length > 0 && (
                <Tooltip title={scanner.formats.join(" · ")}>
                  <div className="mt-1 break-words font-mono text-xs leading-5 text-fg-disabled">
                    {text("覆盖格式", "Formats")}: {scanner.formats.join(" · ")}
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
  );
}

export function DiagnosticsQueueCard({
  diagnostics,
}: {
  diagnostics: Diagnostics;
}) {
  const { locale, text } = usePreferences();
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
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {value ? formatDate(value, locale) : "—"}
        </span>
      ),
    },
  ];

  return (
    <Card className="ag-diagnostics-queue-card">
      <CardHeader title={text("后台队列", "Background queues")} />
      <ConsoleTable<DiagnosticQueueStat>
        rowKey={(queue) => `${queue.kind}-${queue.format}-${queue.state}`}
        dataSource={diagnostics.queues}
        columns={queueColumns}
        locale={{
          emptyText: (
            <EmptyState
              compact
              title={text("当前没有活跃队列", "No active queue entries")}
              hint={text(
                "后台任务启动后会占用队列，这里会实时显示。",
                "Background jobs appear here as soon as they occupy a queue.",
              )}
            />
          ),
        }}
        pagination={false}
        scroll={diagnostics.queues.length > 8 ? { x: 500, y: 320 } : { x: 500 }}
      />
    </Card>
  );
}
