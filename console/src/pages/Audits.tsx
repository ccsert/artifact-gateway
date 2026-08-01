import { Fragment, useCallback, useEffect, useState } from 'react';
import { listAudits, listRepositories, listGroups } from '../client';
import type { AuditRecord } from '../client';
import { PageHeader, Card, DataTable, inputClass, btnSecondary } from '../components/Layout';
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

export function AuditsPage() {
  const [records, setRecords] = useState<AuditRecord[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [repository, setRepository] = useState('');
  const [group, setGroup] = useState('');
  const [outcome, setOutcome] = useState('');
  const [format, setFormat] = useState('');
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
        limit,
      },
    });
    if (err) {
      setError(err);
      return;
    }
    setRecords(data ?? []);
  }, [repository, group, limit]);

  useEffect(() => {
    void load();
  }, [load]);

  // 客户端二次筛选（后端仅支持 repository/group/limit）
  const filtered = (records ?? []).filter(
    (a) =>
      (!outcome || a.outcome === outcome) &&
      (!format || a.format === format),
  );
  const totalPages = Math.max(1, Math.ceil(filtered.length / AUDIT_PAGE_SIZE));
  const currentPage = Math.min(page, totalPages);
  const pageStart = (currentPage - 1) * AUDIT_PAGE_SIZE;
  const pageRecords = filtered.slice(pageStart, pageStart + AUDIT_PAGE_SIZE);
  const outcomeOptions = Array.from(new Set((records ?? []).map((a) => a.outcome).filter(Boolean)));
  const formatOptions = Array.from(new Set((records ?? []).map((a) => a.format).filter((f): f is string => !!f)));

  useEffect(() => {
    setPage(1);
    setExpanded(null);
  }, [repository, group, outcome, format, limit]);

  return (
    <div>
      <PageHeader title="审计日志" description="网关访问与授权决策记录（最新在前）" />
      <div className="mb-4 flex flex-wrap items-end gap-3">
        <label className="block">
          <span className="mb-1.5 block text-xs font-medium text-zinc-400">仓库</span>
          <input
            className={`${inputClass} w-52`}
            list="audit-repos"
            placeholder="全部仓库"
            value={repository}
            onChange={(e) => setRepository(e.target.value)}
          />
          <datalist id="audit-repos">
            {repoOptions.map((r) => (
              <option key={r} value={r} />
            ))}
          </datalist>
        </label>
        <label className="block">
          <span className="mb-1.5 block text-xs font-medium text-zinc-400">分组</span>
          <input
            className={`${inputClass} w-52`}
            list="audit-groups"
            placeholder="全部分组"
            value={group}
            onChange={(e) => setGroup(e.target.value)}
          />
          <datalist id="audit-groups">
            {groupOptions.map((g) => (
              <option key={g} value={g} />
            ))}
          </datalist>
        </label>
        <label className="block">
          <span className="mb-1.5 block text-xs font-medium text-zinc-400">结果</span>
          <select className={`${inputClass} w-32`} value={outcome} onChange={(e) => setOutcome(e.target.value)}>
            <option value="">全部</option>
            {outcomeOptions.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
        </label>
        <label className="block">
          <span className="mb-1.5 block text-xs font-medium text-zinc-400">格式</span>
          <select className={`${inputClass} w-32`} value={format} onChange={(e) => setFormat(e.target.value)}>
            <option value="">全部</option>
            {formatOptions.map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
        </label>
        <label className="block">
          <span className="mb-1.5 block text-xs font-medium text-zinc-400">条数</span>
          <select className={`${inputClass} w-28`} value={limit} onChange={(e) => setLimit(Number(e.target.value))}>
            {[50, 100, 200, 500].map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <button onClick={() => void load()} className={btnSecondary}>
          刷新
        </button>
        <button
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
          className={btnSecondary}
        >
          导出 CSV
        </button>
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
              <Fragment key={a.requestId ?? rowIndex}>
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
            <div className="flex gap-2">
              <button className={btnSecondary} disabled={currentPage <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))}>上一页</button>
              <button className={btnSecondary} disabled={currentPage >= totalPages} onClick={() => setPage((p) => Math.min(totalPages, p + 1))}>下一页</button>
            </div>
          </div>
        </Card>
      )}
    </div>
  );
}
