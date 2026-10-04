import { useCallback, useEffect, useRef, useState } from "react";
import {
  CopyOutlined,
  InfoCircleOutlined,
  FilterOutlined,
  DownOutlined,
  UpOutlined,
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
import {
  customRuntimeLogWindow,
  type RuntimeLogRangeError,
  type RuntimeLogTimeRange,
} from "./runtimeLogWindow";
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
  const [timeRange, setTimeRange] = useState<RuntimeLogTimeRange>(3600);
  const [windowError, setWindowError] = useState<RuntimeLogRangeError | null>(
    null,
  );
  const [moreFilters, setMoreFilters] = useState(Boolean(auditTraceId));
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
    setTimeRange(3600);
    setWindowError(null);
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
    const window = timeRange === "custom" ? customRuntimeLogWindow(range) : {};
    if (window.error) {
      setWindowError(window.error);
      return;
    }
    setWindowError(null);
    setQuery({
      ...window.bounds,
      windowSeconds:
        timeRange !== "custom" && timeRange !== 3600 ? timeRange : undefined,
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
      <div className="ag-runtime-log-intro">
        <InfoCircleOutlined aria-hidden="true" />
        <span>
          {text(
            "仅查询当前进程的内存日志，重启后清空。",
            "Search this process’s in-memory logs; restart clears them.",
          )}
        </span>
      </div>
      <div
        className="ag-runtime-log-filters"
        role="search"
        aria-label={text("日志筛选", "Log filters")}
      >
        <div className="ag-runtime-log-filter-main">
          <FilterField
            label={text("关键词", "Keyword")}
            className="ag-runtime-log-keyword"
          >
            <Input
              prefix={<SearchOutlined />}
              value={keyword}
              allowClear
              placeholder={text("搜索日志内容", "Search log content")}
              onChange={(event) => setKeyword(event.target.value)}
              onPressEnter={search}
            />
          </FilterField>
          <FilterField label={text("时间范围", "Time range")}>
            <Select
              virtual={false}
              className="w-full"
              aria-label={text("时间范围", "Time range")}
              value={timeRange}
              onChange={(value) => {
                setTimeRange(value);
                setWindowError(null);
              }}
              options={[
                { value: 900, label: text("最近 15 分钟", "Last 15 minutes") },
                { value: 3600, label: text("最近 1 小时", "Last hour") },
                { value: 86400, label: text("最近 24 小时", "Last 24 hours") },
                { value: "custom", label: text("自定义", "Custom") },
              ]}
            />
          </FilterField>
          <FilterField label={text("级别", "Level")}>
            <Select
              virtual={false}
              className="w-full"
              aria-label={text("级别", "Level")}
              value={level}
              onChange={setLevel}
              options={[
                { value: "", label: text("全部级别", "All levels") },
                ...["ERROR", "WARN", "INFO", "DEBUG"].map((value) => ({
                  value,
                  label: value,
                })),
              ]}
            />
          </FilterField>
        </div>
        {timeRange === "custom" ? (
          <div className="ag-runtime-log-custom-range">
            <FilterField
              label={text("起止时间（本地时区）", "Start and end (local time)")}
            >
              <DatePicker.RangePicker
                className="w-full"
                showTime
                format="YYYY-MM-DD HH:mm:ss"
                classNames={{ popup: { root: "ag-runtime-log-calendar" } }}
                value={range}
                status={windowError ? "error" : undefined}
                placeholder={[
                  text("开始日期", "Start date"),
                  text("结束日期", "End date"),
                ]}
                onChange={(value) => {
                  setRange(value as [Dayjs, Dayjs] | null);
                  setWindowError(null);
                }}
                allowClear
              />
            </FilterField>
            <span className="ag-runtime-log-hint">
              {text(
                "最多 24 小时，结束时间不能超过当前时间 5 分钟。",
                "At most 24 hours; end no later than 5 minutes from now.",
              )}
            </span>
          </div>
        ) : null}
        <div className="ag-runtime-log-filter-secondary">
          <FilterField label={text("组件", "Component")}>
            <AutoComplete
              value={component}
              onChange={setComponent}
              options={
                page?.source?.components.map((value) => ({ value })) ?? []
              }
              placeholder={text(
                "全部组件，可输入精确名称",
                "All components; enter exact name",
              )}
              className="w-full"
            />
          </FilterField>
          <FilterField label="Request ID">
            <Input
              value={requestId}
              allowClear
              placeholder={text("按请求定位", "Find a request")}
              onChange={(event) => setRequestId(event.target.value)}
              onPressEnter={search}
            />
          </FilterField>
          <Button
            className="ag-runtime-log-more"
            icon={<FilterOutlined />}
            aria-expanded={moreFilters}
            aria-controls="runtime-log-more-filters"
            onClick={() => setMoreFilters(!moreFilters)}
          >
            {text("更多筛选", "More filters")}
            {instanceId || traceId
              ? " · " + (Number(Boolean(instanceId)) + Number(Boolean(traceId)))
              : ""}
            {moreFilters ? <UpOutlined /> : <DownOutlined />}
          </Button>
        </div>
        {moreFilters ? (
          <div
            id="runtime-log-more-filters"
            className="ag-runtime-log-filter-more"
          >
            <FilterField label={text("节点", "Instance")}>
              <Input
                value={instanceId}
                allowClear
                placeholder={text("当前进程", "Current process")}
                onChange={(event) => setInstanceId(event.target.value)}
                onPressEnter={search}
              />
            </FilterField>
            <FilterField label="Trace ID">
              <Input
                value={traceId}
                allowClear
                placeholder={text("按链路关联", "Correlate a trace")}
                onChange={(event) => setTraceId(event.target.value)}
                onPressEnter={search}
              />
            </FilterField>
          </div>
        ) : null}
        {windowError ? (
          <Alert
            type="warning"
            showIcon
            title={text("请调整时间范围", "Adjust the time range")}
            description={text(
              {
                required: "请选择完整的开始和结束时间。",
                order: "开始时间不能晚于结束时间。",
                duration: "时间范围不能超过 24 小时。",
                future: "结束时间不能超过当前时间 5 分钟。",
              }[windowError],
              {
                required: "Choose both start and end times.",
                order: "Start must not be after end.",
                duration: "Time range cannot exceed 24 hours.",
                future: "End cannot be more than 5 minutes from now.",
              }[windowError],
            )}
          />
        ) : null}
        <div className="ag-runtime-log-filter-actions">
          <span className="ag-runtime-log-hint">
            {timeRange === "custom"
              ? text(
                  "固定时间快照；选择最近范围可跟随新日志。",
                  "Fixed snapshot; choose a recent range to follow new logs.",
                )
              : text(
                  "最近范围随每次查询和跟随自动更新。",
                  "Recent ranges update on every query and follow read.",
                )}
          </span>
          <Space wrap>
            <Button onClick={clearFilters}>
              {text("清除筛选", "Clear filters")}
            </Button>
            <Button
              icon={<ReloadOutlined />}
              loading={loading}
              onClick={refresh}
            >
              {text("刷新快照", "Refresh snapshot")}
            </Button>
            <Button icon={<SearchOutlined />} type="primary" onClick={search}>
              {text("查询", "Search")}
            </Button>
          </Space>
        </div>
      </div>
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
                        entry.route,
                        entry.requestClass,
                        entry.status === undefined
                          ? ""
                          : `status=${entry.status}`,
                        entry.durationMs === undefined
                          ? ""
                          : `${entry.durationMs}ms`,
                        entry.errorCode ? `errorCode=${entry.errorCode}` : "",
                        entry.phase ? `phase=${entry.phase}` : "",
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

function runtimeLogError(
  error: unknown,
  text: (zh: string, en: string) => string,
): unknown {
  // Let the shared banner retain its endpoint/version mismatch guidance.
  if (
    typeof error === "string" &&
    /(?:^|\b)404(?:\b|$).*not found/i.test(error.trim())
  )
    return error;
  const problem =
    error && typeof error === "object"
      ? (error as { code?: string; message?: string })
      : {};
  const messages: Record<string, [string, string]> = {
    invalid_time_window: [
      "时间范围无效：最多 24 小时，结束时间不能超过当前时间 5 分钟。请调整范围后查询。",
      "Invalid time range: at most 24 hours, ending no later than 5 minutes from now. Adjust the range and search again.",
    ],
    log_buffer_unavailable: [
      "当前节点未启用内存日志查询，请检查日志缓冲配置。",
      "Memory log queries are disabled on this node. Check the buffer configuration.",
    ],
    log_source_identity_unavailable: [
      "当前日志来源信息不可用，请检查节点配置后重试。",
      "Log source identity is unavailable. Check the node configuration and retry.",
    ],
    remote_log_query_unavailable: [
      "当前入口只能查询所连接进程，请清除节点筛选后重试。",
      "This endpoint queries the connected process only. Clear the instance filter and retry.",
    ],
    log_cursor_scope_changed: [
      "进程或会话已变化，请刷新新快照。",
      "Process or session changed; refresh the snapshot.",
    ],
    invalid_cursor: [
      "查询游标已失效或筛选发生变化，请刷新快照。",
      "The cursor is invalid or filters changed. Refresh the snapshot.",
    ],
    log_query_timeout: [
      "日志查询超时，请稍后重试。",
      "Log query timed out. Retry shortly.",
    ],
    access_denied: [
      "当前身份没有查询运行日志的权限。",
      "This identity cannot query runtime logs.",
    ],
    password_change_required: [
      "需要先更新密码，之后再查询运行日志。",
      "Update your password before querying runtime logs.",
    ],
    invalid_filter: [
      "筛选内容过长或包含控制字符，请调整后查询。",
      "A filter is too long or contains control characters. Adjust it and search again.",
    ],
  };
  const translation = problem.code ? messages[problem.code] : undefined;
  if (translation) return { ...problem, message: text(...translation) };
  if (problem.code)
    return {
      ...problem,
      message: text(
        "日志查询失败，请检查连接或访问权限后重试。",
        "Log query failed. Check the connection or permissions and retry.",
      ),
    };
  return {
    ...problem,
    message: text(
      "日志查询失败，请检查连接后重试。",
      "Log query failed. Check the connection and retry.",
    ),
  };
}
