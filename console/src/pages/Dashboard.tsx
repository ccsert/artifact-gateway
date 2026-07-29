import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { listRepositories, listGroups, listAudits, getRepositoryCapacity } from '../client';
import type { Repository, Group, AuditRecord } from '../client';
import { PageHeader, StatCard, Card, CardHeader, DataTable } from '../components/Layout';
import { Loading, ErrorBanner, isNotFound } from '../components/Feedback';
import { FormatBadge, StateBadge } from '../components/Badge';
import { formatBytes, formatDate, formatNumber } from '../lib/format';

export function DashboardPage() {
  const [repos, setRepos] = useState<Repository[] | null>(null);
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [audits, setAudits] = useState<AuditRecord[] | null>(null);
  const [totalBytes, setTotalBytes] = useState<number | null>(null);
  const [totalObjects, setTotalObjects] = useState<number | null>(null);
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    try {
      const [r, g, a] = await Promise.all([
        listRepositories({ query: { pageSize: 200 } }),
        listGroups({ query: { pageSize: 200 } }),
        listAudits({ query: { limit: 8 } }),
      ]);
      if (r.error) throw r.error;
      // groups / audits 在当前后端构建中可能未启用（404），降级为空数据
      if (g.error && !isNotFound(g.error)) throw g.error;
      if (a.error && !isNotFound(a.error)) throw a.error;
      const repoList = r.data?.items ?? [];
      setRepos(repoList);
      setGroups(g.data?.items ?? []);
      setAudits(a.data ?? []);

      // 汇总各 active 仓库容量（失败/404 的仓库跳过）
      const activeRepos = repoList.filter((x) => x.state === 'active');
      const caps = await Promise.all(activeRepos.map((x) => getRepositoryCapacity({ path: { repositoryId: x.id } })));
      let bytes = 0;
      let objects = 0;
      let any = false;
      for (const c of caps) {
        if (c.data) {
          bytes += c.data.usedBytes;
          objects += c.data.objectCount;
          any = true;
        }
      }
      setTotalBytes(any ? bytes : null);
      setTotalObjects(any ? objects : null);
    } catch (e) {
      setError(e);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (error) return (
    <div>
      <PageHeader title="总览" />
      <ErrorBanner error={error} onRetry={load} />
    </div>
  );
  if (!repos || !groups || !audits) return <Loading />;

  const formatCount = (f: string) => repos.filter((r) => r.format === f).length;
  const active = repos.filter((r) => r.state === 'active').length;

  return (
    <div>
      <PageHeader title="总览" description="Artifact Gateway 运行状态一览" />
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-5">
        <StatCard label="仓库总数" value={repos.length} sub={`${active} 个活跃`} />
        <StatCard label="分组" value={groups.length} sub={`共 ${groups.reduce((n, g) => n + (g.members?.length ?? 0), 0)} 个成员引用`} />
        <StatCard
          label="格式分布"
          value={
            <div className="flex flex-wrap gap-1.5 pt-1">
              {(['oci', 'maven', 'conan', 'raw'] as const).map((f) => (
                <span key={f} className="flex items-center gap-1 text-sm">
                  <FormatBadge format={f} />
                  <span className="text-zinc-400">{formatCount(f)}</span>
                </span>
              ))}
            </div>
          }
        />
        <StatCard
          label="存储占用"
          value={totalBytes !== null ? formatBytes(totalBytes) : '—'}
          sub={totalObjects !== null ? `${formatNumber(totalObjects)} 个对象` : '容量未启用'}
        />
        <StatCard label="最近审计" value={audits.length} sub="最新记录条数" />
      </div>

      <div className="mt-6 grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader
            title="仓库"
            extra={
              <Link to="/repositories" className="text-xs text-cyan-400 hover:text-cyan-300">
                查看全部 →
              </Link>
            }
          />
          <DataTable columns={['名称', '格式', '状态', 'ID']}>
            {repos.slice(0, 6).map((r) => (
              <tr key={r.id} className="hover:bg-zinc-800/30">
                <td className="px-4 py-2.5">
                  <Link to={`/repositories/${r.id}`} className="font-medium text-zinc-100 hover:text-cyan-300">
                    {r.name}
                  </Link>
                </td>
                <td className="px-4 py-2.5">
                  <FormatBadge format={r.format} />
                </td>
                <td className="px-4 py-2.5">
                  <StateBadge state={r.state} />
                </td>
                <td className="px-4 py-2.5 font-mono text-xs text-zinc-500">{r.id.slice(0, 8)}…</td>
              </tr>
            ))}
            {repos.length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-8 text-center text-sm text-zinc-500">
                  暂无仓库
                </td>
              </tr>
            )}
          </DataTable>
        </Card>

        <Card>
          <CardHeader
            title="最近审计事件"
            extra={
              <Link to="/audits" className="text-xs text-cyan-400 hover:text-cyan-300">
                查看全部 →
              </Link>
            }
          />
          <DataTable columns={['时间', '操作', '结果', 'Actor']}>
            {audits.map((a, i) => (
              <tr key={a.requestId ?? i} className="hover:bg-zinc-800/30">
                <td className="whitespace-nowrap px-4 py-2.5 font-mono text-xs text-zinc-400">
                  {formatDate(a.occurredAt)}
                </td>
                <td className="max-w-48 truncate px-4 py-2.5 text-zinc-300" title={a.operation}>
                  {a.operation ?? '—'}
                </td>
                <td className="px-4 py-2.5">
                  <StateBadge state={a.outcome} />
                </td>
                <td className="max-w-32 truncate px-4 py-2.5 text-xs text-zinc-500" title={a.actor}>
                  {a.actor ?? '—'}
                </td>
              </tr>
            ))}
            {audits.length === 0 && (
              <tr>
                <td colSpan={4} className="px-4 py-8 text-center text-sm text-zinc-500">
                  暂无审计记录
                </td>
              </tr>
            )}
          </DataTable>
        </Card>
      </div>
    </div>
  );
}
