import { useCallback, useEffect, useState } from 'react';
import {
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  SearchOutlined,
  SettingOutlined,
} from '@ant-design/icons';
import { Button, Input, Segmented, Select, Space, Switch } from 'antd';
import {
  listGroups,
  createGroup,
  deleteGroup,
  listGroupMembers,
  replaceGroup,
  replaceGroupMembers,
  listRepositories,
  getGroupCapacity,
} from '../client';
import type { Group, Format, Member, Repository } from '../client';
import { PageHeader, Card, DataTable, Pagination, Field, StatCard } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState, isNotFound } from '../components/Feedback';
import { Badge, FormatBadge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';
import { MemberOrderPicker } from '../components/MemberOrderPicker';

const FORMATS: Format[] = ['oci', 'maven', 'conan', 'raw'];

function CreateGroupDialog({ repos, onCreated }: { repos: Repository[]; onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [format, setFormat] = useState<Format>('oci');
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [anonymousRead, setAnonymousRead] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter((r) => r.format === format && r.state === 'active');

  const submit = async () => {
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({ repositoryId, position }));
    const { error: err } = await createGroup({
      body: { name: name.trim(), format, anonymousRead, members },
      headers: { 'Idempotency-Key': crypto.randomUUID() },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    setName('');
    setMemberIds([]);
    setAnonymousRead(false);
    onCreated();
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
        新建分组
      </Button>
      <Modal
        open={dialog.open}
        title="新建分组"
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button type="primary" onClick={submit} loading={busy} disabled={!name.trim()}>
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="分组名称">
            <Input className="font-mono" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="格式" group>
            <Segmented<Format>
              block
              className="font-mono"
              value={format}
              options={FORMATS}
              onChange={(nextFormat) => {
                setFormat(nextFormat);
                setMemberIds([]);
              }}
            />
          </Field>
          <Field label="成员仓库" hint="分组按成员顺序（自上而下）解析制品" group>
            {candidates.length === 0 ? (
              <div className="rounded-lg border border-zinc-800 px-2 py-3 text-center text-xs text-zinc-600">
                该格式下暂无活跃仓库
              </div>
            ) : (
              <MemberOrderPicker candidates={candidates} memberIds={memberIds} onChange={setMemberIds} />
            )}
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-zinc-200">允许匿名读取</div>
              <div className="mt-0.5 text-xs text-zinc-500">Group 和成员 Repository 都允许匿名读取时，匿名请求才会解析该成员。</div>
            </div>
            <Switch checked={anonymousRead} onChange={setAnonymousRead} aria-label="允许分组匿名读取" />
          </div>
        </div>
      </Modal>
    </>
  );
}

function CapacityDialog({ group }: { group: Group }) {
  const dialog = useDisclosure();
  const [capacity, setCapacity] = useState<Awaited<ReturnType<typeof getGroupCapacity>>['data'] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const load = async () => {
    setError(null);
    setCapacity(null);
    const result = await getGroupCapacity({ path: { groupId: group.id } });
    if (result.error || !result.data) setError(result.error ?? new Error('加载分组容量失败'));
    else setCapacity(result.data);
  };
  const formatBytes = (value: number) => value < 1024 ? `${value} B` : `${(value / 1024 / 1024).toFixed(1)} MB`;
  return (
    <>
      <Button size="small" type="text" icon={<DatabaseOutlined />} onClick={() => { dialog.show(); void load(); }}>容量</Button>
      <Modal open={dialog.open} onClose={dialog.hide} title={`容量贡献 · ${group.name}`}>
        {error !== null && <ErrorBanner error={error} />}
        {!capacity ? <Loading /> : <DataTable columns={['位置', '成员', '类型', '已用', '对象', '配额']}>
          {capacity.members.map((member) => <tr key={member.repositoryId}>
            <td className="px-4 py-2 text-zinc-500">{member.position + 1}</td>
            <td className="px-4 py-2 font-mono text-xs">{member.repositoryId.slice(0, 8)}…</td>
            <td className="px-4 py-2"><Badge tone={member.type === 'proxy' ? 'blue' : 'green'}>{member.type}</Badge></td>
            <td className="px-4 py-2 text-zinc-300">{formatBytes(member.usedBytes)}</td>
            <td className="px-4 py-2 text-zinc-300">{member.objectCount}</td>
            <td className="px-4 py-2 text-zinc-500">{member.quotaBytes ? formatBytes(member.quotaBytes) : '无限制'}</td>
          </tr>)}
        </DataTable>}
      </Modal>
    </>
  );
}

function RenameGroupDialog({ group, onSaved }: { group: Group; onSaved: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState(group.name);
  const [anonymousRead, setAnonymousRead] = useState(group.anonymousRead);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    const { error: err } = await replaceGroup({
      path: { groupId: group.id },
      body: { ...group, name: name.trim(), anonymousRead },
      headers: { 'If-Match': group.version },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    onSaved();
  };

  return (
    <>
      <Button
        size="small"
        icon={<SettingOutlined />}
        onClick={() => {
          setName(group.name);
          setAnonymousRead(group.anonymousRead);
          setError(null);
          dialog.show();
        }}
      >
        设置
      </Button>
      <Modal
        open={dialog.open}
        title={`设置分组：${group.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button type="primary" onClick={save} loading={busy} disabled={!name.trim()}>
              保存
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="分组名称">
            <Input className="font-mono" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-zinc-200">允许匿名读取</div>
              <div className="mt-0.5 text-xs text-zinc-500">仍需成员 Repository 自身允许匿名读取。</div>
            </div>
            <Switch checked={anonymousRead} onChange={setAnonymousRead} aria-label="允许分组匿名读取" />
          </div>
        </div>
      </Modal>
    </>
  );
}

function MembersDialog({ group, repos, onSaved }: { group: Group; repos: Repository[]; onSaved: () => void }) {
  const dialog = useDisclosure();
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [version, setVersion] = useState(group.version);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter((r) => r.format === group.format && r.state === 'active');

  const open = async () => {
    setError(null);
    dialog.show();
    const { data, response } = await listGroupMembers({ path: { groupId: group.id } });
    if (data) {
      const sorted = [...data].sort((a, b) => a.position - b.position);
      setMemberIds(sorted.map((m) => m.repositoryId));
    }
	const etag = response?.headers.get('ETag');
    if (etag) setVersion(etag.replaceAll('"', ''));
  };

  const save = async () => {
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({ repositoryId, position }));
    const { error: err } = await replaceGroupMembers({
      path: { groupId: group.id },
      body: members,
      headers: { 'If-Match': version },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    onSaved();
  };

  return (
    <>
      <Button
        size="small"
        icon={<EditOutlined />}
        onClick={open}
      >
        编辑成员
      </Button>
      <Modal
        open={dialog.open}
        title={`编辑成员：${group.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button type="primary" onClick={save} loading={busy}>
              保存
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {error !== null && <ErrorBanner error={error} />}
          <p className="text-xs text-zinc-500">调整成员及其优先级顺序（position 自上而下）。</p>
          <MemberOrderPicker candidates={candidates} memberIds={memberIds} onChange={setMemberIds} />
        </div>
      </Modal>
    </>
  );
}

export function GroupsPage() {
  const [groups, setGroups] = useState<Group[]>([]);
  const [repos, setRepos] = useState<Repository[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [toDelete, setToDelete] = useState<Group | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [filter, setFilter] = useState('');
  const [formatFilter, setFormatFilter] = useState<Format | 'all'>('all');

  const repoName = (id: string) => repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + '…';
  const repoById = (id: string) => repos.find((r) => r.id === id);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const [g, r] = await Promise.all([
      listGroups({ query: { pageSize: 100 } }),
      listRepositories({ query: { pageSize: 200 } }),
    ]);
    setLoading(false);
    if (g.error) {
      setError(g.error);
      return;
    }
    setGroups(g.data?.items ?? []);
    setNextToken(g.data?.nextPageToken);
    setRepos(r.data?.items ?? []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const loadMore = async () => {
    if (!nextToken) return;
    setLoadingMore(true);
    const { data, error: err } = await listGroups({ query: { pageSize: 100, pageToken: nextToken } });
    setLoadingMore(false);
    if (err) {
      setError(err);
      return;
    }
    setGroups((prev) => [...prev, ...(data?.items ?? [])]);
    setNextToken(data?.nextPageToken);
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    const { error: err } = await deleteGroup({ path: { groupId: toDelete.id } });
    setDeleting(false);
    if (!err) {
      setToDelete(null);
      void load();
    } else {
      setError(err);
    }
  };

  const visibleGroups = groups.filter((group) =>
    (!filter || group.name.toLowerCase().includes(filter.toLowerCase())) &&
    (formatFilter === 'all' || group.format === formatFilter),
  );
  const memberCount = groups.reduce((total, group) => total + (group.members?.length ?? 0), 0);
  const publicGroups = groups.filter((group) => group.anonymousRead).length;

  return (
    <div>
      <PageHeader
        title="分组"
        description="将多个同格式仓库聚合为统一入口"
        actions={<CreateGroupDialog repos={repos} onCreated={load} />}
      />
      {error !== null ? (
        isNotFound(error) ? (
          <Card>
            <EmptyState title="分组功能未启用" hint="当前后端构建尚未挂载分组管理端点（返回 404）" />
          </Card>
        ) : (
          <ErrorBanner error={error} onRetry={load} />
        )
      ) : loading ? (
        <Loading />
      ) : groups.length === 0 ? (
        <Card>
          <EmptyState title="暂无分组" hint="创建分组以聚合多个仓库" />
        </Card>
      ) : (
        <>
        <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <StatCard label="分组总数" value={groups.length} sub={`${memberCount} 个成员引用`} />
          <StatCard label="匿名可读" value={publicGroups} sub={publicGroups ? '仍需成员仓库允许匿名读取' : '全部为私有入口'} />
          <StatCard label="覆盖格式" value={new Set(groups.map((group) => group.format)).size} sub="按同格式仓库解析" />
        </div>
        <Card>
          <Space wrap className="!flex w-full border-b border-zinc-800/80 px-4 py-3">
            <Input allowClear prefix={<SearchOutlined />} className="w-72" placeholder="搜索分组名称…" value={filter} onChange={(e) => setFilter(e.target.value)} />
            <Select<Format | 'all'>
              className="w-36"
              value={formatFilter}
              onChange={setFormatFilter}
              options={[
                { value: 'all', label: '全部格式' },
                ...FORMATS.map((format) => ({ value: format, label: format })),
              ]}
            />
          </Space>
          {visibleGroups.length === 0 ? <EmptyState title="没有匹配的分组" hint="调整筛选条件后重试" /> :
          <DataTable
            columns={['名称', '格式', '访问', '成员（按优先级）', '版本', '']}
            columnClassNames={['', '', '', '', 'whitespace-nowrap', 'whitespace-nowrap']}
          >
            {visibleGroups.map((g) => (
              <tr key={g.id} className="group hover:bg-zinc-800/30">
                <td className="px-4 py-3 font-medium text-zinc-100">{g.name}</td>
                <td className="px-4 py-3">
                  <FormatBadge format={g.format} />
                </td>
                <td className="px-4 py-3">
                  <Badge tone={g.anonymousRead ? 'green' : 'zinc'}>{g.anonymousRead ? 'anonymous read' : 'private'}</Badge>
                </td>
                <td className="max-w-md px-4 py-3">
                  <div className="flex flex-wrap gap-1">
                    {[...(g.members ?? [])]
                      .sort((a, b) => a.position - b.position)
                      .map((m) => {
                        const repo = repoById(m.repositoryId);
                        return (
                          <span
                            key={m.repositoryId}
                            className="rounded-md bg-zinc-800 px-2 py-0.5 font-mono text-[11px] text-zinc-300"
                            title={m.repositoryId}
                          >
                            {m.position + 1}. {repoName(m.repositoryId)} · {repo?.type ?? 'hosted'} · {repo?.anonymousRead ? 'anon' : 'private'}
                          </span>
                        );
                      })}
                    {(g.members ?? []).length === 0 && <span className="text-xs text-zinc-600">无成员</span>}
                  </div>
                </td>
                <td className="px-4 py-3 font-mono text-xs text-zinc-500">v{g.version}</td>
                <td className="px-4 py-3">
                  <div className="flex items-center justify-end gap-2">
                    <RenameGroupDialog group={g} onSaved={load} />
                    <MembersDialog group={g} repos={repos} onSaved={load} />
                    <CapacityDialog group={g} />
                    <Button
                      type="text"
                      size="small"
                      danger
                      icon={<DeleteOutlined />}
                      onClick={() => setToDelete(g)}
                    >
                      删除
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </DataTable>
          }
          <Pagination hasMore={!!nextToken} loading={loadingMore} onMore={loadMore} />
        </Card>
        </>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title="删除分组"
        message={
          <>
            确定删除分组 <span className="font-mono text-zinc-100">{toDelete?.name}</span> 吗？成员仓库本身不会被删除。
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
