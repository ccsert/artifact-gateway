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
  const { locale, t } = usePreferences();
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
    return <Loading label={t("operations.loadingDiagnostics")} />;

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
          title={t(
            "operations.refreshFailedShowingThePreviousDiagnosticSnapshot",
          )}
        />
      )}
      <Card className="ag-diagnostics-snapshot">
        <div className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold tracking-tight text-fg-strong">
              {t("operations.diagnosticSnapshot")}
            </h2>
            <p className="mt-1 text-xs text-fg-tertiary">
              {t("operations.generated")}{" "}
              {formatDate(diagnostics.generatedAt, locale)}
            </p>
          </div>
          <Space size="small" wrap>
            <Tooltip title={t("operations.copySanitizedDiagnosticsJSON")}>
              <Button
                aria-label={t("operations.copySanitizedDiagnosticsJSON")}
                icon={<CopyOutlined />}
                onClick={() =>
                  void copy(JSON.stringify(diagnostics, null, 2), "diagnostics")
                }
              >
                {copied ? t("operations.copied") : t("operations.copy")}
              </Button>
            </Tooltip>
            <Button
              aria-label={t("operations.refreshDiagnostics")}
              icon={<ReloadOutlined />}
              loading={loading}
              onClick={() => void load()}
            >
              {t("operations.refresh")}
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
          title={t("operations.systemHealthNeedsAttention")}
          description={t(
            "operations.resolveTheDependencyScannerNodeCapabilityOrFailed",
          )}
        />
      )}
      <MetricStrip
        items={[
          {
            label: t("operations.dependenciesReady"),
            value: diagnostics.dependencies.length - unavailable,
            hint: t("operations.checks", {
              length: diagnostics.dependencies.length,
            }),
            tone: unavailable ? "danger" : "success",
          },
          {
            label: t("operations.onlineNodes"),
            value: diagnostics.nodes.online,
            hint: t("operations.staleOffline", {
              stale: diagnostics.nodes.stale,
              offline: diagnostics.nodes.offline,
            }),
            tone:
              diagnostics.nodes.status === "healthy" ? "success" : "warning",
          },
          {
            label: t("operations.queuedWork"),
            value: queueTotal,
            hint: t("operations.pendingRunningRetryingAndFailed"),
            tone: queueTotal ? "warning" : "default",
          },
          {
            label: t("operations.failedWork"),
            value: failedQueues,
            hint: failedQueues
              ? t("operations.needsReview")
              : t("operations.noFailures"),
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
