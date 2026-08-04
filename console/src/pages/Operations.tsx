import { useCallback, useEffect, useMemo, useState } from 'react';
import { CloseOutlined, PlayCircleOutlined, RedoOutlined, ReloadOutlined, SyncOutlined } from '@ant-design/icons';
import { Button, Popconfirm, Progress, Select, Space, Tooltip } from 'antd';
import {
  cancelRepositoryLifecycleJob,
  listAuditRetentionJobs,
  listRepositories,
  listRepositoryLifecycleJobs,
  retryRepositoryLifecycleJob,
  runRepositoryLifecycleJobNow,
} from '../client';
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
  nextAttemptAt?: string;
  attempts?: number;
  maxAttempts?: number;
  progressCurrent?: number;
  progressTotal?: number;
  progressMessage?: string;
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
  const [actingJob, setActingJob] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const repositoryResult = await listRepositories({ query: { pageSize: 200 } });
      if (repositoryResult.error) throw repositoryResult.error;
      const nextRepositories = repositoryResult.data?.items ?? [];
      setRepositories(nextRepositories);

      const lifecycle = await Promise.all(nextRepositories.map(async (repo) => {
        const result = await listRepositoryLifecycleJobs({ path: { repositoryId: repo.id } });
        if (result.error) throw result.error;
        return result.data?.map((job: LifecycleJob) => {
          const isHistoricalJob = job.state === 'completed' && job.attempts === 0 && !job.progressTotal;
          return {
            id: job.id,
            kind: job.kind,
            state: job.state,
            repository: repo.name,
            repositoryId: repo.id,
            createdAt: job.createdAt,
            startedAt: job.startedAt,
            completedAt: job.completedAt,
            nextAttemptAt: job.nextAttemptAt,
            attempts: isHistoricalJob ? undefined : job.attempts,
            maxAttempts: isHistoricalJob ? undefined : job.maxAttempts,
            progressCurrent: job.progressCurrent,
            progressTotal: job.progressTotal,
            progressMessage: isHistoricalJob ? '历史任务' : job.progressMessage,
            lastError: job.lastError,
          };
        }) ?? [];
      }));
      const auditRetention = await listAuditRetentionJobs();
      if (auditRetention.error) throw auditRetention.error;
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
    } catch (nextError) {
      setError(nextError);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      if (rows?.some((row) => ['pending', 'running', 'retrying'].includes(row.state))) void load();
    }, 15_000);
    return () => window.clearInterval(timer);
  }, [load, rows]);

  const controlJob = async (row: OperationRow, action: 'run' | 'retry' | 'cancel') => {
    if (!row.repositoryId) return;
    setActingJob(row.id);
    setError(null);
    try {
      const options = { path: { repositoryId: row.repositoryId, lifecycleJobId: row.id } };
      const result = action === 'run'
        ? await runRepositoryLifecycleJobNow(options)
        : action === 'retry'
          ? await retryRepositoryLifecycleJob(options)
          : await cancelRepositoryLifecycleJob(options);
      if (result.error) {
        setError(result.error);
        return;
      }
      await load();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setActingJob(null);
    }
  };

  const visibleRows = useMemo(() => (rows ?? []).filter((row) =>
    (stateFilter === 'all' || row.state === stateFilter) &&
    (kindFilter === 'all' || row.kind === kindFilter) &&
    (repositoryFilter === 'all' || row.repositoryId === repositoryFilter),
  ), [kindFilter, repositoryFilter, rows, stateFilter]);
  const running = (rows ?? []).filter((row) => row.state === 'running').length;
  const failed = (rows ?? []).filter((row) => row.state === 'failed').length;
  const pending = (rows ?? []).filter((row) => row.state === 'pending' || row.state === 'retrying').length;
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
            { value: 'retrying', label: 'retrying' },
            { value: 'completed', label: 'completed' },
            { value: 'failed', label: 'failed' },
            { value: 'cancelled', label: 'cancelled' },
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
          <DataTable className="min-w-[1440px]" columns={['类型', '仓库', '状态', '进度', '尝试', '创建时间', '下次执行 / 完成时间', '任务 ID / 失败原因', '操作']}>
            {visibleRows.map((row) => (
              <tr key={`${row.kind}-${row.id}`} className="hover:bg-zinc-800/30">
                <td className="px-4 py-3 text-sm text-zinc-200">
                  <div className="flex items-center gap-2"><SyncOutlined className="text-zinc-500" />{kindLabel(row.kind)}</div>
                </td>
                <td className="px-4 py-3 text-xs text-zinc-400">{row.repository ?? '全局'}</td>
                <td className="px-4 py-3"><StateBadge state={row.state} /></td>
                <td className="w-44 px-4 py-3">
                  {row.progressTotal !== undefined && row.progressTotal > 0 ? (
                    <div>
                      <Progress
                        percent={Math.round(((row.progressCurrent ?? 0) / row.progressTotal) * 100)}
                        size="small"
                        status={row.state === 'failed' ? 'exception' : row.state === 'completed' ? 'success' : 'normal'}
                        format={() => `${row.progressCurrent ?? 0}/${row.progressTotal}`}
                      />
                      {row.progressMessage && <div className="mt-1 max-w-40 truncate text-[11px] text-zinc-500" title={row.progressMessage}>{row.progressMessage}</div>}
                    </div>
                  ) : (
                    <span className="text-xs text-zinc-600">{row.progressMessage ?? '未报告'}</span>
                  )}
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-400">
                  {row.attempts === undefined ? '—' : `${row.attempts} / ${row.maxAttempts}`}
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(row.createdAt)}</td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">
                  <div>{row.nextAttemptAt ? formatDate(row.nextAttemptAt) : formatDate(row.completedAt)}</div>
                  {row.startedAt && <div className="mt-1 text-[11px] text-zinc-600">开始 {formatDate(row.startedAt)}</div>}
                </td>
                <td className="max-w-96 px-4 py-3 font-mono text-[11px] text-zinc-500">
                  <div className="truncate" title={row.id}>{row.id}</div>
                  {row.lastError && <div className="mt-1 truncate text-rose-300" title={row.lastError}>{row.lastError}</div>}
                </td>
                <td className="whitespace-nowrap px-4 py-3">
                  {row.repositoryId ? (
                    <Space size="small">
                      {(row.state === 'pending' || row.state === 'retrying') && (
                        <Tooltip title="立即加入执行队列">
                          <Button aria-label="立即加入执行队列" size="small" icon={<PlayCircleOutlined />} loading={actingJob === row.id} onClick={() => void controlJob(row, 'run')} />
                        </Tooltip>
                      )}
                      {(row.state === 'failed' || row.state === 'cancelled') && (
                        <Tooltip title="重新执行任务">
                          <Button aria-label="重新执行任务" size="small" icon={<RedoOutlined />} loading={actingJob === row.id} onClick={() => void controlJob(row, 'retry')} />
                        </Tooltip>
                      )}
                      {(row.state === 'pending' || row.state === 'retrying') && (
                        <Popconfirm title="取消此任务？" description="取消后仍可从任务中心重新执行。" okText="取消任务" cancelText="返回" onConfirm={() => controlJob(row, 'cancel')}>
                          <Tooltip title="取消任务">
                            <Button aria-label="取消任务" danger size="small" icon={<CloseOutlined />} loading={actingJob === row.id} />
                          </Tooltip>
                        </Popconfirm>
                      )}
                    </Space>
                  ) : <span className="text-xs text-zinc-600">—</span>}
                </td>
              </tr>
            ))}
          </DataTable>
        </Card>
      )}
    </div>
  );
}
