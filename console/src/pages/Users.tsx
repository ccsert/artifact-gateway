import { useCallback, useEffect, useState } from "react";
import {
  DeleteOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, Input, Popconfirm, Select, Space, Switch, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { listUsers, createUser, updateUser, deleteUser } from "../client";
import type { User } from "../client";
import { PageHeader, Card, Field } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { StateBadge, Badge } from "../components/Badge";
import { Modal, ConfirmDialog, useDisclosure } from "../components/Modal";
import { formatDate } from "../lib/format";
import {
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";

type Role = "admin" | "writer" | "reader";

const ROLE_TONE: Record<Role, "red" | "blue" | "green"> = {
  admin: "red",
  writer: "blue",
  reader: "green",
};

function CreateUserDialog({ onCreated }: { onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("reader");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const submit = async () => {
    setBusy(true);
    setError(null);
    const { error: err } = await createUser({
      body: { name: name.trim(), password, role },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    setName("");
    setPassword("");
    setRole("reader");
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
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!name.trim() || password.length < 8}
            >
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label="用户名">
            <Input
              placeholder="alice"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field
            label="密码"
            hint="至少 8 个字符；创建后无法查看，请妥善保存。"
          >
            <Input.Password
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field
            label="角色"
            hint="reader 只读；writer 读写；admin 全部（含用户与密钥管理）。"
          >
            <Select<Role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                { value: "reader", label: "reader · 只读" },
                { value: "writer", label: "writer · 读写" },
                { value: "admin", label: "admin · 管理员" },
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
  const [filter, setFilter] = useState("");
  const [roleFilter, setRoleFilter] = useState<Role | "all">("all");

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
      headers: { "If-Match": user.version },
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
      headers: { "If-Match": user.version },
      body: { state: user.state === "active" ? "disabled" : "active" },
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

  const visibleUsers = (users ?? []).filter(
    (user) =>
      (!filter || user.name.toLowerCase().includes(filter.toLowerCase())) &&
      (roleFilter === "all" || user.role === roleFilter),
  );
  const activeUsers = (users ?? []).filter(
    (user) => user.state === "active",
  ).length;
  const adminUsers = (users ?? []).filter(
    (user) => user.role === "admin",
  ).length;
  const columns: ColumnsType<User> = [
    {
      title: "用户名",
      dataIndex: "name",
      key: "name",
      width: 190,
      render: (name: string) => (
        <span className="font-medium text-zinc-100">{name}</span>
      ),
    },
    {
      title: "角色",
      dataIndex: "role",
      key: "role",
      width: 260,
      render: (role: Role, user) => (
        <div className="flex items-center gap-2">
          <Badge tone={ROLE_TONE[role]}>{role}</Badge>
          <Select<Role>
            size="small"
            className="w-28"
            value={role}
            loading={busyId === user.id}
            disabled={busyId === user.id}
            onChange={(nextRole) => void changeRole(user, nextRole)}
            options={[
              { value: "reader", label: "reader" },
              { value: "writer", label: "writer" },
              { value: "admin", label: "admin" },
            ]}
          />
        </div>
      ),
    },
    {
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 210,
      render: (state: User["state"], user) => (
        <div className="flex items-center gap-2">
          <StateBadge state={state} />
          <Popconfirm
            title={state === "active" ? "确认禁用该用户？" : "确认启用该用户？"}
            description={
              state === "active"
                ? "禁用后，该用户将无法登录或调用 API。"
                : "启用后，该用户可以按当前角色重新访问系统。"
            }
            okText={state === "active" ? "禁用" : "启用"}
            cancelText="取消"
            okButtonProps={{
              danger: state === "active",
              loading: busyId === user.id,
            }}
            onConfirm={() => toggleState(user)}
          >
            <Switch
              size="small"
              checked={state === "active"}
              loading={busyId === user.id}
              aria-label={
                state === "active"
                  ? `禁用用户 ${user.name}`
                  : `启用用户 ${user.name}`
              }
              onChange={() => undefined}
            />
          </Popconfirm>
        </div>
      ),
    },
    {
      title: "创建时间",
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (createdAt: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(createdAt)}
        </span>
      ),
    },
    {
      title: "",
      key: "actions",
      width: 82,
      align: "right",
      render: (_value, user) => (
        <Button
          type="text"
          size="small"
          danger
          icon={<DeleteOutlined />}
          aria-label={`删除用户 ${user.name}`}
          onClick={() => setToDelete(user)}
        />
      ),
    },
  ];

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
          <EmptyState
            title="暂无用户"
            hint="创建用户后即可用用户名密码登录控制台"
          />
        </Card>
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: "用户总数",
                value: users.length,
                hint: `${activeUsers} 个有效账户`,
              },
              {
                label: "管理员",
                value: adminUsers,
                hint: adminUsers ? "具备全局治理权限" : "暂无管理员账户",
                tone: adminUsers ? "warning" : "default",
              },
              {
                label: "已禁用",
                value: users.length - activeUsers,
                hint: "无法登录或调用 API",
                tone: users.length - activeUsers ? "danger" : "success",
              },
            ]}
          />
          <Card>
            <FilterBar
              className="border-x-0 border-t-0 rounded-none"
              actions={
                filter || roleFilter !== "all" ? (
                  <Button
                    type="text"
                    onClick={() => {
                      setFilter("");
                      setRoleFilter("all");
                    }}
                  >
                    清除筛选
                  </Button>
                ) : undefined
              }
            >
              <FilterField label="搜索" className="min-w-[260px]">
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder="搜索用户名…"
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField label="角色" className="min-w-[150px]">
                <Select<Role | "all">
                  className="w-full"
                  value={roleFilter}
                  onChange={setRoleFilter}
                  options={[
                    { value: "all", label: "全部角色" },
                    { value: "reader", label: "reader" },
                    { value: "writer", label: "writer" },
                    { value: "admin", label: "admin" },
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleUsers.length === 0 ? (
              <EmptyState title="没有匹配的用户" hint="调整筛选条件后重试" />
            ) : (
              <Table<User>
                className="ag-console-table"
                rowKey="id"
                size="middle"
                dataSource={visibleUsers}
                columns={columns}
                pagination={false}
                scroll={{ x: 860 }}
              />
            )}
          </Card>
        </>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title="删除用户"
        message={
          <>
            确定删除用户{" "}
            <span className="font-mono text-zinc-100">{toDelete?.name}</span>{" "}
            吗？该账户的会话将立即失效。
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
