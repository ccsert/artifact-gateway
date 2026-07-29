import { useCallback, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { listRepositories, createRepository, deleteRepository } from '../client';
import type { Repository, Format } from '../client';
import { useAuth } from '../lib/auth';
import { listKnownGroups } from '../lib/v1proxy';
import type { ProxyFormat, V1Group } from '../lib/v1proxy';
import { PageHeader, Card, DataTable, Pagination, Field, inputClass, btnPrimary, btnSecondary } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { FormatBadge, StateBadge, Badge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';

const FORMATS: Format[] = ['oci', 'maven', 'conan', 'raw'];

function CreateRepositoryDialog({ onCreated }: { onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [format, setFormat] = useState<Format>('oci');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const { data, error: err } = await createRepository({
      body: { name: name.trim(), format },
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
      onCreated();
    }
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
            <button onClick={submit} disabled={busy || !name.trim()} className={btnPrimary}>
              {busy ? '创建中…' : '创建'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
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
        </div>
      </Modal>
    </>
  );
}

export function RepositoriesPage() {
  const { token } = useAuth();
  const navigate = useNavigate();
  const [items, setItems] = useState<Repository[]>([]);
  const [proxyGroups, setProxyGroups] = useState<{ format: ProxyFormat; group: V1Group }[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [filter, setFilter] = useState('');
  const [toDelete, setToDelete] = useState<Repository | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const { data, error: err } = await listRepositories({ query: { pageSize: 100 } });
    // 代理组加载失败不阻塞 hosted 列表
    listKnownGroups(token).then(setProxyGroups).catch(() => setProxyGroups([]));
    setLoading(false);
    if (err) {
      setError(err);
      return;
    }
    setItems(data?.items ?? []);
    setNextToken(data?.nextPageToken);
  }, [token]);

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
    (r) => !q || r.name.toLowerCase().includes(q) || r.format.includes(q) || 'hosted'.includes(q),
  );
  const visibleProxy = proxyGroups.filter(
    ({ format, group }) =>
      !q || group.name.toLowerCase().includes(q) || format.includes(q) || 'proxy'.includes(q),
  );

  return (
    <div>
      <PageHeader
        title="仓库"
        description="托管仓库（hosted）与代理组（proxy）的统一视图"
        actions={<CreateRepositoryDialog onCreated={load} />}
      />
      <div className="mb-4">
        <input
          className={`${inputClass} max-w-xs`}
          placeholder="按名称、格式或类型过滤…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
      </div>
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : loading ? (
        <Loading />
      ) : visible.length === 0 && visibleProxy.length === 0 ? (
        <Card>
          <EmptyState title="暂无仓库" hint="点击右上角「新建仓库」创建第一个仓库" />
        </Card>
      ) : (
        <Card>
          <DataTable columns={['名称', '类型', '格式', '状态', '版本/上游', 'ID', '']}>
            {visible.map((r) => (
              <tr key={r.id} className="group hover:bg-zinc-800/30">
                <td className="px-4 py-3">
                  <Link to={`/repositories/${r.id}`} className="font-medium text-zinc-100 hover:text-cyan-300">
                    {r.name}
                  </Link>
                </td>
                <td className="px-4 py-3">
                  <Badge tone="cyan">hosted</Badge>
                </td>
                <td className="px-4 py-3">
                  <FormatBadge format={r.format} />
                </td>
                <td className="px-4 py-3">
                  <StateBadge state={r.state} />
                </td>
                <td className="px-4 py-3 font-mono text-xs text-zinc-500">v{r.version}</td>
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
            ))}
            {visibleProxy.map(({ format, group }) => {
              const upstream = group.members.find((m) => m.type === 'proxy')?.endpoint ?? '';
              return (
                <tr
                  key={`proxy-${format}-${group.name}`}
                  className="group cursor-pointer hover:bg-zinc-800/30"
                  onClick={() => navigate('/proxy')}
                >
                  <td className="px-4 py-3">
                    <span className="font-medium text-zinc-100 group-hover:text-cyan-300">{group.name}</span>
                  </td>
                  <td className="px-4 py-3">
                    <Badge tone="amber">proxy</Badge>
                  </td>
                  <td className="px-4 py-3">
                    <FormatBadge format={format} />
                  </td>
                  <td className="px-4 py-3">
                    <StateBadge state={group.enabled ? 'enabled' : 'disabled'} />
                  </td>
                  <td className="max-w-48 truncate px-4 py-3 font-mono text-xs text-zinc-500" title={upstream}>
                    {upstream || '—'}
                  </td>
                  <td className="px-4 py-3 font-mono text-xs text-zinc-600">v1</td>
                  <td className="px-4 py-3 text-right text-xs text-zinc-600">→</td>
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
