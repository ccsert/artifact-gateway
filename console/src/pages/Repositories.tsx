import { useCallback, useEffect, useState } from 'react';
import { ClearOutlined, DeleteOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Input, Segmented, Select, Space } from 'antd';
import { Link } from 'react-router-dom';
import { listRepositories, createRepository, deleteRepository, getRepositoryCapacity } from '../client';
import type { Repository, Format } from '../client';
import { PageHeader, Card, DataTable, Pagination, Field, StatCard } from '../components/Layout';
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
      <Button
        type="primary"
        icon={<PlusOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        新建仓库
      </Button>
      <Modal
        open={dialog.open}
        title="新建仓库"
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={busy || !name.trim() || (type === 'proxy' && (!endpoint.trim() || (needsHosts && !allowedHosts.trim())))}
            >
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="类型" group>
            <Segmented<'hosted' | 'proxy'>
              block
              value={type}
              onChange={setType}
              options={[
                { value: 'hosted', label: '托管 (hosted)' },
                { value: 'proxy', label: '代理 (proxy)' },
              ]}
            />
            <span className="mt-1 block text-xs text-zinc-600">
              {type === 'hosted' ? '自己托管制品，可推送' : '从上游仓库拉取并缓存'}
            </span>
          </Field>
          <Field label="仓库名称" hint="小写字母、数字与连字符，例如 team-images">
            <Input
              className="font-mono"
              placeholder="my-repository"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="格式" group>
            <Segmented<Format>
              block
              className="font-mono"
              value={format}
              onChange={setFormat}
              options={FORMATS}
            />
          </Field>
          {type === 'proxy' && (
            <>
              <Field label="上游地址 endpoint" hint="代理拉取的外部仓库地址">
                <Input
                  className="font-mono text-xs"
                  placeholder={endpointPlaceholder[format]}
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </Field>
              <Field
                label={`允许主机 allowedHosts${needsHosts ? '（必填）' : '（可选）'}`}
                hint="逗号分隔的主机名；raw/conan 代理必填"
              >
                <Input
                  className="font-mono text-xs"
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
    const { data, error: err } = await listRepositories({ query: { pageSize: 100, pageToken: nextToken } });
    if (err) {
      setLoadingMore(false);
      setError(err);
      return;
    }
    const nextItems = data?.items ?? [];
    setItems((prev) => [...prev, ...nextItems]);
    setNextToken(data?.nextPageToken);
    const activeItems = nextItems.filter((repository) => repository.state === 'active');
    const capacityResults = await Promise.all(
      activeItems.map(async (repository) => {
        const result = await getRepositoryCapacity({ path: { repositoryId: repository.id } });
        return result.data ? [repository.id, result.data] as const : null;
      }),
    );
    setCapacities((previous) => ({
      ...previous,
      ...Object.fromEntries(capacityResults.filter((entry): entry is NonNullable<typeof entry> => entry !== null)),
    }));
    setLoadingMore(false);
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    const { error: err } = await deleteRepository({ path: { repositoryId: toDelete.id } });
    setDeleting(false);
    if (!err) {
      setToDelete(null);
      void load();
    } else {
      setError(err);
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
      <Space wrap className="mb-4">
        <Input
          allowClear
          prefix={<SearchOutlined />}
          className="w-72"
          placeholder="搜索名称或类型…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <Select<Format | 'all'>
          className="w-36"
          value={formatFilter}
          onChange={setFormatFilter}
          options={[
            { value: 'all', label: '全部格式' },
            ...FORMATS.map((format) => ({ value: format, label: format })),
          ]}
        />
        <Select<Repository['state'] | 'all'>
          className="w-36"
          value={stateFilter}
          onChange={setStateFilter}
          options={[
            { value: 'all', label: '全部状态' },
            { value: 'active', label: 'active' },
            { value: 'deleting', label: 'deleting' },
            { value: 'deleted', label: 'deleted' },
          ]}
        />
        {(filter || formatFilter !== 'all' || stateFilter !== 'all') && (
          <Button type="text" icon={<ClearOutlined />} onClick={() => { setFilter(''); setFormatFilter('all'); setStateFilter('all'); }}>
            清除筛选
          </Button>
        )}
      </Space>
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : loading ? (
        <Loading />
      ) : items.length === 0 ? (
        <Card>
          <EmptyState title="暂无仓库" hint="点击右上角「新建仓库」创建第一个仓库" />
        </Card>
      ) : (
        <Card>
          {visible.length === 0 ? (
            <EmptyState title="没有匹配的仓库" hint="调整筛选条件，或继续加载更多仓库" />
          ) : <DataTable columns={['名称', '类型', '格式', '状态', '容量', '配置', 'ID', '']}>
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
                    <Button
                      type="text"
                      size="small"
                      danger
                      icon={<DeleteOutlined />}
                      onClick={() => setToDelete(r)}
                    >
                      删除
                    </Button>
                  </td>
                </tr>
              );
            })}
          </DataTable>}
          <Pagination hasMore={!!nextToken} loading={loadingMore} onMore={loadMore} />
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
