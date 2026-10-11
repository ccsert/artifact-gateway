import type { DiagnosticScanner, Diagnostics } from "../../client";
import { formatDate } from "../../lib/format";

type Localize = (chinese: string, english: string) => string;

export const scannerStatusLabels: Record<
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

/** Scanner states that put the scanner on the needs-attention list. */
export function scannerNeedsAttentionFor(diagnostics: Diagnostics): boolean {
  return (
    diagnostics.scanner !== undefined &&
    diagnostics.scanner.status !== "healthy" &&
    diagnostics.scanner.status !== "not_configured"
  );
}

/** Every unhealthy dependency, scanner, node and failed queue, one row each. */
