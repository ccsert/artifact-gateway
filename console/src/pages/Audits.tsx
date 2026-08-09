import { useCallback, useEffect, useState } from "react";
import { Button, Collapse, Input, Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ClearOutlined,
  DownloadOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { listAuditPage, listRepositories, listGroups } from "../client";
import type { AuditRecord } from "../client";
import { PageHeader, Card } from "../components/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  isNotFound,
} from "../components/Feedback";
import { StateBadge, FormatBadge } from "../components/Badge";
import { formatBytes, formatDate } from "../lib/format";
import { toCsv, downloadCsv } from "../lib/csv";
import {
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";

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
type AuditTableRow = {
  key: string;
  rowIndex: number;
  record: AuditRecord;
};

function auditOutcomeLabel(value: string): string {
  const labels: Record<string, string> = {
    resolved: "resolved · 已处理",
    failed: "failed · 失败",
    denied: "denied · 拒绝",
    access_denied: "access_denied · 访问拒绝",
    not_found: "not_found · 未找到",
    proxy_denied: "proxy_denied · 代理拒绝",
    upstream_error: "upstream_error · 上游错误",
    storage_error: "storage_error · 存储错误",
  };
  return labels[value] ?? value;
}

function auditOperationLabel(value: string): string {
  const labels: Record<string, string> = {
    get: "GET · 读取",
    head: "HEAD · 探测",
    put: "PUT · 发布",
    post: "POST · 创建",
    delete: "DELETE · 删除",
    grant: "grant · 授权",
  };
  return labels[value] ?? value;
}

function AuditSelect({
  label,
  value,
  placeholder,
  options,
  onChange,
}: {
  label: string;
  value: string;
  placeholder: string;
  options: { value: string; label: string }[];
  onChange: (value: string) => void;
}) {
  return (
    <FilterField label={label}>
      <Select
        className="w-full min-w-[170px]"
        allowClear
        showSearch={{ optionFilterProp: "label" }}
        value={value || undefined}
        placeholder={placeholder}
        options={options}
        onChange={(next) => onChange(next ?? "")}
      />
    </FilterField>
  );
}

export function AuditsPage() {
  const { locale, text } = usePreferences();
  const [records, setRecords] = useState<AuditRecord[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [repository, setRepository] = useState("");
  const [group, setGroup] = useState("");
  const [outcome, setOutcome] = useState("");
  const [format, setFormat] = useState("");
  const [operation, setOperation] = useState("");
  const [actor, setActor] = useState("");
  const [limit, setLimit] = useState(100);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [repoOptions, setRepoOptions] = useState<string[]>([]);
  const [groupOptions, setGroupOptions] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [pageTokens, setPageTokens] = useState<string[]>([""]);
  const [nextPageToken, setNextPageToken] = useState<string | null>(null);

  useEffect(() => {
    void listRepositories({ query: { pageSize: 200 } }).then(({ data }) =>
      setRepoOptions((data?.items ?? []).map((r) => r.name)),
    );
    void listGroups({ query: { pageSize: 200 } }).then(({ data }) =>
      setGroupOptions((data?.items ?? []).map((g) => g.name)),
    );
  }, []);

  const load = useCallback(
    async (token = "", pageNumber = 1) => {
      setError(null);
      setRecords(null);
      setExpanded(null);
      setPage(pageNumber);
      setPageTokens((current) =>
        pageNumber === 1
          ? [token]
          : [...current.slice(0, pageNumber - 1), token],
      );
      const { data, error: err } = await listAuditPage({
        query: {
          repository: repository || undefined,
          group: group || undefined,
          outcome: outcome || undefined,
          format: format || undefined,
          operation: operation || undefined,
          actor: actor || undefined,
          from: from ? new Date(from).toISOString() : undefined,
          to: to ? new Date(to).toISOString() : undefined,
          pageSize: limit,
          pageToken: token || undefined,
        },
      });
      if (err) {
        setError(err);
        return;
      }
      setRecords(data?.items ?? []);
      setNextPageToken(data?.nextPageToken ?? null);
    },
    [repository, group, outcome, format, operation, actor, limit, from, to],
  );

  useEffect(() => {
    void load();
  }, [load]);
  const filtered = records ?? [];
  const currentPage = page;
  const pageRecords = filtered;
  const tableRows: AuditTableRow[] = pageRecords.map((record, index) => ({
    key: String(index),
    rowIndex: index,
    record,
  }));
  const outcomeOptions = Array.from(
    new Set(filtered.map((a) => a.outcome).filter(Boolean)),
  ).map((value) => ({ value, label: auditOutcomeLabel(value) }));
  const formatOptions = Array.from(
    new Set(
      filtered
        .map((a) => a.format)
        .filter((value): value is string => Boolean(value)),
    ),
  ).map((value) => ({ value, label: value.toUpperCase() }));
  const operationOptions = Array.from(
    new Set(
      filtered
        .map((a) => a.operation)
        .filter((value): value is string => Boolean(value)),
    ),
  )
    .sort()
    .map((value) => ({ value, label: auditOperationLabel(value) }));
  const actorOptions = Array.from(
    new Set(
      filtered
        .map((a) => a.actor)
        .filter((value): value is string => Boolean(value)),
    ),
  )
    .sort()
    .map((value) => ({ value, label: value }));
  const failedCount = filtered.filter(
    (record) => record.outcome === "failed" || (record.status ?? 0) >= 400,
  ).length;
  const deniedCount = filtered.filter(
    (record) =>
      record.outcome === "denied" || record.outcome === "access_denied",
  ).length;
  const actorCount = new Set(
    filtered.map((record) => record.actor).filter(Boolean),
  ).size;
  const hasFilters = Boolean(
    repository ||
    group ||
    outcome ||
    format ||
    operation ||
    actor ||
    from ||
    to,
  );
  const clearFilters = () => {
    setRepository("");
    setGroup("");
    setOutcome("");
    setFormat("");
    setOperation("");
    setActor("");
    setFrom("");
    setTo("");
  };
  const columns: ColumnsType<AuditTableRow> = [
    {
      title: text("时间", "Time"),
      key: "occurredAt",
      width: 180,
      render: (_, row) => (
        <span className="whitespace-nowrap font-mono text-xs text-zinc-400">
          {formatDate(row.record.occurredAt, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Operation"),
      key: "operation",
      width: 190,
      render: (_, row) => (
        <span
          className="block max-w-52 truncate text-xs text-zinc-200"
          title={row.record.operation}
        >
          {row.record.operation
            ? auditOperationLabel(row.record.operation)
            : "—"}
        </span>
      ),
    },
    {
      title: text("结果", "Outcome"),
      key: "outcome",
      width: 170,
      render: (_, row) => <StateBadge state={row.record.outcome} />,
    },
    {
      title: text("仓库/分组", "Repository / group"),
      key: "repository",
      width: 180,
      render: (_, row) => (
        <span className="block max-w-40 truncate font-mono text-xs text-zinc-400">
          {row.record.repository ?? row.record.groupName ?? "—"}
        </span>
      ),
    },
    {
      title: text("格式", "Format"),
      key: "format",
      width: 110,
      render: (_, row) =>
        row.record.format ? <FormatBadge format={row.record.format} /> : "—",
    },
    {
      title: text("状态", "Status"),
      key: "status",
      width: 90,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {row.record.status ?? "—"}
        </span>
      ),
    },
    {
      title: text("流量", "Traffic"),
      key: "bytes",
      width: 110,
      render: (_, row) => (
        <span className="text-xs text-zinc-400">
          {formatBytes(row.record.bytes)}
        </span>
      ),
    },
    {
      title: "Actor",
      key: "actor",
      width: 170,
      render: (_, row) => (
        <span
          className="block max-w-32 truncate text-xs text-zinc-500"
          title={row.record.actor}
        >
          {row.record.actor ?? "—"}
        </span>
      ),
    },
  ];

  const expandedRowRender = ({ record }: AuditTableRow) => (
    <div className="grid grid-cols-3 gap-x-8 gap-y-2 px-2 py-1 text-xs">
      {(
        [
          [text("资源", "Resource"), record.resource],
          [text("表示", "Representation"), record.representation],
          [text("成员", "Member"), record.memberName],
          [text("成员类型", "Member type"), record.memberType],
          [text("上游主机", "Upstream host"), record.upstreamHost],
          [text("缓存", "Cache"), record.cacheDisposition],
          [
            text("授权来源", "Authorization source"),
            record.authorizationSource,
          ],
          [
            text("授权原因", "Authorization reason"),
            record.authorizationReason,
          ],
          ["Request ID", record.requestId],
          ["Trace ID", record.traceId],
        ] as const
      ).map(([label, value]) => (
        <div key={label} className="flex min-w-0 gap-2">
          <span className="w-20 shrink-0 text-zinc-600">{label}</span>
          <span className="min-w-0 break-all font-mono text-zinc-400">
            {value ?? "—"}
          </span>
        </div>
      ))}
    </div>
  );

  return (
    <div>
      <PageHeader
        title={text("审计日志", "Audit log")}
        description={text(
          "网关访问与授权决策记录（最新在前）",
          "Gateway access and authorization decisions, newest first",
        )}
      />
      <MetricStrip
        items={[
          {
            label: text("当前记录", "Current records"),
            value: records ? filtered.length : "—",
            hint: text(`当前页最多 ${limit} 条`, `Up to ${limit} on this page`),
          },
          {
            label: text("失败请求", "Failed requests"),
            value: failedCount,
            hint: failedCount
              ? text("建议优先检查失败原因", "Review failure details first")
              : text("当前窗口未发现失败", "No failures in this window"),
            tone: failedCount ? "danger" : "success",
          },
          {
            label: text("拒绝访问", "Denied access"),
            value: deniedCount,
            hint: text(`${actorCount} 个操作主体`, `${actorCount} actors`),
            tone: deniedCount ? "warning" : "default",
          },
        ]}
      />
      <Card className="mt-4">
        <FilterBar
          actions={
            <Space size="small">
              <Button
                icon={<ClearOutlined />}
                disabled={!hasFilters}
                onClick={clearFilters}
              >
                {text("清除", "Clear")}
              </Button>
              <Button icon={<ReloadOutlined />} onClick={() => void load()}>
                {text("刷新", "Refresh")}
              </Button>
              <Button
                icon={<DownloadOutlined />}
                disabled={!filtered.length}
                onClick={() => {
                  const rows = filtered.map((a) => [
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
                      locale === "en-US"
                        ? AUDIT_CSV_COLUMNS_EN
                        : AUDIT_CSV_COLUMNS_ZH,
                      rows,
                    ),
                  );
                }}
              >
                {text("导出当前页 CSV", "Export current page CSV")}
              </Button>
            </Space>
          }
        >
          <AuditSelect
            label={text("仓库", "Repository")}
            value={repository}
            placeholder={text("全部仓库", "All repositories")}
            options={repoOptions.map((value) => ({ value, label: value }))}
            onChange={setRepository}
          />
          <AuditSelect
            label={text("结果", "Outcome")}
            value={outcome}
            placeholder={text("全部结果", "All outcomes")}
            options={outcomeOptions}
            onChange={setOutcome}
          />
          <FilterField label={text("加载窗口", "Load window")}>
            <Select
              className="min-w-[150px]"
              value={String(limit)}
              options={[50, 100, 200].map((value) => ({
                value: String(value),
                label: text(`最近 ${value} 条`, `Latest ${value}`),
              }))}
              onChange={(value) => setLimit(Number(value))}
            />
          </FilterField>
          <FilterField label={text("起始时间", "From")}>
            <Input
              type="datetime-local"
              value={from}
              onChange={(event) => setFrom(event.target.value)}
            />
          </FilterField>
          <FilterField label={text("结束时间", "To")}>
            <Input
              type="datetime-local"
              value={to}
              onChange={(event) => setTo(event.target.value)}
            />
          </FilterField>
        </FilterBar>
        <Collapse
          ghost
          className="border-t border-zinc-800/70"
          items={[
            {
              key: "advanced",
              label: (
                <span className="text-xs text-zinc-400">
                  {text("高级筛选", "Advanced filters")}{" "}
                  <span className="ml-2 text-zinc-600">
                    {text(
                      "操作、访问主体、分组、格式",
                      "Operation, actor, group, and format",
                    )}
                  </span>
                </span>
              ),
              children: (
                <FilterBar>
                  <AuditSelect
                    label={text("操作类型", "Operation")}
                    value={operation}
                    placeholder={text("全部操作类型", "All operations")}
                    options={operationOptions}
                    onChange={setOperation}
                  />
                  <AuditSelect
                    label={text("访问主体", "Actor")}
                    value={actor}
                    placeholder={text("全部访问主体", "All actors")}
                    options={actorOptions}
                    onChange={setActor}
                  />
                  <AuditSelect
                    label={text("所属分组", "Group")}
                    value={group}
                    placeholder={text("全部分组", "All groups")}
                    options={groupOptions.map((value) => ({
                      value,
                      label: value,
                    }))}
                    onChange={setGroup}
                  />
                  <AuditSelect
                    label={text("制品格式", "Artifact format")}
                    value={format}
                    placeholder={text("全部格式", "All formats")}
                    options={formatOptions}
                    onChange={setFormat}
                  />
                </FilterBar>
              ),
            },
          ]}
        />
      </Card>
      {error !== null ? (
        isNotFound(error) ? (
          <Card className="mt-4">
            <EmptyState
              title={text("审计功能未启用", "Audit log is unavailable")}
              hint={text(
                "当前后端构建尚未挂载审计端点（返回 404）",
                "The current backend does not expose the audit endpoint (404)",
              )}
            />
          </Card>
        ) : (
          <div className="mt-4">
            <ErrorBanner error={error} onRetry={load} />
          </div>
        )
      ) : !records ? (
        <Loading />
      ) : filtered.length === 0 ? (
        <Card className="mt-4">
          <EmptyState
            title={text("没有匹配的审计记录", "No matching audit records")}
            hint={text(
              "尝试清除筛选或扩大加载窗口",
              "Clear filters or expand the load window",
            )}
          />
        </Card>
      ) : (
        <Card className="mt-4">
          <Table<AuditTableRow>
            className="ag-console-table"
            rowKey="key"
            size="middle"
            dataSource={tableRows}
            columns={columns}
            pagination={false}
            scroll={{
              x: 1200,
              y: "clamp(240px, calc(100vh - 610px), 520px)",
            }}
            rowClassName="cursor-pointer"
            expandable={{
              expandedRowKeys: expanded === null ? [] : [expanded],
              expandedRowRender,
              expandRowByClick: true,
              onExpandedRowsChange: (keys) => {
                const key = keys[keys.length - 1];
                setExpanded(key === undefined ? null : String(key));
              },
            }}
          />
          <div className="flex items-center justify-between gap-3 border-t border-zinc-800/60 px-4 py-3 text-xs text-zinc-500">
            <span>
              {text(
                `第 ${currentPage} 页 · 当前页 ${pageRecords.length} 条`,
                `Page ${currentPage} · ${pageRecords.length} records`,
              )}
            </span>
            <Space size="small">
              <Button
                disabled={currentPage <= 1 || !records}
                onClick={() => {
                  const previousPage = Math.max(1, currentPage - 1);
                  void load(pageTokens[previousPage - 1] ?? "", previousPage);
                }}
              >
                {text("上一页", "Previous")}
              </Button>
              <Button
                disabled={!nextPageToken || !records}
                onClick={() =>
                  nextPageToken && void load(nextPageToken, currentPage + 1)
                }
              >
                {text("下一页", "Next")}
              </Button>
            </Space>
          </div>
        </Card>
      )}
    </div>
  );
}
