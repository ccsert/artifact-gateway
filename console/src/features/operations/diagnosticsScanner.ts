import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
import type { DiagnosticScanner, Diagnostics } from "../../client";
import { formatDate } from "../../lib/format";

export const scannerStatusLabels: Record<
  DiagnosticScanner["status"],
  MessageKey
> = {
  healthy: "operations.healthy",
  degraded: "operations.degraded",
  unhealthy: "operations.unhealthy",
  unreachable: "operations.unreachable",
  unknown: "operations.unknown",
  not_configured: "operations.notConfigured",
};

export const scannerStatusTone: Record<DiagnosticScanner["status"], string> = {
  healthy: "text-[var(--ag-status-success)]",
  degraded: "text-[var(--ag-status-warning)]",
  unhealthy: "text-[var(--ag-status-danger)]",
  unreachable: "text-[var(--ag-status-danger)]",
  unknown: "text-[var(--ag-status-warning)]",
  not_configured: "text-fg-tertiary",
};

export function scannerOperationalDetail(
  scanner: DiagnosticScanner,
  t: Translate,
  locale: string,
): string {
  if (scanner.status === "not_configured") {
    return t("operations.externalScannerIsNotConfigured");
  }
  if (scanner.status === "unknown") {
    return t("operations.healthReportingUnavailableDatabaseFreshnessUnknown");
  }
  const databaseDetail =
    scanner.databaseFreshness === "unknown"
      ? t("operations.vulnerabilityDatabaseUpdateTimeUnavailable")
      : t(
          scanner.databaseFreshness === "fresh"
            ? "operations.vulnerabilityDatabaseFresh"
            : "operations.vulnerabilityDatabaseStale",
          { date: formatDate(scanner.databaseUpdatedAt, locale) },
        );
  if (scanner.status === "unreachable") {
    return t("operations.healthCheckFailed", { databaseDetail });
  }
  if (scanner.status === "unhealthy") {
    return t("operations.scannerReportsUnhealthy", { databaseDetail });
  }
  if (scanner.status === "degraded" && scanner.databaseFreshness !== "stale") {
    return t("operations.scannerReportsDegraded", { databaseDetail });
  }
  return databaseDetail;
}

/** Scanner states that put the scanner on the needs-attention list. */
export function scannerNeedsAttentionFor(diagnostics: Diagnostics): boolean {
  return (
    diagnostics.scanner !== undefined &&
    diagnostics.scanner.status !== "healthy" &&
    diagnostics.scanner.status !== "not_configured"
  );
}

/** Every unhealthy dependency, scanner, node and failed queue, one row each. */
