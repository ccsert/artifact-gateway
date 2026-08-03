import { Fragment, useCallback, useEffect, useState } from 'react';
import { Button, Select, Space } from 'antd';
import { ClearOutlined, DownloadOutlined, ReloadOutlined } from '@ant-design/icons';
import { listAudits, listRepositories, listGroups } from '../client';
import type { AuditRecord } from '../client';
import { PageHeader, Card, DataTable, StatCard } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState, isNotFound } from '../components/Feedback';
import { StateBadge, FormatBadge } from '../components/Badge';
import { formatBytes, formatDate } from '../lib/format';
import { toCsv, downloadCsv } from '../lib/csv';

const AUDIT_CSV_COLUMNS = [
  '时间',
  '操作',
  '结果',
  '仓库',
  '分组',
  '格式',
  '状态',
  '流量',
  'Actor',
  '资源',
  '表示',
  '成员',
  '成员类型',
  '上游主机',
  '缓存',
  '授权来源',
  '授权原因',
  'Request ID',
  'Trace ID',
];

const AUDIT_PAGE_SIZE = 50;

function auditOutcomeLabel(value: string): string {
  const labels: Record<string, string> = {
    resolved: 'resolved · 已处理',
    failed: 'failed · 失败',
    denied: 'denied · 拒绝',
    access_denied: 'access_denied · 访问拒绝',
    not_found: 'not_found · 未找到',
    proxy_denied: 'proxy_denied · 代理拒绝',
    upstream_error: 'upstream_error · 上游错误',
    storage_error: 'storage_error · 存储错误',
  };
  return labels[value] ?? value;
}

function auditOperationLabel(value: string): string {
  const labels: Record<string, string> = {
    get: 'GET · 读取',
    head: 'HEAD · 探测',
    put: 'PUT · 发布',
    post: 'POST · 创建',
    delete: 'DELETE · 删除',
    grant: 'grant · 授权',
  };
  return labels[value] ?? value;
}

function AuditFilter({
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
    <label className="block">
      <span className="mb-1.5 block text-[11px] font-medium text-zinc-400">{label}</span>
      <Select
        className="w-full"
        allowClear
        showSearch={{ optionFilterProp: 'label' }}
        value={value || undefined}
        placeholder={placeholder}
        options={options}
        onChange={(next) => onChange(next ?? '')}
      />
    </label>
  );
}

