import { CopyOutlined } from "@ant-design/icons";
import { Button } from "antd";
import type { RuntimeLogEntry } from "../../client";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { logText, MAX_DISPLAY_CHARS, projectLog } from "./runtimeLogView";

/** The copy text for one entry: its projected fields as JSON. */
export function logCopy(entry: RuntimeLogEntry) {
  return JSON.stringify(projectLog(entry), null, 2);
}

/** One log line with its request detail and a copy action. */
export function RuntimeLogRow({
  entry,
  locale,
  copied,
  onCopy,
}: {
  entry: RuntimeLogEntry;
  locale: string;
  copied: boolean;
  onCopy: () => void;
}) {
  const { text } = usePreferences();
  return (
    <div className="ag-runtime-log-row" data-sequence={entry.sequence}>
      <span className="ag-runtime-log-time">
        {logText(formatDate(entry.time, locale), MAX_DISPLAY_CHARS)}
      </span>
      <span
        className={`ag-runtime-log-level ag-runtime-log-level-${entry.level.toLowerCase()}`}
      >
        {logText(entry.level, MAX_DISPLAY_CHARS)}
      </span>
      <span className="ag-runtime-log-scope">
        {logText(entry.instanceId, MAX_DISPLAY_CHARS)} /{" "}
        {logText(entry.sessionId, MAX_DISPLAY_CHARS)}
      </span>
      <span className="ag-runtime-log-component">
        {logText(entry.component || "—", MAX_DISPLAY_CHARS)} ·{" "}
        {logText(entry.operation, MAX_DISPLAY_CHARS)}
      </span>
      <span className="ag-runtime-log-message">
        {logText(entry.message, MAX_DISPLAY_CHARS)}
      </span>
      <Button
        className="ag-runtime-log-copy"
        size="small"
        type="text"
        icon={<CopyOutlined />}
        onClick={onCopy}
      >
        {copied ? text("已复制", "Copied") : text("复制", "Copy")}
      </Button>
      <div className="ag-runtime-log-detail">
        {logText(
          [
            entry.method,
            entry.route,
            entry.requestClass,
            entry.status === undefined ? "" : `status=${entry.status}`,
            entry.durationMs === undefined ? "" : `${entry.durationMs}ms`,
            entry.errorCode ? `errorCode=${entry.errorCode}` : "",
            entry.phase ? `phase=${entry.phase}` : "",
            entry.jobId ? `job=${entry.jobId}` : "",
            entry.attempt === undefined ? "" : `attempt=${entry.attempt}`,
            `Request ${entry.requestId || "—"}`,
            `Trace ${entry.traceId || "—"}`,
          ]
            .filter(Boolean)
            .join(" · "),
          MAX_DISPLAY_CHARS,
        )}
      </div>
    </div>
  );
}
