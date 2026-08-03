import { useCallback, useEffect, useState } from 'react';
import { listUsers, createUser, updateUser, deleteUser } from '../client';
import type { User } from '../client';
import { PageHeader, Card, DataTable, Field, inputClass, btnPrimary, btnSecondary, StatCard } from '../components/Layout';
import { Loading, ErrorBanner, EmptyState } from '../components/Feedback';
import { StateBadge, Badge } from '../components/Badge';
import { Modal, ConfirmDialog, useDisclosure } from '../components/Modal';
import { formatDate } from '../lib/format';

type Role = 'admin' | 'writer' | 'reader';

const ROLE_TONE: Record<Role, 'red' | 'blue' | 'green'> = {
  admin: 'red',
  writer: 'blue',
  reader: 'green',
};

function CreateUserDialog({ onCreated }: { onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState<Role>('reader');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const { error: err } = await createUser({ body: { name: name.trim(), password, role } });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    setName('');
    setPassword('');
    setRole('reader');
    onCreated();
  };

  return (
    <>
      <button onClick={dialog.show} className={btnPrimary}>
        + 新建用户
      </button>
      <Modal
        open={dialog.open}
        title="新建用户"
        onClose={dialog.hide}
        footer={
          <>
            <button onClick={dialog.hide} className={btnSecondary}>
              取消
            </button>
            <button onClick={submit} disabled={busy || !name.trim() || password.length < 8} className={btnPrimary}>
              {busy ? '创建中…' : '创建'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label="用户名">
            <input className={inputClass} placeholder="alice" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="密码" hint="至少 8 个字符；创建后无法查看，请妥善保存。">
            <input
              type="password"
              className={inputClass}
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field label="角色" hint="reader 只读；writer 读写；admin 全部（含用户与密钥管理）。">
            <select className={inputClass} value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="reader">reader · 只读</option>
              <option value="writer">writer · 读写</option>
              <option value="admin">admin · 管理员</option>
            </select>
          </Field>
        </div>
      </Modal>
    </>
  );
}

export function UsersPage() {
  const [users, setUsers] = useState<User[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [toDelete, setToDelete] = useState<User | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [filter, setFilter] = useState('');
  const [roleFilter, setRoleFilter] = useState<Role | 'all'>('all');

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await listUsers();
    if (err) {
      setError(err);
      return;
    }
    setUsers(data?.items ?? []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const changeRole = async (user: User, role: Role) => {
    setBusyId(user.id);
    setError(null);
    const { error: err } = await updateUser({
      path: { userId: user.id },
      headers: { 'If-Match': user.version },
      body: { role },
    });
    setBusyId(null);
    if (err) {
      setError(err);
      return;
    }
    void load();
  };

  const toggleState = async (user: User) => {
    setBusyId(user.id);
    setError(null);
    const { error: err } = await updateUser({
      path: { userId: user.id },
      headers: { 'If-Match': user.version },
      body: { state: user.state === 'active' ? 'disabled' : 'active' },
    });
    setBusyId(null);
    if (err) {
      setError(err);
      return;
    }
    void load();
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    const { error: err } = await deleteUser({ path: { userId: toDelete.id } });
    setDeleting(false);
    if (!err) {
      setToDelete(null);
      void load();
    } else {
      setError(err);
    }
  };

  const visibleUsers = (users ?? []).filter((user) =>
    (!filter || user.name.toLowerCase().includes(filter.toLowerCase())) &&
    (roleFilter === 'all' || user.role === roleFilter),
  );
  const activeUsers = (users ?? []).filter((user) => user.state === 'active').length;
  const adminUsers = (users ?? []).filter((user) => user.role === 'admin').length;

  return (
    <div>
      <PageHeader
        title="用户"
        description="本地用户账户（用户名 + 密码 + 角色）。禁用或删除后立即失效。"
        actions={<CreateUserDialog onCreated={load} />}
      />
      {error ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !users ? (
        <Loading />
      ) : users.length === 0 ? (
        <Card>
          <EmptyState title="暂无用户" hint="创建用户后即可用用户名密码登录控制台" />
        </Card>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-3 sm:grid-cols-3">
            <StatCard label="用户总数" value={users.length} sub={`${activeUsers} 个有效账户`} />
            <StatCard label="管理员" value={adminUsers} sub={adminUsers ? '具备全局治理权限' : '暂无管理员账户'} />
            <StatCard label="已禁用" value={users.length - activeUsers} sub="无法登录或调用 API" />
          </div>
          <Card>
          <div className="flex flex-wrap items-center gap-2 border-b border-zinc-800/80 px-4 py-3">
            <input className={`${inputClass} w-56`} placeholder="搜索用户名…" value={filter} onChange={(e) => setFilter(e.target.value)} />
            <select className={`${inputClass} w-auto min-w-28`} value={roleFilter} onChange={(e) => setRoleFilter(e.target.value as Role | 'all')}>
              <option value="all">全部角色</option><option value="reader">reader</option><option value="writer">writer</option><option value="admin">admin</option>
            </select>
          </div>
          {visibleUsers.length === 0 ? <EmptyState title="没有匹配的用户" hint="调整筛选条件后重试" /> : <DataTable columns={['用户名', '角色', '状态', '创建时间', '']}>
            {visibleUsers.map((u) => (
              <tr key={u.id} className="hover:bg-zinc-800/30">
                <td className="px-4 py-3 font-medium text-zinc-100">{u.name}</td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <Badge tone={ROLE_TONE[u.role]}>{u.role}</Badge>
                    <select
                      value={u.role}
                      disabled={busyId === u.id}
                      onChange={(e) => void changeRole(u, e.target.value as Role)}
                      className="rounded border border-zinc-700 bg-zinc-800/60 px-1.5 py-0.5 text-xs text-zinc-300 disabled:opacity-50"
                    >
                      <option value="reader">reader</option>
                      <option value="writer">writer</option>
                      <option value="admin">admin</option>
                    </select>
                  </div>
                </td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <StateBadge state={u.state} />
                    <button
                      onClick={() => void toggleState(u)}
                      disabled={busyId === u.id}
                      className="rounded border border-zinc-700 px-2 py-0.5 text-[11px] text-zinc-400 hover:bg-zinc-800 disabled:opacity-50"
                    >
                      {u.state === 'active' ? '禁用' : '启用'}
                    </button>
                  </div>
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(u.createdAt)}</td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => setToDelete(u)}
                    className="rounded border border-rose-500/40 px-2.5 py-1 text-xs text-rose-300 hover:bg-rose-500/10"
                  >
                    删除
                  </button>
                </td>
              </tr>
            ))}
          </DataTable>}
        </Card>
        </>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title="删除用户"
        message={
          <>
            确定删除用户 <span className="font-mono text-zinc-100">{toDelete?.name}</span> 吗？该账户的会话将立即失效。
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
