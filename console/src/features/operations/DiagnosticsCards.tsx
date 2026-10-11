import type { ReactNode } from "react";
import { Alert } from "antd";
import type { Diagnostics } from "../../client";
import { buildVersionsMismatch } from "../../lib/buildVersion";
import { usePreferences } from "../../lib/preferences";
import { StateBadge } from "../../components/ui/Badge";
import { Card, CardHeader } from "../../components/ui/Layout";
import {
  scannerNeedsAttentionFor,
  scannerOperationalDetail,
  scannerStatusLabels,
  scannerStatusTone,
} from "./diagnosticsScanner";

function DiagnosticIdentityItem({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="ag-diagnostic-identity-row">
      <dt>{label}</dt>
      <dd className={mono ? "font-mono" : undefined}>{value}</dd>
    </div>
  );
}

export function DiagnosticsAttentionCard({
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
  const scannerNeedsAttention = scannerNeedsAttentionFor(diagnostics);
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
  if (!needsAttention) return null;

  return (
    <Card>
      <CardHeader
        title={text("需要处理", "Needs attention")}
        extra={
          <span className="text-xs text-fg-tertiary">
            {text(`${attentionCount} 项`, `${attentionCount} items`)}
          </span>
        }
      />
      <div className="divide-y divide-line">
        {unavailableDependencies.map((dependency) => (
          <div key={dependency.name} className="ag-diagnostic-issue-row">
            <div className="min-w-0">
              <div className="text-sm font-medium text-fg">
                {text("依赖不可用", "Dependency unavailable")} ·{" "}
                {dependency.name}
              </div>
              <div className="mt-1 break-words text-xs leading-5 text-fg-tertiary">
                {dependency.detail}
              </div>
            </div>
            <StateBadge state={dependency.status} />
          </div>
        ))}
        {scannerNeedsAttention && scanner && scannerStatusLabel && (
          <div className="ag-diagnostic-issue-row">
            <div className="min-w-0">
              <div className="text-sm font-medium text-fg">
                {text("扫描器可信度", "Scanner trust")} · {scanner.name}
              </div>
              <div className="mt-1 break-words text-xs leading-5 text-fg-tertiary">
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
              <div className="font-mono text-xs text-[var(--ag-status-warning)]">
                {issue.code}
              </div>
              <div className="mt-1 break-words text-xs leading-5 text-fg-secondary">
                {issue.message}
              </div>
            </div>
            <StateBadge state={diagnostics.nodes.status} />
          </div>
        ))}
        {nodeStatusNeedsAttention && nodeIssues.length === 0 && (
          <div className="ag-diagnostic-issue-row">
            <div className="min-w-0">
              <div className="text-sm font-medium text-fg">
                {text("运行节点状态异常", "Runtime node health degraded")}
              </div>
              <div className="mt-1 text-xs leading-5 text-fg-tertiary">
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
              <div className="text-sm font-medium text-fg">
                {text("失败队列", "Failed queue")} · {queue.kind}
              </div>
              <div className="mt-1 text-xs leading-5 text-fg-tertiary">
                {queue.format} ·{" "}
                {text(`${queue.count} 个任务`, `${queue.count} jobs`)}
              </div>
            </div>
            <StateBadge state={queue.state} />
          </div>
        ))}
      </div>
    </Card>
  );
}

export function DiagnosticsIdentityCard({
  diagnostics,
  consoleVersion,
}: {
  diagnostics: Diagnostics;
  consoleVersion: string;
}) {
  const { text } = usePreferences();
  return (
    <Card className="ag-diagnostics-identity-card">
      <CardHeader
        title={text("构建与运行身份", "Build and runtime identity")}
      />
      <div className="ag-diagnostic-identity-groups">
        <section className="ag-diagnostic-identity-group">
          <h3>{text("构建信息", "Build information")}</h3>
          <div className="grid gap-4">
            <dl>
              <DiagnosticIdentityItem
                label={text("Console 版本", "Console version")}
                value={consoleVersion}
              />
              <DiagnosticIdentityItem
                label={text("Gateway 版本", "Gateway version")}
                value={diagnostics.build.version}
              />
              <DiagnosticIdentityItem
                label={text("Gateway 修订", "Gateway revision")}
                value={diagnostics.build.revision}
                mono
              />
              <DiagnosticIdentityItem
                label="Go"
                value={diagnostics.build.goVersion}
                mono
              />
              <DiagnosticIdentityItem
                label={text("工作区", "Workspace")}
                value={
                  diagnostics.build.modified
                    ? text("有未提交修改", "Modified")
                    : text("干净", "Clean")
                }
              />
            </dl>
            {buildVersionsMismatch(
              consoleVersion,
              diagnostics.build.version,
            ) && (
              <Alert
                type="warning"
                showIcon
                title={text(
                  "前后端版本不一致",
                  "Console and Gateway versions differ",
                )}
                description={text(
                  "可能是镜像 tag 不一致或浏览器缓存了旧 bundle。请核对部署版本并刷新页面。",
                  "Image tags may differ, or the browser may have cached an older bundle. Check the deployed versions and refresh the page.",
                )}
              />
            )}
          </div>
        </section>
        <section className="ag-diagnostic-identity-group">
          <h3>{text("运行身份", "Runtime identity")}</h3>
          <dl>
            <DiagnosticIdentityItem
              label={text("实例", "Instance")}
              value={diagnostics.runtime.instanceId || "—"}
              mono
            />
            <DiagnosticIdentityItem
              label={text("角色", "Roles")}
              value={diagnostics.runtime.roles.join(" · ") || "—"}
            />
            <DiagnosticIdentityItem
              label={text("Worker 格式", "Worker formats")}
              value={diagnostics.runtime.workerFormats.join(" · ") || "—"}
            />
            <DiagnosticIdentityItem
              label={text("Worker 任务", "Worker jobs")}
              value={diagnostics.runtime.workerKinds.join(" · ") || "—"}
            />
          </dl>
        </section>
      </div>
    </Card>
  );
}
