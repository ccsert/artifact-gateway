import type { AuditRecord } from "../../client";
import { StatusText, statusTone } from "../../components/ui/Badge";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { auditOperationLabel } from "../audit/auditOperations";
import {
  auditOutcomeIsDenied,
  auditOutcomeLabel,
  auditOutcomeTone,
} from "../audit/auditOutcomes";

/**
 * Recent audit events as readable activity: what happened to which object on
 * the first line, who and when on the second. Codes come through the same
 * label tables as the audit log, so both pages say the same words.
 */
export function RecentAuditList({ records }: { records: AuditRecord[] }) {
  const { t, locale } = usePreferences();

  return (
    <ol className="ag-activity-list">
      {records.map((record) => {
        const operation = record.operation
          ? auditOperationLabel(record.operation, t)
          : t("dashboard.access");
        const target = [record.repository ?? record.groupName, record.resource]
          .filter(Boolean)
          .join("/");
        const denied = auditOutcomeIsDenied(record.outcome);
        // A resolved request is the normal case; only refusals and failures
        // should draw the eye.
        const tone = statusTone(auditOutcomeTone(record.outcome));
        return (
          <li
            key={
              record.requestId ??
              record.traceId ??
              `${record.occurredAt}-${record.actor ?? ""}-${record.operation ?? ""}-${record.resource ?? ""}`
            }
            className="ag-activity-item"
            data-denied={denied ? "true" : "false"}
          >
            <div className="ag-activity-title">
              <span title={record.operation}>{operation}</span>
              {target && (
                <span className="ag-activity-target font-mono" title={target}>
                  {target}
                </span>
              )}
            </div>
            <div className="ag-activity-meta">
              <span className="ag-activity-actor" title={record.actor}>
                {record.actor === "anonymous"
                  ? t("dashboard.anonymous")
                  : record.actor || t("dashboard.notRecorded")}
              </span>
              <span aria-hidden="true">·</span>
              <time dateTime={record.occurredAt}>
                {formatDate(record.occurredAt, locale)}
              </time>
              <span aria-hidden="true">·</span>
              <StatusText tone={tone === "success" ? "neutral" : tone}>
                {auditOutcomeLabel(record.outcome, t)}
              </StatusText>
            </div>
          </li>
        );
      })}
    </ol>
  );
}
