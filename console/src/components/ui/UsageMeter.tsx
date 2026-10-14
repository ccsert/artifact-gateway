import { formatBytes } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

/** Usage above these shares of the quota reads as warning, then danger. */
export const USAGE_WARNING = 0.7;
export const USAGE_DANGER = 0.9;

/**
 * Stored bytes against a quota: the figures, and under them a thin bar
 * coloured by how close the repository is to its limit. Without a quota only
 * the stored size shows.
 */
export function UsageMeter({
  usedBytes,
  quotaBytes,
}: {
  usedBytes: number;
  quotaBytes: number;
}) {
  const { t } = usePreferences();
  if (quotaBytes <= 0) {
    return (
      <span className="font-mono text-xs text-fg-secondary">
        {formatBytes(usedBytes)}
      </span>
    );
  }
  const share = Math.min(usedBytes / quotaBytes, 1);
  const tone =
    share >= USAGE_DANGER
      ? "danger"
      : share >= USAGE_WARNING
        ? "warning"
        : "neutral";
  const percent = Math.round(share * 100);
  return (
    <div
      className="ag-usage-meter"
      data-tone={tone}
      role="meter"
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={percent}
      aria-label={t("ui.quotaUsage", {
        used: formatBytes(usedBytes),
        quota: formatBytes(quotaBytes),
        percent,
      })}
    >
      <div className="font-mono text-xs text-fg-secondary">
        {formatBytes(usedBytes)}
        <span className="text-fg-disabled"> / {formatBytes(quotaBytes)}</span>
      </div>
      <div className="ag-usage-meter-track" aria-hidden="true">
        <div
          className="ag-usage-meter-fill"
          style={{ width: `${Math.max(share * 100, 2)}%` }}
        />
      </div>
    </div>
  );
}
