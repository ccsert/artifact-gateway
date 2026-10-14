import type { MessageKey } from "../../messages";
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

const dependencyLabels: Record<string, MessageKey> = {
  postgresql: "operations.postgresql",
  "object-storage": "operations.objectStorage",
  runtime: "operations.runtimeDependencies",
};

export function DiagnosticsDependenciesCard({
  diagnostics,
}: {
  diagnostics: Diagnostics;
}) {
  const { locale, t } = usePreferences();
  const scanner = diagnostics.scanner;
  const scannerStatusLabel = scanner
    ? scannerStatusLabels[scanner.status]
    : null;
  const scannerDetail = scanner
    ? scannerOperationalDetail(scanner, t, locale)
    : "";
  return (
    <Card className="ag-diagnostics-dependencies-card">
      <CardHeader title={t("operations.dependenciesAndScanner")} />
      <div className="divide-y divide-line">
        {diagnostics.dependencies.map((dependency: DiagnosticDependency) => {
          const labels = dependencyLabels[dependency.name];
          const reachable = dependency.status === "reachable";
          return (
            <div key={dependency.name} className="ag-diagnostic-status-row">
              <div className="min-w-0">
                <div className="text-sm text-fg">
                  {labels ? t(labels) : dependency.name}
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
                    ? t("operations.reachable")
                    : dependency.status === "not_configured"
                      ? t("operations.notConfigured")
                      : t("operations.unreachable2")}
                </span>
              </span>
            </div>
          );
        })}
        {diagnostics.dependencies.length === 0 && (
          <div className="px-5 py-5 text-sm text-fg-tertiary">
            {t("operations.noDependencyChecksReported")}
          </div>
        )}
        {scanner && scannerStatusLabel && (
          <div className="ag-diagnostic-status-row">
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-baseline gap-2 text-sm text-fg">
                <span>
                  {t("operations.artifactScanner")} · {scanner.name}
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
                    {t("operations.formats")}: {scanner.formats.join(" · ")}
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
              <span className="ml-2">{t(scannerStatusLabel)}</span>
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
  const { locale, t } = usePreferences();
  const queueColumns: ColumnsType<DiagnosticQueueStat> = [
    {
      title: t("operations.jobType"),
      dataIndex: "kind",
      key: "kind",
      width: 150,
      render: (kind: string) => (
        <span className="font-mono text-xs">{kind}</span>
      ),
    },
    {
      title: t("operations.format"),
      dataIndex: "format",
      key: "format",
      width: 110,
      responsive: ["sm"],
      render: (format: string) => <FormatBadge format={format} />,
    },
    {
      title: t("operations.status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (state: string) => <StateBadge state={state} />,
    },
    {
      title: t("operations.count"),
      dataIndex: "count",
      key: "count",
      width: 80,
      align: "right",
    },
    {
      title: t("operations.oldestQueued"),
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
      <CardHeader title={t("operations.backgroundQueues")} />
      <ConsoleTable<DiagnosticQueueStat>
        rowKey={(queue) => `${queue.kind}-${queue.format}-${queue.state}`}
        dataSource={diagnostics.queues}
        columns={queueColumns}
        locale={{
          emptyText: (
            <EmptyState
              compact
              title={t("operations.noActiveQueueEntries")}
              hint={t("operations.backgroundJobsAppearHereAsSoonAsThey")}
            />
          ),
        }}
        pagination={false}
        scroll={diagnostics.queues.length > 8 ? { x: 500, y: 320 } : { x: 500 }}
      />
    </Card>
  );
}
