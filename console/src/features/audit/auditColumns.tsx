import type { Translate } from "../../lib/preferences";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import type { AuditRecord } from "../../client";
import { FormatBadge, StatusText, statusTone } from "../../components/ui/Badge";
import { formatBytes, formatDate } from "../../lib/format";
import type { AppLocale } from "../../lib/preferences";
import { auditOperationLabel } from "./auditOperations";
import { auditOutcomeLabel, auditOutcomeTone } from "./auditOutcomes";

export type AuditTableRow = {
  key: string;
  rowIndex: number;
  record: AuditRecord;
};

export function auditColumns(
  t: Translate,
  locale: AppLocale,
): ColumnsType<AuditTableRow> {
  return [
    {
      title: t("audit.time"),
      key: "occurredAt",
      width: 170,
      render: (_, row) => (
        <span className="whitespace-nowrap font-mono text-xs text-fg-secondary">
          {formatDate(row.record.occurredAt, locale)}
        </span>
      ),
    },
    {
      title: t("audit.operation"),
      key: "operation",
      width: 170,
      render: (_, row) => (
        <span
          className="block max-w-52 truncate text-xs text-fg"
          title={row.record.operation}
        >
          {row.record.operation
            ? auditOperationLabel(row.record.operation, t)
            : "—"}
        </span>
      ),
    },
    {
      title: t("audit.outcome"),
      key: "outcome",
      width: 110,
      render: (_, row) => (
        <span title={row.record.outcome}>
          <StatusText tone={statusTone(auditOutcomeTone(row.record.outcome))}>
            {row.record.outcome
              ? auditOutcomeLabel(row.record.outcome, t)
              : "—"}
          </StatusText>
        </span>
      ),
    },
    {
      title: t("audit.repositoryGroup"),
      key: "repository",
      width: 160,
      render: (_, row) => (
        <span className="block max-w-40 truncate font-mono text-xs text-fg-secondary">
          {row.record.repository ?? row.record.groupName ?? "—"}
        </span>
      ),
    },
    {
      title: t("audit.format"),
      key: "format",
      width: 100,
      render: (_, row) =>
        row.record.format ? <FormatBadge format={row.record.format} /> : "—",
    },
    {
      title: t("audit.status"),
      key: "status",
      width: 70,
      render: (_, row) => (
        <span className="font-mono text-xs text-fg-secondary">
          {row.record.status ?? "—"}
        </span>
      ),
    },
    {
      title: t("audit.traffic"),
      key: "bytes",
      width: 90,
      render: (_, row) => (
        <span className="text-xs text-fg-secondary">
          {formatBytes(row.record.bytes)}
        </span>
      ),
    },
    {
      title: t("audit.actor"),
      key: "actor",
      // The remaining width goes to the actor, the longest value in a row.
      render: (_, row) => (
        <span
          className="block truncate text-xs text-fg-tertiary"
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
  t,
}: {
  record: AuditRecord;
  t: Translate;
}) {
  const publication =
    record.format === "conan" && record.operation === "publish";
  const evidence = record.evidence;
  // Evidence crosses a JSON boundary. Only the two fields emitted by native
  // Conan publication are meaningful here; never render arbitrary evidence.
  const sessionId =
    publication &&
    evidence !== null &&
    typeof evidence === "object" &&
    !Array.isArray(evidence) &&
    typeof evidence.publicationSessionId === "string" &&
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(
      evidence.publicationSessionId,
    )
      ? evidence.publicationSessionId
      : undefined;
  const publicationFields: ReadonlyArray<
    readonly [string, string | undefined]
  > = publication
    ? [
        [t("audit.publicationSession"), sessionId],
        [
          t("audit.publicationState"),
          sessionId && evidence?.publicationState === "committed"
            ? t("audit.publicationCommitted")
            : undefined,
        ],
      ]
    : [];
  return (
    <div className="grid grid-cols-1 gap-x-8 gap-y-2 px-2 py-1 text-xs md:grid-cols-2 xl:grid-cols-3">
      {(
        [
          ...publicationFields,
          [t("audit.resource"), record.resource],
          [t("audit.representation"), record.representation],
          [t("audit.member"), record.memberName],
          [t("audit.memberType"), record.memberType],
          [t("audit.upstreamHost"), record.upstreamHost],
          [t("audit.cache"), record.cacheDisposition],
          [t("audit.authorizationSource"), record.authorizationSource],
          [t("audit.authorizationReason"), record.authorizationReason],
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