export function AuditsPage() {
  const [records, setRecords] = useState<AuditRecord[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [repository, setRepository] = useState('');
  const [group, setGroup] = useState('');
  const [outcome, setOutcome] = useState('');
  const [format, setFormat] = useState('');
  const [operation, setOperation] = useState('');
  const [actor, setActor] = useState('');
  const [limit, setLimit] = useState(100);
  const [repoOptions, setRepoOptions] = useState<string[]>([]);
  const [groupOptions, setGroupOptions] = useState<string[]>([]);
  const [expanded, setExpanded] = useState<number | null>(null);
  const [page, setPage] = useState(1);

  useEffect(() => {
    void listRepositories({ query: { pageSize: 200 } }).then(({ data }) => {
      setRepoOptions((data?.items ?? []).map((r) => r.name));
    });
    void listGroups({ query: { pageSize: 200 } }).then(({ data }) => {
      setGroupOptions((data?.items ?? []).map((g) => g.name));
    });
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

  // Filters are evaluated by the API, so results remain correct beyond the
  // current page/window. Keep this alias for counters, table pagination, and export.
  const filtered = records ?? [];
  const totalPages = Math.max(1, Math.ceil(filtered.length / AUDIT_PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageStart = (currentPage - 1) * AUDIT_PAGE_SIZE;
  const pageRecords = filtered.slice(pageStart, pageStart + AUDIT_PAGE_SIZE);
  const outcomeOptions = Array.from(new Set((records ?? []).map((a) => a.outcome).filter(Boolean)));
  const formatOptions = Array.from(new Set((records ?? []).map((a) => a.format).filter((f): f is string => !!f)));
  const operationOptions = Array.from(new Set((records ?? []).map((a) => a.operation).filter((value): value is string => !!value))).sort();
  const actorOptions = Array.from(new Set((records ?? []).map((a) => a.actor).filter((value): value is string => !!value))).sort();
  const failedCount = (records ?? []).filter((record) => record.outcome === 'failed' || (record.status ?? 0) >= 400).length;
  const deniedCount = (records ?? []).filter((record) => record.outcome === 'denied').length;
  const actorCount = new Set((records ?? []).map((record) => record.actor).filter(Boolean)).size;

  useEffect(() => {
    setPage(1);
    setExpanded(null);
  }, [repository, group, outcome, format, operation, actor, limit]);

  return (
    <div>
      <PageHeader title="审计日志" description="网关访问与授权决策记录（最新在前）" />
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <StatCard label="当前记录" value={records?.length ?? '—'} sub={`当前检索窗口 ${limit} 条`} />
        <StatCard label="失败请求" value={failedCount} sub={failedCount ? '建议优先检查失败原因' : '当前窗口未发现失败'} />
        <StatCard label="拒绝访问" value={deniedCount} sub={`${actorCount} 个操作主体`} />
      </div>
      <div className="mb-4 grid grid-cols-4 items-end gap-3">
        <AuditFilter label="仓库" value={repository} placeholder="全部仓库" options={repoOptions.map((value) => ({ value, label: value }))} onChange={setRepository} />
        <AuditFilter label="操作类型" value={operation} placeholder="全部操作类型" options={operationOptions.map((value) => ({ value, label: auditOperationLabel(value) }))} onChange={setOperation} />
        <AuditFilter label="访问主体" value={actor} placeholder="全部访问主体" options={actorOptions.map((value) => ({ value, label: value }))} onChange={setActor} />
        <AuditFilter label="所属分组" value={group} placeholder="全部分组" options={groupOptions.map((value) => ({ value, label: value }))} onChange={setGroup} />
        <AuditFilter label="结果" value={outcome} placeholder="全部结果" options={outcomeOptions.map((value) => ({ value, label: auditOutcomeLabel(value) }))} onChange={setOutcome} />
        <AuditFilter label="制品格式" value={format} placeholder="全部格式" options={formatOptions.map((value) => ({ value, label: value.toUpperCase() }))} onChange={setFormat} />
        <label className="block">
          <span className="mb-1.5 block text-[11px] font-medium text-zinc-400">加载窗口</span>
          <Select
            className="w-full"
            value={String(limit)}
            options={[50, 100, 200, 500].map((value) => ({ value: String(value), label: `最近 ${value} 条` }))}
            onChange={(value) => setLimit(Number(value))}
          />
        </label>
        <Space className="flex flex-wrap justify-end" size="small">
          {(repository || group || outcome || format || operation || actor) && (
            <Button type="text" icon={<ClearOutlined />} onClick={() => { setRepository(''); setGroup(''); setOutcome(''); setFormat(''); setOperation(''); setActor(''); }}>清除筛选</Button>
          )}
          <Button icon={<ReloadOutlined />} onClick={() => void load()}>刷新</Button>
          <Button
            icon={<DownloadOutlined />}
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
              downloadCsv(`audits-${new Date().toISOString().slice(0, 19)}.csv`, toCsv(AUDIT_CSV_COLUMNS, rows));
            }}
            disabled={!filtered.length}
          >
            导出 CSV
          </Button>
        </Space>
      </div>
      {error !== null ? (
        isNotFound(error) ? (
          <Card>
            <EmptyState title="审计功能未启用" hint="当前后端构建尚未挂载审计端点（返回 404）" />
          </Card>
        ) : (
          <ErrorBanner error={error} onRetry={load} />
        )
      ) : !records ? (
        <Loading />
      ) : filtered.length === 0 ? (
        <Card>
          <EmptyState title="没有匹配的审计记录" />
        </Card>
      ) : (
        <Card>
          <DataTable columns={['时间', '操作', '结果', '仓库/分组', '格式', '状态', '流量', 'Actor', '']}>
            {pageRecords.map((a, i) => {
              const rowIndex = pageStart + i;
              return (
              <Fragment key={`${a.requestId ?? 'audit'}-${rowIndex}`}>
                <tr
                  className="cursor-pointer hover:bg-zinc-800/30"
                  onClick={() => setExpanded(expanded === rowIndex ? null : rowIndex)}
                >
                  <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                    {formatDate(a.occurredAt)}
                  </td>
                  <td className="max-w-52 truncate px-4 py-2.5 text-xs text-zinc-200" title={a.operation}>
                    {a.operation ?? '—'}
                  </td>
                  <td className="px-4 py-2.5">
                    <StateBadge state={a.outcome} />
                  </td>
                  <td className="max-w-40 truncate px-4 py-2.5 font-mono text-xs text-zinc-400">
                    {a.repository ?? a.groupName ?? '—'}
                  </td>
                  <td className="px-4 py-2.5">{a.format ? <FormatBadge format={a.format} /> : '—'}</td>
                  <td className="px-4 py-2.5 font-mono text-xs text-zinc-400">{a.status ?? '—'}</td>
                  <td className="px-4 py-2.5 text-xs text-zinc-400">{formatBytes(a.bytes)}</td>
                  <td className="max-w-32 truncate px-4 py-2.5 text-xs text-zinc-500" title={a.actor}>
                    {a.actor ?? '—'}
                  </td>
                  <td className="px-4 py-2.5 text-right text-xs text-zinc-600">{expanded === rowIndex ? '▲' : '▼'}</td>
                </tr>
                {expanded === rowIndex && (
                  <tr className="bg-zinc-900/60">
                    <td colSpan={9} className="px-4 py-3">
                      <div className="grid grid-cols-2 gap-x-8 gap-y-1.5 text-xs md:grid-cols-3">
                        {(
                          [
                            ['资源', a.resource],
                            ['表示', a.representation],
                            ['成员', a.memberName],
                            ['成员类型', a.memberType],
                            ['上游主机', a.upstreamHost],
                            ['缓存', a.cacheDisposition],
                            ['授权来源', a.authorizationSource],
                            ['授权原因', a.authorizationReason],
                            ['Request ID', a.requestId],
                            ['Trace ID', a.traceId],
                          ] as const
                        ).map(([label, value]) => (
                          <div key={label} className="flex gap-2">
                            <span className="w-20 shrink-0 text-zinc-600">{label}</span>
                            <span className="break-all font-mono text-zinc-400">{value ?? '—'}</span>
                          </div>
                        ))}
                      </div>
                    </td>
                  </tr>
                )}
              </Fragment>
              );
            })}
          </DataTable>
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-zinc-800/60 px-4 py-3 text-xs text-zinc-500">
            <span>
              第 {currentPage} / {totalPages} 页 · 显示 {pageRecords.length} / {filtered.length} 条
            </span>
            <Space size="small">
              <Button disabled={currentPage <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))}>上一页</Button>
              <Button disabled={currentPage >= totalPages} onClick={() => setPage((p) => Math.min(totalPages, p + 1))}>下一页</Button>
            </Space>
          </div>
        </Card>
      )}
    </div>
  );
}
