import type { AuditRecord } from "../../client";
import { downloadCsv, toCsv } from "../../lib/csv";
import type { AppLocale } from "../../lib/preferences";

const AUDIT_CSV_COLUMNS_ZH = [
  "时间",
  "操作",
  "结果",
  "仓库",
  "分组",
  "格式",
  "状态",
  "流量",
  "Actor",
  "资源",
  "表示",
  "成员",
  "成员类型",
  "上游主机",
  "缓存",
  "授权来源",
  "授权原因",
  "Request ID",
  "Trace ID",
];
const AUDIT_CSV_COLUMNS_EN = [
  "Time",
  "Operation",
  "Outcome",
  "Repository",
  "Group",
  "Format",
  "Status",
  "Traffic",
  "Actor",
  "Resource",
  "Representation",
  "Member",
  "Member type",
  "Upstream host",
  "Cache",
  "Authorization source",
  "Authorization reason",
  "Request ID",
  "Trace ID",
];

/** Download the records on screen as CSV, with headers in the UI language. */
export function exportAuditCsv(records: AuditRecord[], locale: AppLocale) {
  const rows = records.map((a) => [
    a.occurredAt,
    a.operation,
    a.outcome,
    a.repository,
    a.groupName,
    a.format,
    a.status,
    a.bytes,
    a.actor,
    a.resource,
    a.representation,
    a.memberName,
    a.memberType,
    a.upstreamHost,
    a.cacheDisposition,
    a.authorizationSource,
    a.authorizationReason,
    a.requestId,
    a.traceId,
  ]);
  downloadCsv(
    `audits-${new Date().toISOString().slice(0, 19)}.csv`,
    toCsv(
      locale === "en-US" ? AUDIT_CSV_COLUMNS_EN : AUDIT_CSV_COLUMNS_ZH,
      rows,
    ),
  );
}
