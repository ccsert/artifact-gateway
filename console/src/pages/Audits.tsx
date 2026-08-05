import { useCallback, useEffect, useState } from "react";
import { Button, Collapse, Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  ClearOutlined,
  DownloadOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { listAudits, listRepositories, listGroups } from "../client";
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

const AUDIT_CSV_COLUMNS = [
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
const AUDIT_PAGE_SIZE = 50;

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
  const [records, setRecords] = useState<AuditRecord[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [repository, setRepository] = useState("");
  const [group, setGroup] = useState("");
  const [outcome, setOutcome] = useState("");
  const [format, setFormat] = useState("");
  const [operation, setOperation] = useState("");
  const [actor, setActor] = useState("");
  const [limit, setLimit] = useState(100);
  const [repoOptions, setRepoOptions] = useState<string[]>([]);
  const [groupOptions, setGroupOptions] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [page, setPage] = useState(1);

  useEffect(() => {
    void listRepositories({ query: { pageSize: 200 } }).then(({ data }) =>
      setRepoOptions((data?.items ?? []).map((r) => r.name)),
    );
    void listGroups({ query: { pageSize: 200 } }).then(({ data }) =>
      setGroupOptions((data?.items ?? []).map((g) => g.name)),
    );
  }, []);

  const load = useCallback(async () => {
    setError(null);
    setRecords(null);
    setExpanded(null);
    setPage(1);
    const { data, error: err } = await listAudits({
      query: {
        repository: repository || undefined,
        group: group || undefined,
        outcome: outcome || undefined,
        format: format || undefined,
        operation: operation || undefined,
        actor: actor || undefined,
        limit,
      },
    });
    if (err) {
      setError(err);
      return;
    }
    setRecords(data ?? []);
  }, [repository, group, outcome, format, operation, actor, limit]);

  useEffect(() => {
    void load();
  }, [load]);
  const filtered = records ?? [];
  const totalPages = Math.max(1, Math.ceil(filtered.length / AUDIT_PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageStart = (currentPage - 1) * AUDIT_PAGE_SIZE;
  const pageRecords = filtered.slice(pageStart, pageStart + AUDIT_PAGE_SIZE);
  const tableRows: AuditTableRow[] = pageRecords.map((record, index) => ({
    key: String(pageStart + index),
    rowIndex: pageStart + index,
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
    repository || group || outcome || format || operation || actor,
  );
  const clearFilters = () => {
    setRepository("");
    setGroup("");
    setOutcome("");
    setFormat("");
    setOperation("");
    setActor("");
  };
  const columns: ColumnsType<AuditTableRow> = [
    {
      title: "时间",
      key: "occurredAt",
      width: 180,
      render: (_, row) => (
        <span className="whitespace-nowrap font-mono text-xs text-zinc-400">
          {formatDate(row.record.occurredAt)}
        </span>
      ),
    },
    {
      title: "操作",
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
      title: "结果",
      key: "outcome",
      width: 170,
      render: (_, row) => <StateBadge state={row.record.outcome} />,
    },
    {
      title: "仓库/分组",
      key: "repository",
      width: 180,
      render: (_, row) => (
        <span className="block max-w-40 truncate font-mono text-xs text-zinc-400">
          {row.record.repository ?? row.record.groupName ?? "—"}
        </span>
      ),
    },
    {
      title: "格式",
      key: "format",
      width: 110,
      render: (_, row) =>
        row.record.format ? <FormatBadge format={row.record.format} /> : "—",
    },
    {
      title: "状态",
      key: "status",
      width: 90,
      render: (_, row) => (
        <span className="font-mono text-xs text-zinc-400">
          {row.record.status ?? "—"}
        </span>
      ),
    },
    {
      title: "流量",
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
          ["资源", record.resource],
          ["表示", record.representation],
          ["成员", record.memberName],
          ["成员类型", record.memberType],
          ["上游主机", record.upstreamHost],
          ["缓存", record.cacheDisposition],
          ["授权来源", record.authorizationSource],
          ["授权原因", record.authorizationReason],
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
        title="审计日志"
        description="网关访问与授权决策记录（最新在前）"
      />
      <MetricStrip
        items={[
          {
            label: "当前记录",
            value: records ? filtered.length : "—",
            hint: `当前检索窗口 ${limit} 条`,
          },
          {
            label: "失败请求",
            value: failedCount,
            hint: failedCount ? "建议优先检查失败原因" : "当前窗口未发现失败",
            tone: failedCount ? "danger" : "success",
          },
          {
            label: "拒绝访问",
            value: deniedCount,
            hint: `${actorCount} 个操作主体`,
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
                清除
              </Button>
              <Button icon={<ReloadOutlined />} onClick={() => void load()}>
                刷新
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
                    toCsv(AUDIT_CSV_COLUMNS, rows),
                  );
                }}
              >
                导出 CSV
              </Button>
            </Space>
          }
        >
          <AuditSelect
            label="仓库"
            value={repository}
            placeholder="全部仓库"
            options={repoOptions.map((value) => ({ value, label: value }))}
            onChange={setRepository}
          />
          <AuditSelect
            label="结果"
            value={outcome}
            placeholder="全部结果"
            options={outcomeOptions}
            onChange={setOutcome}
          />
          <FilterField label="加载窗口">
            <Select
              className="min-w-[150px]"
              value={String(limit)}
              options={[50, 100, 200, 500].map((value) => ({
                value: String(value),
                label: `最近 ${value} 条`,
              }))}
              onChange={(value) => setLimit(Number(value))}
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
                  高级筛选{" "}
                  <span className="ml-2 text-zinc-600">
                    操作、访问主体、分组、格式
                  </span>
                </span>
              ),
              children: (
                <FilterBar>
                  <AuditSelect
                    label="操作类型"
                    value={operation}
                    placeholder="全部操作类型"
                    options={operationOptions}
                    onChange={setOperation}
                  />
                  <AuditSelect
                    label="访问主体"
                    value={actor}
                    placeholder="全部访问主体"
                    options={actorOptions}
                    onChange={setActor}
                  />
                  <AuditSelect
                    label="所属分组"
                    value={group}
                    placeholder="全部分组"
                    options={groupOptions.map((value) => ({
                      value,
                      label: value,
                    }))}
                    onChange={setGroup}
                  />
                  <AuditSelect
                    label="制品格式"
                    value={format}
                    placeholder="全部格式"
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
              title="审计功能未启用"
              hint="当前后端构建尚未挂载审计端点（返回 404）"
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
            title="没有匹配的审计记录"
            hint="尝试清除筛选或扩大加载窗口"
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
            scroll={{ x: 1200 }}
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
              第 {currentPage} / {totalPages} 页 · 显示 {pageRecords.length} /{" "}
              {filtered.length} 条
            </span>
            <Space size="small">
              <Button
                disabled={currentPage <= 1}
                onClick={() => setPage((value) => Math.max(1, value - 1))}
              >
                上一页
              </Button>
              <Button
                disabled={currentPage >= totalPages}
                onClick={() =>
                  setPage((value) => Math.min(totalPages, value + 1))
                }
              >
                下一页
              </Button>
            </Space>
          </div>
        </Card>
      )}
    </div>
  );
}
