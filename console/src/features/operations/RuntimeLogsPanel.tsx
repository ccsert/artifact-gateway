import { useCallback, useEffect, useRef, useState } from "react";
import {
  CopyOutlined,
  InfoCircleOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, DatePicker, Input, Select, Space, Tag } from "antd";
import type { Dayjs } from "dayjs";
import { useSearchParams } from "react-router-dom";
import { listRuntimeLogs } from "../../client";
import type {
  ListRuntimeLogsData,
  RuntimeLogEntry,
  RuntimeLogPage,
} from "../../client";
import { Card, CardHeader, Pagination } from "../../components/ui/Layout";
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
  const [page, setPage] = useState<RuntimeLogPage | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const requestSequence = useRef(0);
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

  const load = useCallback(
    async (beforeSequence?: number, append = false) => {
      const sequence = ++requestSequence.current;
      setLoading(true);
      setError(null);
      try {
        const result = await listRuntimeLogs({
          query: { ...query, beforeSequence },
        });
        if (result.error) throw result.error;
        if (sequence !== requestSequence.current) return;
        const nextPage = result.data ?? null;
        setPage((current) =>
          append && current && nextPage
            ? { ...nextPage, items: [...current.items, ...nextPage.items] }
            : nextPage,
        );
      } catch (nextError) {
        if (sequence !== requestSequence.current) return;
        setError(nextError);
      } finally {
        if (sequence === requestSequence.current) setLoading(false);
      }
    },
    [query],
  );

  useEffect(() => {
    setPage(null);
    void load();
    return () => {
      requestSequence.current += 1;
    };
  }, [load]);

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
          <ErrorBanner
            error={error}
            tone={page ? "warning" : "error"}
            onRetry={() => void load()}
          />
        </div>
      ) : null}
      {loading && !page ? (
        <div className="p-6">
          <Loading label={text("查询运行日志…", "Querying runtime logs…")} />
        </div>
      ) : null}
      {page ? (
        <div className="border-t border-[var(--ag-border-subtle)]">
          <div className="flex flex-wrap gap-x-3 gap-y-1 px-5 py-3 text-xs text-[var(--ag-content-tertiary)]">
            <span>
              {text("当前进程", "Current process")}: {page.instanceId}
            </span>
            <span>
              {text("会话", "Session")}: {page.sessionId}
            </span>
            <span>
              {text(`${page.items.length} 条日志`, `${page.items.length} logs`)}
            </span>
          </div>
          {page.items.length === 0 ? (
            <EmptyState
              compact
              title={text("当前范围内没有日志", "No logs in this range")}
              hint={text(
                "可扩大时间范围或调整筛选条件。",
                "Try a wider time range or different filters.",
              )}
            />
          ) : (
            <div className="space-y-2 px-5 pb-4">
              {page.items.map((entry) => (
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
              ))}
            </div>
          )}
          <Pagination
            hasMore={Boolean(page.nextSequence)}
            loading={loading}
            label={text("加载更早日志", "Load older logs")}
            onMore={() => {
              if (page.nextSequence) void load(page.nextSequence, true);
            }}
          />
        </div>
      ) : null}
    </Card>
  );
}
