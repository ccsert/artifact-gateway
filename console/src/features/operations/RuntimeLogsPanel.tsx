import { useCallback, useEffect, useRef, useState } from "react";
import {
  CopyOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Alert, Button, DatePicker, Input, Select, Space, Tag } from "antd";
import type { Dayjs } from "dayjs";
import { useSearchParams } from "react-router-dom";
import { listRuntimeLogs } from "../../client";
import type {
  ListRuntimeLogsData,
  RuntimeLogEntry,
  RuntimeLogPage,
} from "../../client";
import { Card, CardHeader } from "../../components/ui/Layout";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  FilterBar,
  FilterField,
  useClipboardAction,
} from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

type LogQuery = NonNullable<ListRuntimeLogsData["query"]>;

function logCopy(entry: RuntimeLogEntry) {
  return JSON.stringify(entry, null, 2);
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
  const [cursors, setCursors] = useState<Array<number | undefined>>([
    undefined,
  ]);
  const [pageIndex, setPageIndex] = useState(0);
  const [page, setPage] = useState<RuntimeLogPage | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
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
    setCursors([undefined]);
    setPageIndex(0);
  }, [auditRequestId, auditTraceId]);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await listRuntimeLogs({
        query: { ...query, beforeSequence: cursors[pageIndex] },
      });
      if (result.error) throw result.error;
      setPage(result.data ?? null);
    } catch (nextError) {
      setPage(null);
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, [query, cursors, pageIndex]);

  useEffect(() => {
    void load();
  }, [load]);

  const search = () => {
    setCursors([undefined]);
    setPageIndex(0);
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
      <Alert
        className="mb-4"
        type="info"
        showIcon
        title={text(
          "仅查询当前进程的内存缓冲区",
          "Queries this process's in-memory buffer only",
        )}
        description={text(
          "默认查询最近一小时。重启后记录会消失；其他节点及长期日志需使用部署的采集系统。查询结果会标明实例和运行会话。",
          "The default window is the past hour. Records disappear after restart. Use the deployed collector for other nodes and retained history. Results identify the instance and session.",
        )}
      />
      <FilterBar
        embedded
        actions={
          <Space>
            <Button icon={<SearchOutlined />} type="primary" onClick={search}>
              {text("查询", "Search")}
            </Button>
            <Button
              icon={<ReloadOutlined />}
              loading={loading}
              onClick={() => void load()}
            >
              {text("刷新", "Refresh")}
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
          <Input
            value={component}
            onChange={(event) => setComponent(event.target.value)}
            placeholder="http / worker"
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
          <ErrorBanner error={error} onRetry={load} />
        </div>
      ) : null}
      {loading && !page ? (
        <div className="p-6">
          <Loading label={text("查询运行日志…", "Querying runtime logs…")} />
        </div>
      ) : null}
      {page ? (
        <div className="space-y-3 pt-4">
          <div className="text-xs text-zinc-500">
            {text("本地结果", "Local results")} · {page.instanceId} /{" "}
            {page.sessionId} · {text("第", "Page ")}
            {pageIndex + 1}
            {locale.startsWith("zh") ? " 页" : ""}
          </div>
          {page.items.length === 0 ? (
            <EmptyState
              title={text("当前范围内没有日志", "No logs in this range")}
              hint={text(
                "可扩大时间范围或调整筛选条件。",
                "Try a wider time range or different filters.",
              )}
            />
          ) : (
            page.items.map((entry) => (
              <div
                key={`${entry.sessionId}-${entry.sequence}`}
                className="rounded-lg border border-[var(--ag-border-subtle)] p-3"
              >
                <div className="flex flex-wrap items-center gap-2 text-xs text-zinc-400">
                  <span>{formatDate(entry.time, locale)}</span>
                  <Tag
                    color={
                      entry.level === "ERROR"
                        ? "error"
                        : entry.level === "WARN"
                          ? "warning"
                          : "default"
                    }
                  >
                    {entry.level}
                  </Tag>
                  <span>
                    {entry.component} · {entry.operation}
                  </span>
                  <Button
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
                </div>
                <div className="mt-1 break-words text-sm text-zinc-200">
                  {entry.message}
                </div>
                <div className="mt-2 break-all font-mono text-xs text-zinc-500">
                  Request {entry.requestId || "—"} · Trace{" "}
                  {entry.traceId || "—"}
                </div>
              </div>
            ))
          )}
          <Space>
            <Button
              disabled={pageIndex === 0}
              onClick={() => setPageIndex((current) => current - 1)}
            >
              {text("上一页", "Previous")}
            </Button>
            <Button
              disabled={!page.nextSequence}
              onClick={() => {
                if (!page.nextSequence) return;
                setCursors((current) => [
                  ...current.slice(0, pageIndex + 1),
                  page.nextSequence,
                ]);
                setPageIndex((current) => current + 1);
              }}
            >
              {text("下一页", "Next")}
            </Button>
          </Space>
        </div>
      ) : null}
    </Card>
  );
}
