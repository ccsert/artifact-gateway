import { useCallback, useEffect, useMemo, useState } from 'react';
import { ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { Button, Select, Space } from 'antd';
import { listAuditRetentionJobs, listRepositories, listRepositoryLifecycleJobs } from '../client';
import type { LifecycleJob, Repository } from '../client';
import { PageHeader, Card, DataTable, StatCard } from '../components/Layout';
import { EmptyState, ErrorBanner, Loading } from '../components/Feedback';
import { StateBadge } from '../components/Badge';
import { formatDate } from '../lib/format';

type OperationRow = {
  id: string;
  kind: string;
  state: string;
  repository?: string;
  repositoryId?: string;
  createdAt: string;
  startedAt?: string;
  completedAt?: string;
  lastError?: string;
};

const KIND_LABELS: Record<string, string> = {
  retention: '制品保留',
  promotion: '制品晋级',
  replication: '仓库复制',
  reclaim: '对象回收',
  'audit-retention': '审计保留',
};

function kindLabel(kind: string): string {
  return KIND_LABELS[kind] ?? kind;
}

export function OperationsPage() {
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [rows, setRows] = useState<OperationRow[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [stateFilter, setStateFilter] = useState('all');
  const [kindFilter, setKindFilter] = useState('all');
  const [repositoryFilter, setRepositoryFilter] = useState('all');

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const repositoryResult = await listRepositories({ query: { pageSize: 200 } });
    if (repositoryResult.error) {
      setError(repositoryResult.error);
      setLoading(false);
      return;
    }
    const nextRepositories = repositoryResult.data?.items ?? [];
    setRepositories(nextRepositories);

    const lifecycle = await Promise.all(nextRepositories.map(async (repo) => {
      const result = await listRepositoryLifecycleJobs({ path: { repositoryId: repo.id } });
      return result.data?.map((job: LifecycleJob) => ({
        id: job.id,
        kind: job.kind,
        state: job.state,
        repository: repo.name,
        repositoryId: repo.id,
        createdAt: job.createdAt,
        startedAt: job.startedAt,
        completedAt: job.completedAt,
        lastError: job.lastError,
      })) ?? [];
    }));
    const auditRetention = await listAuditRetentionJobs();
    const auditRows: OperationRow[] = (auditRetention.data ?? []).map((job) => ({
      id: job.id,
      kind: 'audit-retention',
      state: job.state,
      createdAt: job.createdAt,
      startedAt: job.startedAt,
      completedAt: job.completedAt,
      lastError: job.lastError,
    }));
    setRows([...lifecycle.flat(), ...auditRows].sort((a, b) => b.createdAt.localeCompare(a.createdAt)));
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const visibleRows = useMemo(() => (rows ?? []).filter((row) =>
    (stateFilter === 'all' || row.state === stateFilter) &&
    (kindFilter === 'all' || row.kind === kindFilter) &&
    (repositoryFilter === 'all' || row.repositoryId === repositoryFilter),
  ), [kindFilter, repositoryFilter, rows, stateFilter]);
  const running = (rows ?? []).filter((row) => row.state === 'running').length;
  const failed = (rows ?? []).filter((row) => row.state === 'failed').length;
  const pending = (rows ?? []).filter((row) => row.state === 'pending').length;
  const kindOptions = Array.from(new Set((rows ?? []).map((row) => row.kind))).sort();

  return (
    <div>
      <PageHeader
        title="任务中心"
        description="跨仓库查看晋级、复制、保留和对象回收任务的当前状态。"
        actions={<Button icon={<ReloadOutlined />} onClick={() => void load()} loading={loading}>刷新</Button>}
      />
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-4">
        <StatCard label="任务总数" value={rows?.length ?? '—'} sub="当前保留窗口" />
        <StatCard label="运行中" value={running} sub="正在处理" />
        <StatCard label="待处理" value={pending} sub="等待 worker" />
        <StatCard label="失败" value={failed} sub={failed ? '需要检查失败原因' : '当前没有失败任务'} />
      </div>
      <Space wrap className="mb-4">
        <Select
          className="w-36"
          value={stateFilter}
          onChange={setStateFilter}
          options={[
            { value: 'all', label: '全部状态' },
            { value: 'pending', label: 'pending' },
            { value: 'running', label: 'running' },
            { value: 'completed', label: 'completed' },
            { value: 'failed', label: 'failed' },
          ]}
        />
        <Select
          className="w-36"
          value={kindFilter}
          onChange={setKindFilter}
          options={[{ value: 'all', label: '全部类型' }, ...kindOptions.map((kind) => ({ value: kind, label: kindLabel(kind) }))]}
        />
        <Select
          className="min-w-48"
          value={repositoryFilter}
          onChange={setRepositoryFilter}
          options={[{ value: 'all', label: '全部仓库' }, ...repositories.map((repo) => ({ value: repo.id, label: repo.name }))]}
        />
      </Space>
      {error ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !rows ? (
        <Loading label="加载任务状态…" />
      ) : visibleRows.length === 0 ? (
        <Card><EmptyState title="没有匹配的任务" hint="调整筛选条件，或等待任务产生后再刷新。" /></Card>
      ) : (
        <Card>
          <DataTable columns={['类型', '仓库', '状态', '创建时间', '开始时间', '完成时间', '任务 ID / 失败原因']}>
            {visibleRows.map((row) => (
              <tr key={`${row.kind}-${row.id}`} className="hover:bg-zinc-800/30">
                <td className="px-4 py-3 text-sm text-zinc-200">
                  <div className="flex items-center gap-2"><SyncOutlined className="text-zinc-500" />{kindLabel(row.kind)}</div>
                </td>
                <td className="px-4 py-3 text-xs text-zinc-400">{row.repository ?? '全局'}</td>
                <td className="px-4 py-3"><StateBadge state={row.state} /></td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(row.createdAt)}</td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(row.startedAt)}</td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(row.completedAt)}</td>
                <td className="max-w-96 px-4 py-3 font-mono text-[11px] text-zinc-500">
                  <div className="truncate" title={row.id}>{row.id}</div>
                  {row.lastError && <div className="mt-1 truncate text-rose-300" title={row.lastError}>{row.lastError}</div>}
                </td>
              </tr>
            ))}
          </DataTable>
        </Card>
      )}
    </div>
  );
}
