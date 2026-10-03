import { useCallback, useEffect, useRef, useState } from "react";
import {
  CopyOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
  SearchOutlined,
  PauseOutlined,
  CaretRightOutlined,
  DownloadOutlined,
} from "@ant-design/icons";
import {
  Alert,
  AutoComplete,
  Button,
  DatePicker,
  Input,
  Select,
  Space,
  Switch,
} from "antd";
import type { Dayjs } from "dayjs";
import { useSearchParams } from "react-router-dom";
import type { RuntimeLogEntry } from "../../client";
import { Card, CardHeader, Pagination } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  FilterBar,
  FilterField,
  useClipboardAction,
} from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { useRuntimeLogStream } from "./useRuntimeLogStream";
import type { LogQuery } from "./useRuntimeLogStream";
import {
  logNDJSON,
  logText,
  MAX_DISPLAY_CHARS,
  MAX_LOG_BYTES,
  MAX_LOG_RECORDS,
  projectLog,
} from "./runtimeLogView";
import "./RuntimeLogsPanel.css";

function logCopy(entry: RuntimeLogEntry) {
  return JSON.stringify(projectLog(entry), null, 2);
}

export function RuntimeLogsPanel() {
  const { locale, text } = usePreferences();
  const [searchParams] = useSearchParams();
  const auditRequestId = searchParams.get("requestId") ?? "";
  const auditTraceId = searchParams.get("traceId") ?? "";
  const lastAuditLink = useRef(`${auditRequestId}\n${auditTraceId}`);
  const [range, setRange] = useState<[Dayjs, Dayjs] | null>(null);
  const [instanceId, setInstanceId] = useState("");
  const [level, setLevel] = useState("");
  const [component, setComponent] = useState("");
  const [requestId, setRequestId] = useState(auditRequestId);
  const [traceId, setTraceId] = useState(auditTraceId);
  const [keyword, setKeyword] = useState("");
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

  useEffect(() => {
    const link = `${auditRequestId}\n${auditTraceId}`;
    if (lastAuditLink.current === link) return;
    lastAuditLink.current = link;
    setRequestId(auditRequestId);
    setTraceId(auditTraceId);
    setQuery((current) => ({
      ...current,
      requestId: auditRequestId || undefined,
      traceId: auditTraceId || undefined,
    }));
  }, [auditRequestId, auditTraceId]);

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

  const clearFilters = () => {
    setRange(null);
    setInstanceId("");
    setLevel("");
    setComponent("");
    setRequestId("");
    setTraceId("");
    setKeyword("");
    setQuery({ limit: 50 });
  };

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

  const search = () => {
    setQuery({
      from: range?.[0].toISOString(),
      to: range?.[1].toISOString(),
      instanceId: instanceId.trim() || undefined,
      level: level || undefined,
      component: component.trim() || undefined,
      requestId: requestId.trim() || undefined,
      traceId: traceId.trim() || undefined,
      keyword: keyword.trim() || undefined,
      limit: 50,
    });
  };

  return (
    <Card>
      <CardHeader title={text("运行日志", "Runtime logs")} />
      <div className="flex items-start gap-2 px-5 pt-4 text-xs leading-5 text-[var(--ag-content-secondary)]">
        <InfoCircleOutlined className="mt-0.5 shrink-0" aria-hidden="true" />
        <span>
          {text(
            "仅查询当前进程的内存日志，默认最近一小时；重启后记录会清空。其他节点及历史日志请使用部署的采集系统。",
            "Searches this process's in-memory logs from the past hour by default. Restart clears them; use the deployed collector for other nodes and retained history.",
          )}
        </span>
      </div>
      <FilterBar
        embedded
        actions={
          <Space wrap>
            <Button icon={<SearchOutlined />} type="primary" onClick={search}>
              {text("查询", "Search")}
            </Button>
            <Button
              icon={<ReloadOutlined />}
              loading={loading}
              onClick={refresh}
            >
              {text("刷新快照", "Refresh snapshot")}
            </Button>
            <Button onClick={clearFilters}>
              {text("清除筛选", "Clear filters")}
            </Button>
          </Space>
        }
      >
        <FilterField
          label={text("时间范围", "Time range")}
          className="min-w-[260px]"
        >
          <DatePicker.RangePicker
            className="w-full"
            showTime
            value={range}
            onChange={(value) => setRange(value as [Dayjs, Dayjs] | null)}
          />
        </FilterField>
        <FilterField label={text("节点", "Instance")} className="min-w-[170px]">
          <Input
            value={instanceId}
            onChange={(event) => setInstanceId(event.target.value)}
            placeholder={text("当前进程", "Current process")}
          />
        </FilterField>
        <FilterField label={text("级别", "Level")} className="min-w-[125px]">
          <Select
            className="w-full"
            value={level}
            onChange={setLevel}
            options={[
              { value: "", label: text("全部", "All") },
              ...["ERROR", "WARN", "INFO", "DEBUG"].map((value) => ({
                value,
                label: value,
              })),
            ]}
          />
        </FilterField>
        <FilterField
          label={text("组件", "Component")}
          className="w-full min-w-[160px] sm:w-auto"
        >
          <AutoComplete
            value={component}
            onChange={setComponent}
            options={page?.source?.components.map((value) => ({ value })) ?? []}
            placeholder={text("精确组件名称", "Exact component name")}
            className="w-full"
          />
        </FilterField>
        <FilterField
          label="Request ID"
          className="w-full min-w-[180px] sm:w-auto"
        >
          <Input
            value={requestId}
            onChange={(event) => setRequestId(event.target.value)}
          />
        </FilterField>
        <FilterField
          label="Trace ID"
          className="w-full min-w-[180px] sm:w-auto"
        >
          <Input
            value={traceId}
            onChange={(event) => setTraceId(event.target.value)}
          />
        </FilterField>
        <FilterField
          label={text("关键词", "Keyword")}
          className="w-full min-w-[180px] sm:w-auto"
        >
          <Input
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            onPressEnter={search}
          />
        </FilterField>
      </FilterBar>
      {error ? (
        <div className="p-4">
          <ErrorBanner
            error={error}
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
                disabled={!page.afterCursor}
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
            <span>
              {text(
                "暂停会保留增量游标；恢复后继续读取。离底阅读不自动滚动，未读数仅表示已读取的新日志。",
                "Pause preserves the incremental cursor for resume. Reading above the bottom does not force scrolling; unread counts cover newly fetched logs only.",
              )}
            </span>
            <span>
              {text(
                `仅当前筛选已加载结果，最多 ${MAX_LOG_RECORDS} 条 / ${MAX_LOG_BYTES} 字节；显示每字段最多 ${MAX_DISPLAY_CHARS} 字符，选择复制最多 ${MAX_LOG_BYTES} UTF-8 字节，超限拒绝复制。DEBUG 筛选不会启用 DEBUG；空结果不能判定未记录原因。`,
                `Only loaded results in the current filters, at most ${MAX_LOG_RECORDS} rows / ${MAX_LOG_BYTES} bytes; display fields capped at ${MAX_DISPLAY_CHARS} characters. Selection copy accepts at most ${MAX_LOG_BYTES} UTF-8 bytes and rejects larger selections. A DEBUG filter does not enable DEBUG; empty results do not identify why events were absent.`,
              )}
            </span>
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
                "可扩大时间范围或调整筛选条件。",
                "Try a wider time range or different filters.",
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
                <div
                  key={`${entry.instanceId}-${entry.sessionId}-${entry.sequence}`}
                  className="ag-runtime-log-row"
                  data-sequence={entry.sequence}
                >
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
                    onClick={() =>
                      void copy(
                        logCopy(entry),
                        `${entry.sessionId}-${entry.sequence}`,
                      )
                    }
                  >
                    {copiedValue === `${entry.sessionId}-${entry.sequence}`
                      ? text("已复制", "Copied")
                      : text("复制", "Copy")}
                  </Button>
                  <div className="ag-runtime-log-detail">
                    {logText(
                      [
                        entry.method,
                        entry.requestClass,
                        entry.status === undefined
                          ? ""
                          : `status=${entry.status}`,
                        entry.durationMs === undefined
                          ? ""
                          : `${entry.durationMs}ms`,
                        entry.jobId ? `job=${entry.jobId}` : "",
                        entry.attempt === undefined
                          ? ""
                          : `attempt=${entry.attempt}`,
                        `Request ${entry.requestId || "—"}`,
                        `Trace ${entry.traceId || "—"}`,
                      ]
                        .filter(Boolean)
                        .join(" · "),
                      MAX_DISPLAY_CHARS,
                    )}
                  </div>
                </div>
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
