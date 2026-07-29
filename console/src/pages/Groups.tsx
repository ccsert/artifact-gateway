import { useCallback, useEffect, useState } from 'react';
import {
  listGroups,
  createGroup,
  deleteGroup,
  listGroupMembers,
  replaceGroup,
  replaceGroupMembers,
  listRepositories,
} from '../client';
import type { Group, Format, Member, Repository } from '../client';
import { PageHeader, Card, DataTable, Pagination, Field, inputClass, btnPrimary, btnSecondary } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState, isNotFound } from '../components/Feedback';
import { FormatBadge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';

const FORMATS: Format[] = ['oci', 'maven', 'conan', 'raw'];

function CreateGroupDialog({ repos, onCreated }: { repos: Repository[]; onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [format, setFormat] = useState<Format>('oci');
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter((r) => r.format === format && r.state === 'active');

  const submit = async () => {
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({ repositoryId, position }));
    const { error: err } = await createGroup({
      body: { name: name.trim(), format, members },
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
    onCreated();
  };

  return (
    <>
      <button onClick={dialog.show} className={btnPrimary}>
        + 新建分组
      </button>
      <Modal
        open={dialog.open}
        title="新建分组"
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
          <Field label="分组名称">
            <input className={`${inputClass} font-mono`} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="格式">
            <div className="grid grid-cols-4 gap-2">
              {FORMATS.map((f) => (
                <button
                  key={f}
                  type="button"
                  onClick={() => {
                    setFormat(f);
                    setMemberIds([]);
                  }}
                  className={`rounded-md border px-3 py-2 font-mono text-sm ${
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
          <Field label="成员仓库（按选择顺序排序）" hint="分组按成员顺序解析制品">
            <div className="max-h-48 space-y-1 overflow-y-auto rounded-lg border border-zinc-800 p-2">
              {candidates.length === 0 && <div className="px-2 py-3 text-center text-xs text-zinc-600">该格式下暂无活跃仓库</div>}
              {candidates.map((r) => {
                const idx = memberIds.indexOf(r.id);
                const selected = idx >= 0;
                return (
                  <button
                    key={r.id}
                    type="button"
                    onClick={() =>
                      setMemberIds((ids) => (selected ? ids.filter((id) => id !== r.id) : [...ids, r.id]))
                    }
                    className={`flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm ${
                      selected ? 'bg-cyan-500/10 text-cyan-200' : 'text-zinc-400 hover:bg-zinc-800'
                    }`}
                  >
                    <span className="font-mono text-xs">{r.name}</span>
                    {selected && (
                      <span className="rounded-full bg-cyan-500/20 px-2 py-0.5 text-[10px] text-cyan-300">
                        #{idx + 1}
                      </span>
                    )}
                  </button>
                );
              })}
            </div>
          </Field>
        </div>
      </Modal>
    </>
  );
}

function RenameGroupDialog({ group, onSaved }: { group: Group; onSaved: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState(group.name);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    const { error: err } = await replaceGroup({
      path: { groupId: group.id },
      body: { ...group, name: name.trim() },
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
      <button
        onClick={() => {
          setName(group.name);
          dialog.show();
        }}
        className="rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 hover:bg-zinc-800"
      >
        重命名
      </button>
      <Modal
        open={dialog.open}
        title={`重命名分组：${group.name}`}
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={save} disabled={busy || !name.trim()} className={btnPrimary}>
              {busy ? '保存中…' : '保存'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="分组名称">
            <input className={`${inputClass} font-mono`} value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
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

  const repoName = (id: string) => repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + '…';
  const candidates = repos.filter((r) => r.format === group.format && r.state === 'active');

  const open = async () => {
    setError(null);
    dialog.show();
    const { data, response } = await listGroupMembers({ path: { groupId: group.id } });
    if (data) {
      const sorted = [...data].sort((a, b) => a.position - b.position);
      setMemberIds(sorted.map((m) => m.repositoryId));
    }
    const etag = response.headers.get('ETag');
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
      <button
        onClick={open}
        className="rounded border border-zinc-700 px-2.5 py-1 text-xs text-zinc-300 hover:bg-zinc-800"
      >
        编辑成员
      </button>
      <Modal
        open={dialog.open}
        title={`编辑成员：${group.name}`}
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={save} disabled={busy} className={btnPrimary}>
              {busy ? '保存中…' : '保存'}
            </button>
          </>
        }
      >
        <div className="space-y-3">
          {error !== null && <ErrorBanner error={error} />}
          <p className="text-xs text-zinc-500">按选择顺序确定成员优先级（position）。</p>
          <div className="max-h-64 space-y-1 overflow-y-auto rounded-lg border border-zinc-800 p-2">
            {candidates.map((r) => {
              const idx = memberIds.indexOf(r.id);
              const selected = idx >= 0;
              return (
                <button
                  key={r.id}
                  type="button"
                  onClick={() =>
                    setMemberIds((ids) => (selected ? ids.filter((id) => id !== r.id) : [...ids, r.id]))
                  }
                  className={`flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm ${
                    selected ? 'bg-cyan-500/10 text-cyan-200' : 'text-zinc-400 hover:bg-zinc-800'
                  }`}
                >
                  <span className="font-mono text-xs">{r.name}</span>
                  {selected && (
                    <span className="rounded-full bg-cyan-500/20 px-2 py-0.5 text-[10px] text-cyan-300">#{idx + 1}</span>
                  )}
                </button>
              );
            })}
          </div>
          {memberIds.length > 0 && (
            <p className="text-xs text-zinc-500">
              当前顺序：{memberIds.map((id) => repoName(id)).join(' → ')}
            </p>
          )}
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
  const [error, setError] = useState<unknown>(null);
  const [toDelete, setToDelete] = useState<Group | null>(null);
  const [deleting, setDeleting] = useState(false);

  const repoName = (id: string) => repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + '…';

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
    const { data } = await listGroups({ query: { pageSize: 100, pageToken: nextToken } });
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
    }
  };

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
        <Card>
          <DataTable columns={['名称', '格式', '成员（按优先级）', '版本', '']}>
            {groups.map((g) => (
              <tr key={g.id} className="group hover:bg-zinc-800/30">
                <td className="px-4 py-3 font-medium text-zinc-100">{g.name}</td>
                <td className="px-4 py-3">
                  <FormatBadge format={g.format} />
                </td>
                <td className="max-w-md px-4 py-3">
                  <div className="flex flex-wrap gap-1">
                    {[...(g.members ?? [])]
                      .sort((a, b) => a.position - b.position)
                      .map((m) => (
                        <span
                          key={m.repositoryId}
                          className="rounded-md bg-zinc-800 px-2 py-0.5 font-mono text-[11px] text-zinc-300"
                          title={m.repositoryId}
                        >
                          {m.position}. {repoName(m.repositoryId)}
                        </span>
                      ))}
                    {(g.members ?? []).length === 0 && <span className="text-xs text-zinc-600">无成员</span>}
                  </div>
                </td>
                <td className="px-4 py-3 font-mono text-xs text-zinc-500">v{g.version}</td>
                <td className="px-4 py-3">
                  <div className="flex items-center justify-end gap-2">
                    <RenameGroupDialog group={g} onSaved={load} />
                    <MembersDialog group={g} repos={repos} onSaved={load} />
                    <button
                      onClick={() => setToDelete(g)}
                      className="rounded px-2 py-1 text-xs text-zinc-600 opacity-0 hover:bg-rose-500/10 hover:text-rose-400 group-hover:opacity-100"
                    >
                      删除
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </DataTable>
          <Pagination hasMore={!!nextToken} onMore={loadMore} />
        </Card>
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
