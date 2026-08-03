import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { listRepositories, createRepository, deleteRepository, getRepositoryCapacity } from '../client';
import type { Repository, Format } from '../client';
import { PageHeader, Card, DataTable, Pagination, Field, inputClass, btnPrimary, btnSecondary, StatCard } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { FormatBadge, StateBadge, Badge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';
import { formatBytes, formatNumber } from '../lib/format';

const FORMATS: Format[] = ['oci', 'maven', 'conan', 'raw'];

function CreateRepositoryDialog({ onCreated }: { onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [format, setFormat] = useState<Format>('oci');
  const [type, setType] = useState<'hosted' | 'proxy'>('hosted');
  const [endpoint, setEndpoint] = useState('');
  const [allowedHosts, setAllowedHosts] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const needsHosts = type === 'proxy' && (format === 'raw' || format === 'conan');

  const submit = async () => {
    setBusy(true);
    setError(null);
    const hosts = allowedHosts.split(',').map((h) => h.trim()).filter(Boolean);
    const { data, error: err } = await createRepository({
      body: {
        name: name.trim(),
        format,
        type,
        ...(type === 'proxy' ? { endpoint: endpoint.trim(), allowedHosts: hosts } : {}),
      },
      headers: { 'Idempotency-Key': crypto.randomUUID() },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      dialog.hide();
      setName('');
      setEndpoint('');
      setAllowedHosts('');
      setType('hosted');
      onCreated();
    }
  };

  const endpointPlaceholder: Record<string, string> = {
    oci: 'https://registry-1.docker.io',
    maven: 'https://repo1.maven.org/maven2',
    raw: 'https://raw.githubusercontent.com',
    conan: 'https://center.conan.io/v2',
  };

  return (
    <>
      <button onClick={dialog.show} className={btnPrimary}>
        + 新建仓库
      </button>
      <Modal
        open={dialog.open}
        title="新建仓库"
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button
              onClick={submit}
              disabled={busy || !name.trim() || (type === 'proxy' && (!endpoint.trim() || (needsHosts && !allowedHosts.trim())))}
              className={btnPrimary}
            >
              {busy ? '创建中…' : '创建'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="类型">
            <div className="grid grid-cols-2 gap-2">
              {(['hosted', 'proxy'] as const).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => setType(t)}
                  className={`rounded-md border px-3 py-2 text-sm transition-colors ${
                    type === t
                      ? 'border-cyan-500/60 bg-cyan-500/10 text-cyan-300'
                      : 'border-zinc-700 text-zinc-400 hover:bg-zinc-800'
                  }`}
                >
                  {t === 'hosted' ? '托管 (hosted)' : '代理 (proxy)'}
                </button>
              ))}
            </div>
            <span className="mt-1 block text-xs text-zinc-600">
              {type === 'hosted' ? '自己托管制品，可推送' : '从上游仓库拉取并缓存'}
            </span>
          </Field>
          <Field label="仓库名称" hint="小写字母、数字与连字符，例如 team-images">
            <input
              className={`${inputClass} font-mono`}
              placeholder="my-repository"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="格式">
            <div className="grid grid-cols-4 gap-2">
              {FORMATS.map((f) => (
                <button
                  key={f}
                  type="button"
                  onClick={() => setFormat(f)}
                  className={`rounded-md border px-3 py-2 font-mono text-sm transition-colors ${
                    format === f
                      ? 'border-cyan-500/60 bg-cyan-500/10 text-cyan-300'
                      : 'border-zinc-700 text-zinc-400 hover:bg-zinc-800'
                  }`}
                >
                  {f}
                </button>
              ))}
            </div>
          </Field>
          {type === 'proxy' && (
            <>
              <Field label="上游地址 endpoint" hint="代理拉取的外部仓库地址">
                <input
                  className={`${inputClass} font-mono text-xs`}
                  placeholder={endpointPlaceholder[format]}
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </Field>
              <Field
                label={`允许主机 allowedHosts${needsHosts ? '（必填）' : '（可选）'}`}
                hint="逗号分隔的主机名；raw/conan 代理必填"
              >
                <input
                  className={`${inputClass} font-mono text-xs`}
                  placeholder="repo1.maven.org"
                  value={allowedHosts}
                  onChange={(e) => setAllowedHosts(e.target.value)}
                />
              </Field>
            </>
          )}
        </div>
      </Modal>
    </>
  );
}

export function RepositoriesPage() {
  const [items, setItems] = useState<Repository[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [filter, setFilter] = useState('');
  const [formatFilter, setFormatFilter] = useState<Format | 'all'>('all');
  const [stateFilter, setStateFilter] = useState<Repository['state'] | 'all'>('all');
  const [capacities, setCapacities] = useState<Record<string, { usedBytes: number; objectCount: number; quotaBytes: number }>>({});
  const [toDelete, setToDelete] = useState<Repository | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const { data, error: err } = await listRepositories({ query: { pageSize: 100 } });
    setLoading(false);
    if (err) {
      setError(err);
      return;
    }
    const nextItems = data?.items ?? [];
    setItems(nextItems);
    setNextToken(data?.nextPageToken);
    const activeItems = nextItems.filter((repository) => repository.state === 'active');
    const capacityResults = await Promise.all(
      activeItems.map(async (repository) => {
        const result = await getRepositoryCapacity({ path: { repositoryId: repository.id } });
        return result.data ? [repository.id, result.data] as const : null;
      }),
    );
    setCapacities(Object.fromEntries(capacityResults.filter((entry): entry is NonNullable<typeof entry> => entry !== null)));
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const loadMore = async () => {
    if (!nextToken) return;
    setLoadingMore(true);
    const { data } = await listRepositories({ query: { pageSize: 100, pageToken: nextToken } });
    setLoadingMore(false);
    setItems((prev) => [...prev, ...(data?.items ?? [])]);
    setNextToken(data?.nextPageToken);
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    const { error: err } = await deleteRepository({ path: { repositoryId: toDelete.id } });
    setDeleting(false);
    if (!err) {
      setToDelete(null);
      void load();
    }
  };

  const q = filter.toLowerCase();
  const visible = items.filter(
    (r) =>
      !q ||
      r.name.toLowerCase().includes(q) ||
      r.format.includes(q) ||
      (r.type ?? 'hosted').includes(q),
  ).filter((r) => formatFilter === 'all' || r.format === formatFilter)
    .filter((r) => stateFilter === 'all' || r.state === stateFilter);

  const activeCount = items.filter((r) => r.state === 'active').length;
  const proxyCount = items.filter((r) => r.type === 'proxy').length;
  const totalUsedBytes = Object.values(capacities).reduce((sum, value) => sum + value.usedBytes, 0);

  return (
    <div>
      <PageHeader
        title="仓库"
        description="Hosted 与 Proxy Repository 的统一视图"
        actions={<CreateRepositoryDialog onCreated={load} />}
      />
      <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
        <StatCard label="仓库总数" value={items.length} sub={`${activeCount} 个活跃`} />
        <StatCard label="代理仓库" value={proxyCount} sub="上游缓存与镜像" />
        <StatCard label="当前占用" value={totalUsedBytes ? formatBytes(totalUsedBytes) : '—'} sub={Object.keys(capacities).length ? `${formatNumber(Object.values(capacities).reduce((sum, value) => sum + value.objectCount, 0))} 个对象` : '容量未启用'} />
      </div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <input
          className={`${inputClass} min-w-56 max-w-xs`}
          placeholder="搜索名称或类型…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <select className={`${inputClass} w-auto min-w-28`} value={formatFilter} onChange={(e) => setFormatFilter(e.target.value as Format | 'all')}>
          <option value="all">全部格式</option>
          {FORMATS.map((format) => <option key={format} value={format}>{format}</option>)}
        </select>
        <select className={`${inputClass} w-auto min-w-28`} value={stateFilter} onChange={(e) => setStateFilter(e.target.value as Repository['state'] | 'all')}>
          <option value="all">全部状态</option>
          <option value="active">active</option>
          <option value="deleting">deleting</option>
          <option value="deleted">deleted</option>
        </select>
        {(filter || formatFilter !== 'all' || stateFilter !== 'all') && (
          <button className="px-2 text-xs text-zinc-500 hover:text-zinc-200" onClick={() => { setFilter(''); setFormatFilter('all'); setStateFilter('all'); }}>
            清除筛选
          </button>
        )}
      </div>
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : loading ? (
        <Loading />
      ) : visible.length === 0 ? (
        <Card>
          <EmptyState title="暂无仓库" hint="点击右上角「新建仓库」创建第一个仓库" />
        </Card>
      ) : (
        <Card>
          <DataTable columns={['名称', '类型', '格式', '状态', '容量', '配置', 'ID', '']}>
            {visible.map((r) => {
              const isProxy = r.type === 'proxy';
              return (
                <tr key={r.id} className="group hover:bg-zinc-800/30">
                  <td className="px-4 py-3">
                    <Link to={`/repositories/${r.id}`} className="font-medium text-zinc-100 hover:text-cyan-300">
                      {r.name}
                    </Link>
                  </td>
                  <td className="px-4 py-3">
                    <Badge tone={isProxy ? 'amber' : 'cyan'}>{isProxy ? 'proxy' : 'hosted'}</Badge>
                  </td>
                  <td className="px-4 py-3">
                    <FormatBadge format={r.format} />
                  </td>
                  <td className="px-4 py-3">
                    <StateBadge state={r.state} />
                  </td>
                  <td className="px-4 py-3">
                    {capacities[r.id] ? (
                      <div>
                        <div className="font-mono text-xs text-zinc-300">{formatBytes(capacities[r.id].usedBytes)}</div>
                        {capacities[r.id].quotaBytes > 0 && <div className="mt-1 text-[10px] text-zinc-600">/ {formatBytes(capacities[r.id].quotaBytes)}</div>}
                      </div>
                    ) : <span className="text-xs text-zinc-600">—</span>}
                  </td>
                  <td className="max-w-48 truncate px-4 py-3 font-mono text-xs text-zinc-500" title={isProxy ? r.endpoint : `v${r.version}`}>
                    {isProxy ? r.endpoint : `v${r.version}`}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-zinc-500" title={r.id}>
                    {r.id.slice(0, 8)}…
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => setToDelete(r)}
                      className="rounded px-2 py-1 text-xs text-zinc-600 opacity-0 transition-opacity hover:bg-rose-500/10 hover:text-rose-400 group-hover:opacity-100"
                    >
                      删除
                    </button>
                  </td>
                </tr>
              );
            })}
          </DataTable>
          <Pagination hasMore={!!nextToken && !filter} loading={loadingMore} onMore={loadMore} />
        </Card>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title="删除仓库"
        message={
          <>
            确定要删除仓库 <span className="font-mono text-zinc-100">{toDelete?.name}</span> 吗？
            仓库将进入 deleting 状态，此操作不可撤销。
          </>
        }
        confirmLabel="删除"
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
