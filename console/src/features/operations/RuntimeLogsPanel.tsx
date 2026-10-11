import { useCallback, useEffect, useState } from "react";
import {
  CopyOutlined,
  InfoCircleOutlined,
  PauseOutlined,
  CaretRightOutlined,
  DownloadOutlined,
} from "@ant-design/icons";
import { Alert, Button, Space, Switch } from "antd";
import { useSearchParams } from "react-router-dom";
import { Card, CardHeader, Pagination } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { useRuntimeLogStream } from "./useRuntimeLogStream";
import type { LogQuery } from "./useRuntimeLogStream";
import {
  logNDJSON,
  logText,
  MAX_DISPLAY_CHARS,
  MAX_LOG_BYTES,
  MAX_LOG_RECORDS,
} from "./runtimeLogView";
import { RuntimeLogFilters } from "./RuntimeLogFilters";
import { logCopy, RuntimeLogRow } from "./RuntimeLogRow";
import { runtimeLogError } from "./runtimeLogErrors";
import "./RuntimeLogsPanel.css";

export function RuntimeLogsPanel() {
  const { locale, text } = usePreferences();
  const [searchParams] = useSearchParams();
  const auditRequestId = searchParams.get("requestId") ?? "";
  const auditTraceId = searchParams.get("traceId") ?? "";
  const [query, setQuery] = useState<LogQuery>(() => ({
    requestId: auditRequestId || undefined,
    traceId: auditTraceId || undefined,
    limit: 50,
  }));
  const {
    page,
    entries,
    error,
    loading,
    following,
    visible,
    gap,
    scopeChanged,
    trimmed,
    unread,
    bytes,
    viewport: viewportRef,
    follow,
    pause,
    resumeBottom,
    scrolled,
    refresh,
    loadOlder,
    hasOlder,
    resetVersion,
  } = useRuntimeLogStream(query);
  const [wrap, setWrap] = useState(true);
  const [selected, setSelected] = useState("");
  const [selectionTooLarge, setSelectionTooLarge] = useState(false);
  const [selectionVersion, setSelectionVersion] = useState(-1);
  const { copiedValue, copy } = useClipboardAction();

  const changedSelection = useCallback(() => {
    const selection = window.getSelection();
    const node = viewportRef.current;
    const withinStream =
      selection &&
      node &&
      selection.anchorNode &&
      selection.focusNode &&
      node.contains(selection.anchorNode) &&
      node.contains(selection.focusNode) &&
      Array.from({ length: selection.rangeCount }, (_, index) =>
        selection.getRangeAt(index),
      ).every(
        (range) =>
          node.contains(range.startContainer) &&
          node.contains(range.endContainer),
      );
    const raw = withinStream ? selection.toString() : "";
    const safe = logText(raw, MAX_LOG_BYTES);
    const tooLarge =
      raw.length > MAX_LOG_BYTES ||
      safe.length > MAX_LOG_BYTES ||
      new TextEncoder().encode(safe).byteLength > MAX_LOG_BYTES;
    setSelectionTooLarge(tooLarge);
    setSelected(tooLarge ? "" : safe);
    setSelectionVersion(resetVersion);
    return tooLarge ? "" : safe;
  }, [viewportRef, resetVersion]);

  useEffect(() => {
    const selection = window.getSelection();
    const node = viewportRef.current;
    if (selection?.anchorNode && node?.contains(selection.anchorNode))
      selection.removeAllRanges();
    changedSelection();
    document.addEventListener("selectionchange", changedSelection);
    return () =>
      document.removeEventListener("selectionchange", changedSelection);
  }, [changedSelection, viewportRef]);

  useEffect(() => {
    changedSelection();
  }, [entries, changedSelection]);

  const download = () => {
    const url = URL.createObjectURL(
      new Blob([logNDJSON(entries)], {
        type: "application/x-ndjson;charset=utf-8",
      }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "runtime-logs.ndjson";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  return (
    <Card>
      <CardHeader title={text("运行日志", "Runtime logs")} />
      <div className="ag-runtime-log-intro">
        <InfoCircleOutlined aria-hidden="true" />
        <span>
          {text(
            "仅查询当前进程的内存日志，重启后清空。",
            "Search this process’s in-memory logs; restart clears them.",
          )}
        </span>
      </div>
      <RuntimeLogFilters
        auditRequestId={auditRequestId}
        auditTraceId={auditTraceId}
        components={page?.source?.components ?? []}
        loading={loading}
        onRefresh={refresh}
        setQuery={setQuery}
      />
      {error ? (
        <div className="p-4">
          <ErrorBanner
            error={runtimeLogError(error, text)}
            tone={page ? "warning" : "error"}
            onRetry={refresh}
            title={
              scopeChanged
                ? text(
                    "进程或会话已变化，请刷新新快照",
                    "Process or session changed; refresh the snapshot",
                  )
                : undefined
            }
          />
        </div>
      ) : null}
      {loading && !page && !error ? (
        <div className="p-6">
          <Loading label={text("查询运行日志…", "Querying runtime logs…")} />
        </div>
      ) : null}
      {page ? (
        <div className="border-t border-[var(--ag-border-subtle)]">
          <div className="ag-runtime-log-source flex flex-wrap gap-x-3 gap-y-1 px-5 py-3 text-xs text-[var(--ag-content-tertiary)]">
            <span>
              {text("当前进程", "Current process")}:{" "}
              {logText(page.instanceId, MAX_DISPLAY_CHARS)}
            </span>
            <span>
              {text("会话", "Session")}:{" "}
              {logText(page.sessionId, MAX_DISPLAY_CHARS)}
            </span>
            <span>
              {text(
                `${entries.length} 条已加载 · ${bytes} 字节`,
                `${entries.length} loaded · ${bytes} bytes`,
              )}
            </span>
            <span>
              {text("保留序号", "Retained sequences")}:{" "}
              {page.retention
                ? `${page.retention.earliestSequence}–${page.retention.latestSequence}`
                : text("未报告", "Not reported")}
            </span>
            <span>
              {page.source
                ? text(
                    `最低 ${page.source.minimumLevel} · 请求 ${page.source.accessMode} · 慢请求 ${page.source.slowThresholdMs}ms · 缓冲 ${page.source.capacityLines} 行`,
                    `Minimum ${page.source.minimumLevel} · requests ${page.source.accessMode} · slow ${page.source.slowThresholdMs}ms · buffer ${page.source.capacityLines} lines`,
                  )
                : text(
                    "Gateway 未报告记录策略",
                    "Gateway did not report its logging policy",
                  )}
            </span>
          </div>
          <div className="ag-runtime-log-toolbar">
            <Space wrap>
              <Button
                icon={following ? <PauseOutlined /> : <CaretRightOutlined />}
                disabled={!page.afterCursor || Boolean(query.from || query.to)}
                onClick={following ? pause : follow}
              >
                {following
                  ? text("暂停跟随", "Pause follow")
                  : text("跟随新日志", "Follow new logs")}
              </Button>
              {following && !visible ? (
                <span>
                  {text("隐藏页面已暂停轮询", "Polling paused while hidden")}
                </span>
              ) : null}
              {unread > 0 ? (
                <Button onClick={resumeBottom}>
                  {text(
                    `${unread} 条未读 · 回到底部`,
                    `${unread} unread · Resume at bottom`,
                  )}
                </Button>
              ) : null}
              <label className="ag-runtime-log-wrap">
                <Switch size="small" checked={wrap} onChange={setWrap} />
                {text("折行", "Wrap lines")}
              </label>
              <Button
                icon={<CopyOutlined />}
                disabled={!selected || selectionVersion !== resetVersion}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => {
                  const current = changedSelection();
                  if (current) void copy(current, "selection");
                }}
              >
                {text("复制选择", "Copy selection")}
              </Button>
              <Button
                icon={<CopyOutlined />}
                disabled={!entries.length}
                onClick={() => void copy(logNDJSON(entries), "loaded")}
              >
                {text("复制已加载", "Copy loaded")}
              </Button>
              <Button
                icon={<DownloadOutlined />}
                disabled={!entries.length}
                onClick={download}
              >
                {text("下载 NDJSON", "Download NDJSON")}
              </Button>
            </Space>
            <details className="ag-runtime-log-help">
              <summary>
                {text("查询与复制说明", "Query and copy details")}
              </summary>
              <p>
                {text(
                  "暂停保留游标，恢复继续读取；离底阅读不会自动滚动。",
                  "Pause preserves the cursor for resume; reading above the bottom does not force scrolling.",
                )}
              </p>
              <p>
                {text(
                  `仅复制和导出已加载结果，最多 ${MAX_LOG_RECORDS} 条 / 1 MiB；每字段显示最多 ${MAX_DISPLAY_CHARS} 字符，选择复制超过 1 MiB 时拒绝。DEBUG 筛选不会启用 DEBUG；空结果不能判定未记录原因。`,
                  `Copy and export loaded results only, at most ${MAX_LOG_RECORDS} rows / 1 MiB; display fields capped at ${MAX_DISPLAY_CHARS} characters. Selection over 1 MiB is rejected. A DEBUG filter does not enable DEBUG; empty results do not identify why events were absent.`,
                )}
              </p>
            </details>
            {selectionTooLarge && selectionVersion === resetVersion ? (
              <Alert
                type="warning"
                showIcon
                title={text(
                  "选择内容超过 1 MiB，请缩小选择后复制",
                  "Selection exceeds 1 MiB; select less text to copy",
                )}
              />
            ) : null}
            {page.source?.componentsTruncated ? (
              <span>
                {text(
                  "组件列表有界，可输入其他精确名称；worker 不是通配。",
                  "The component list is bounded; enter another exact name. worker is not a wildcard.",
                )}
              </span>
            ) : null}
            {query.requestId || query.traceId ? (
              <span>
                {text("当前关联筛选", "Current correlation filters")}: Request{" "}
                {logText(query.requestId ?? "", MAX_DISPLAY_CHARS)} · Trace{" "}
                {logText(query.traceId ?? "", MAX_DISPLAY_CHARS)}
              </span>
            ) : null}
            {gap ? (
              <Alert
                type="warning"
                showIcon
                title={text(
                  "服务器保留已覆盖部分未读日志",
                  "Server retention overwrote some unread logs",
                )}
              />
            ) : null}
            {scopeChanged ? (
              <Alert
                type="warning"
                showIcon
                title={text(
                  "已切换新进程或会话，旧记录已清除",
                  "Changed to a new process or session; previous records cleared",
                )}
              />
            ) : null}
            {trimmed > 0 ? (
              <Alert
                type="info"
                showIcon
                title={text(
                  `客户端容量已移除 ${trimmed} 条；不表示服务器日志丢失`,
                  `Client capacity removed ${trimmed} rows; this does not indicate server log loss`,
                )}
              />
            ) : null}
          </div>
          {entries.length === 0 ? (
            <EmptyState
              compact
              title={text("当前范围内没有日志", "No logs in this range")}
              hint={text(
                "可选择最近 24 小时或调整筛选；日志仅保存在当前进程。",
                "Try the last 24 hours or different filters. Logs are held only in this process.",
              )}
            />
          ) : (
            <div
              ref={viewportRef}
              onScroll={scrolled}
              role="log"
              aria-live="off"
              aria-label={text("运行日志流", "Runtime log stream")}
              className={`ag-runtime-log-stream${wrap ? "" : " ag-runtime-log-nowrap"}`}
            >
              {entries.map((entry) => (
                <RuntimeLogRow
                  key={`${entry.instanceId}-${entry.sessionId}-${entry.sequence}`}
                  entry={entry}
                  locale={locale}
                  copied={
                    copiedValue === `${entry.sessionId}-${entry.sequence}`
                  }
                  onCopy={() =>
                    void copy(
                      logCopy(entry),
                      `${entry.sessionId}-${entry.sequence}`,
                    )
                  }
                />
              ))}
            </div>
          )}
          <Pagination
            hasMore={
              hasOlder &&
              entries.length < MAX_LOG_RECORDS &&
              bytes < MAX_LOG_BYTES - 16_384
            }
            loading={loading}
            label={text("加载更早日志", "Load older logs")}
            onMore={loadOlder}
          />
        </div>
      ) : null}
    </Card>
  );
}
