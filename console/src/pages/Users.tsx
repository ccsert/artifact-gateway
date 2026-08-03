import { useCallback, useEffect, useState } from 'react';
import { DeleteOutlined, PlusOutlined, SearchOutlined } from '@ant-design/icons';
import { Button, Input, Popconfirm, Select, Space, Switch } from 'antd';
import { listUsers, createUser, updateUser, deleteUser } from '../client';
import type { User } from '../client';
import { PageHeader, Card, DataTable, Field, StatCard } from '../components/Layout';
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
      <Button
        type="primary"
        icon={<PlusOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        新建用户
      </Button>
      <Modal
        open={dialog.open}
        title="新建用户"
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button type="primary" onClick={submit} loading={busy} disabled={!name.trim() || password.length < 8}>
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label="用户名">
            <Input placeholder="alice" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field label="密码" hint="至少 8 个字符；创建后无法查看，请妥善保存。">
            <Input.Password
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field label="角色" hint="reader 只读；writer 读写；admin 全部（含用户与密钥管理）。">
            <Select<Role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                { value: 'reader', label: 'reader · 只读' },
                { value: 'writer', label: 'writer · 读写' },
                { value: 'admin', label: 'admin · 管理员' },
              ]}
            />
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
          <Space wrap className="!flex w-full border-b border-zinc-800/80 px-4 py-3">
            <Input allowClear prefix={<SearchOutlined />} className="w-72" placeholder="搜索用户名…" value={filter} onChange={(e) => setFilter(e.target.value)} />
            <Select<Role | 'all'>
              className="w-36"
              value={roleFilter}
              onChange={setRoleFilter}
              options={[
                { value: 'all', label: '全部角色' },
                { value: 'reader', label: 'reader' },
                { value: 'writer', label: 'writer' },
                { value: 'admin', label: 'admin' },
              ]}
            />
          </Space>
          {visibleUsers.length === 0 ? <EmptyState title="没有匹配的用户" hint="调整筛选条件后重试" /> : <DataTable columns={['用户名', '角色', '状态', '创建时间', '']}>
            {visibleUsers.map((u) => (
              <tr key={u.id} className="hover:bg-zinc-800/30">
                <td className="px-4 py-3 font-medium text-zinc-100">{u.name}</td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <Badge tone={ROLE_TONE[u.role]}>{u.role}</Badge>
                    <Select<Role>
                      size="small"
                      className="w-28"
                      value={u.role}
                      loading={busyId === u.id}
                      disabled={busyId === u.id}
                      onChange={(role) => void changeRole(u, role)}
                      options={[
                        { value: 'reader', label: 'reader' },
                        { value: 'writer', label: 'writer' },
                        { value: 'admin', label: 'admin' },
                      ]}
                    />
                  </div>
                </td>
                <td className="px-4 py-3">
                  <div className="flex items-center gap-2">
                    <StateBadge state={u.state} />
                    <Popconfirm
                      title={u.state === 'active' ? '确认禁用该用户？' : '确认启用该用户？'}
                      description={u.state === 'active' ? '禁用后，该用户将无法登录或调用 API。' : '启用后，该用户可以按当前角色重新访问系统。'}
                      okText={u.state === 'active' ? '禁用' : '启用'}
                      cancelText="取消"
                      okButtonProps={{ danger: u.state === 'active', loading: busyId === u.id }}
                      onConfirm={() => toggleState(u)}
                    >
                      <Switch
                        size="small"
                        checked={u.state === 'active'}
                        loading={busyId === u.id}
                        aria-label={u.state === 'active' ? `禁用用户 ${u.name}` : `启用用户 ${u.name}`}
                        onChange={() => undefined}
                      />
                    </Popconfirm>
                  </div>
                </td>
                <td className="whitespace-nowrap px-4 py-3 text-xs text-zinc-500">{formatDate(u.createdAt)}</td>
                <td className="px-4 py-3 text-right">
                  <Button
                    size="small"
                    danger
                    icon={<DeleteOutlined />}
                    onClick={() => setToDelete(u)}
                  >
                    删除
                  </Button>
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
