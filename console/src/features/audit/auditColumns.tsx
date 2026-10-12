import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type { AuditRecord } from "../../client";
import { FormatBadge, StatusText, statusTone } from "../../components/ui/Badge";
import { formatBytes, formatDate } from "../../lib/format";
import type { AppLocale } from "../../lib/preferences";
import { auditOperationLabel } from "./auditOperations";
import { auditOutcomeLabel, auditOutcomeTone } from "./auditOutcomes";

type Text = (zh: string, en: string) => string;

export type AuditTableRow = {
  key: string;
  rowIndex: number;
  record: AuditRecord;
};

export function auditColumns(
  text: Text,
  locale: AppLocale,
): ColumnsType<AuditTableRow> {
  return [
    {
      title: text("时间", "Time"),
      key: "occurredAt",
      width: 180,
      render: (_, row) => (
        <span className="whitespace-nowrap font-mono text-xs text-fg-secondary">
          {formatDate(row.record.occurredAt, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Operation"),
      key: "operation",
      width: 190,
      render: (_, row) => (
        <span
          className="block max-w-52 truncate text-xs text-fg"
          title={row.record.operation}
        >
          {row.record.operation
            ? auditOperationLabel(row.record.operation, text)
            : "—"}
        </span>
      ),
    },
    {
      title: text("结果", "Outcome"),
      key: "outcome",
      width: 170,
      render: (_, row) => (
        <span title={row.record.outcome}>
          <StatusText tone={statusTone(auditOutcomeTone(row.record.outcome))}>
            {row.record.outcome
              ? auditOutcomeLabel(row.record.outcome, text)
              : "—"}
          </StatusText>
        </span>
      ),
    },
    {
      title: text("仓库/分组", "Repository / group"),
      key: "repository",
      width: 180,
      render: (_, row) => (
        <span className="block max-w-40 truncate font-mono text-xs text-fg-secondary">
          {row.record.repository ?? row.record.groupName ?? "—"}
        </span>
      ),
    },
    {
      title: text("格式", "Format"),
      key: "format",
      width: 110,
      render: (_, row) =>
        row.record.format ? <FormatBadge format={row.record.format} /> : "—",
    },
    {
      title: text("状态", "Status"),
      key: "status",
      width: 90,
      render: (_, row) => (
        <span className="font-mono text-xs text-fg-secondary">
          {row.record.status ?? "—"}
        </span>
      ),
    },
    {
      title: text("流量", "Traffic"),
      key: "bytes",
      width: 110,
      render: (_, row) => (
        <span className="text-xs text-fg-secondary">
          {formatBytes(row.record.bytes)}
        </span>
      ),
    },
    {
      title: text("访问主体", "Actor"),
      key: "actor",
      width: 170,
      render: (_, row) => (
        <span
          className="block max-w-32 truncate text-xs text-fg-tertiary"
          title={row.record.actor}
        >
          {row.record.actor ?? "—"}
        </span>
      ),
    },
  ];
}

/** The fields of one record that do not fit in its row. */
export function AuditRecordDetail({
  record,
  text,
}: {
  record: AuditRecord;
  text: Text;
}) {
  return (
    <div className="grid grid-cols-3 gap-x-8 gap-y-2 px-2 py-1 text-xs">
      {(
        [
          [text("资源", "Resource"), record.resource],
          [text("表示", "Representation"), record.representation],
          [text("成员", "Member"), record.memberName],
          [text("成员类型", "Member type"), record.memberType],
          [text("上游主机", "Upstream host"), record.upstreamHost],
          [text("缓存", "Cache"), record.cacheDisposition],
          [
            text("授权来源", "Authorization source"),
            record.authorizationSource,
          ],
          [
            text("授权原因", "Authorization reason"),
            record.authorizationReason,
          ],
          ["Request ID", record.requestId],
          ["Trace ID", record.traceId],
        ] as const
      ).map(([label, value]) => (
        <div key={label} className="flex min-w-0 gap-2">
          <span className="w-20 shrink-0 text-fg-disabled">{label}</span>
          {value && (label === "Request ID" || label === "Trace ID") ? (
            <Link
              className="min-w-0 break-all font-mono"
              to={`/system?tab=logs&${label === "Request ID" ? "requestId" : "traceId"}=${encodeURIComponent(value)}`}
            >
              {value}
            </Link>
          ) : (
            <span className="min-w-0 break-all font-mono text-fg-secondary">
              {value ?? "—"}
            </span>
          )}
        </div>
      ))}
    </div>
  );
}
