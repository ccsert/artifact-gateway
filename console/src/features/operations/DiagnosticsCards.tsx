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
  const { locale, t } = usePreferences();
  const scanner = diagnostics.scanner;
  const scannerStatusLabel = scanner
    ? scannerStatusLabels[scanner.status]
    : null;
  const scannerDetail = scanner
    ? scannerOperationalDetail(scanner, t, locale)
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
        title={t("operations.needsAttention")}
        extra={
          <span className="text-xs text-fg-tertiary">
            {t("operations.items", { attentionCount })}
          </span>
        }
      />
      <div className="divide-y divide-line">
        {unavailableDependencies.map((dependency) => (
          <div key={dependency.name} className="ag-diagnostic-issue-row">
            <div className="min-w-0">
              <div className="text-sm font-medium text-fg">
                {t("operations.dependencyUnavailable")} · {dependency.name}
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
                {t("operations.scannerTrust")} · {scanner.name}
              </div>
              <div className="mt-1 break-words text-xs leading-5 text-fg-tertiary">
                {scannerDetail}
              </div>
            </div>
            <span
              className={`shrink-0 text-xs ${scannerStatusTone[scanner.status]}`}
            >
              {t(scannerStatusLabel)}
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
                {t("operations.runtimeNodeHealthDegraded")}
              </div>
              <div className="mt-1 text-xs leading-5 text-fg-tertiary">
                {t("operations.staleAndOffline", {
                  stale: diagnostics.nodes.stale,
                  offline: diagnostics.nodes.offline,
                })}
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
                {t("operations.failedQueue")} · {queue.kind}
              </div>
              <div className="mt-1 text-xs leading-5 text-fg-tertiary">
                {queue.format} · {t("operations.jobs2", { count: queue.count })}
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
  const { t } = usePreferences();
  return (
    <Card className="ag-diagnostics-identity-card">
      <CardHeader title={t("operations.buildAndRuntimeIdentity")} />
      <div className="ag-diagnostic-identity-groups">
        <section className="ag-diagnostic-identity-group">
          <h3>{t("operations.buildInformation")}</h3>
          <div className="grid gap-4">
            <dl>
              <DiagnosticIdentityItem
                label={t("operations.consoleVersion")}
                value={consoleVersion}
              />
              <DiagnosticIdentityItem
                label={t("operations.gatewayVersion")}
                value={diagnostics.build.version}
              />
              <DiagnosticIdentityItem
                label={t("operations.gatewayRevision")}
                value={diagnostics.build.revision}
                mono
              />
              <DiagnosticIdentityItem
                label="Go"
                value={diagnostics.build.goVersion}
                mono
              />
              <DiagnosticIdentityItem
                label={t("operations.workspace")}
                value={
                  diagnostics.build.modified
                    ? t("operations.modified")
                    : t("operations.clean")
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
                title={t("operations.consoleAndGatewayVersionsDiffer")}
                description={t("operations.imageTagsMayDifferOrTheBrowserMay")}
              />
            )}
          </div>
        </section>
        <section className="ag-diagnostic-identity-group">
          <h3>{t("operations.runtimeIdentity")}</h3>
          <dl>
            <DiagnosticIdentityItem
              label={t("operations.instance2")}
              value={diagnostics.runtime.instanceId || "—"}
              mono
            />
            <DiagnosticIdentityItem
              label={t("operations.roles")}
              value={diagnostics.runtime.roles.join(" · ") || "—"}
            />
            <DiagnosticIdentityItem
              label={t("operations.workerFormats")}
              value={diagnostics.runtime.workerFormats.join(" · ") || "—"}
            />
            <DiagnosticIdentityItem
              label={t("operations.workerJobs")}
              value={diagnostics.runtime.workerKinds.join(" · ") || "—"}
            />
          </dl>
        </section>
      </div>
    </Card>
  );
}
