import type { RuntimeLogEntry } from "../../client";

export const MAX_LOG_RECORDS = 300;
export const MAX_LOG_BYTES = 1_048_576;
export const MAX_DISPLAY_CHARS = 4096;
const encoder = new TextEncoder();

const failurePhases: Record<string, string> = {
  upstream_configuration_invalid: "prepare",
  upstream_policy_rejected: "prepare",
  upstream_egress_failed: "egress",
  upstream_transport_failed: "fetch",
  upstream_status_rejected: "fetch",
  upstream_body_failed: "body",
  unknown: "unknown",
};

// Keep log output as literal text, including ANSI and bidirectional controls.
export function logText(value: string, limit = 16_384): string {
  const visible = value
    .slice(0, limit)
    .replace(/[\p{Cc}\u202a-\u202e\u2066-\u2069]/gu, (char) =>
      char === "\n" || char === "\t"
        ? char
        : `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`,
    );
  return visible.length > limit || value.length > limit
    ? `${visible.slice(0, limit)}…`
    : visible;
}

// Deliberately copy the safe API projection instead of spreading a response
// object into a download or clipboard payload.
export function projectLog(row: RuntimeLogEntry): RuntimeLogEntry {
  const failure =
    typeof row.errorCode === "string" &&
    typeof row.phase === "string" &&
    Object.hasOwn(failurePhases, row.errorCode) &&
    failurePhases[row.errorCode] === row.phase
      ? { errorCode: row.errorCode, phase: row.phase }
      : {};
  return {
    sequence: row.sequence,
    time: logText(row.time),
    level: logText(row.level),
    message: logText(row.message),
    instanceId: logText(row.instanceId),
    sessionId: logText(row.sessionId),
    component: logText(row.component),
    operation: logText(row.operation),
    requestId: logText(row.requestId),
    traceId: logText(row.traceId),
    ...(row.status === undefined ? {} : { status: row.status }),
    ...(row.durationMs === undefined ? {} : { durationMs: row.durationMs }),
    ...(row.method === undefined ? {} : { method: row.method }),
    ...(row.route === undefined ? {} : { route: logText(row.route, 256) }),
    ...failure,
    ...(row.requestClass === undefined
      ? {}
      : { requestClass: row.requestClass }),
    ...(row.jobId === undefined ? {} : { jobId: logText(row.jobId) }),
    ...(row.attempt === undefined ? {} : { attempt: row.attempt }),
  };
}

export function logNDJSON(entries: RuntimeLogEntry[]): string {
  return entries.map((row) => `${JSON.stringify(projectLog(row))}\n`).join("");
}

export function boundRuntimeLogs(
  rows: RuntimeLogEntry[],
  instance: string,
  session: string,
) {
  const unique = new Map<number, RuntimeLogEntry>();
  for (const row of rows) {
    if (
      row.instanceId === instance &&
      row.sessionId === session &&
      !unique.has(row.sequence)
    ) {
      unique.set(row.sequence, {
        ...projectLog(row),
        instanceId: row.instanceId,
        sessionId: row.sessionId,
      });
    }
  }
  const ordered = [...unique.values()].sort(
    (left, right) => left.sequence - right.sequence,
  );
  const entries: RuntimeLogEntry[] = [];
  let bytes = 0;
  for (let i = ordered.length - 1; i >= 0; i--) {
    const row = ordered[i];
    const size = encoder.encode(logNDJSON([row])).byteLength;
    if (entries.length === MAX_LOG_RECORDS || bytes + size > MAX_LOG_BYTES)
      break;
    entries.unshift(row);
    bytes += size;
  }
  return { entries, bytes, dropped: ordered.length - entries.length };
}
