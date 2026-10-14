import type { AuditRecord } from "../../client";
import { downloadCsv, toCsv } from "../../lib/csv";
import type { Translate } from "../../lib/preferences";
import type { MessageKey } from "../../messages";

const AUDIT_CSV_COLUMNS: MessageKey[] = [
  "audit.csvTime",
  "audit.csvOperation",
  "audit.csvOutcome",
  "audit.csvRepository",
  "audit.csvGroup",
  "audit.csvFormat",
  "audit.csvStatus",
  "audit.csvTraffic",
  "audit.csvActor",
  "audit.csvResource",
  "audit.csvRepresentation",
  "audit.csvMember",
  "audit.csvMemberType",
  "audit.csvUpstreamHost",
  "audit.csvCache",
  "audit.csvAuthorizationSource",
  "audit.csvAuthorizationReason",
  "audit.csvRequestId",
  "audit.csvTraceId",
];

/** Download the records on screen as CSV, with headers in the UI language. */
export function exportAuditCsv(records: AuditRecord[], t: Translate) {
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
      AUDIT_CSV_COLUMNS.map((column) => t(column)),
      rows,
    ),
  );
}
