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
import { usePreferences } from "../lib/preferences";

type Role = "admin" | "writer" | "reader";

const ROLE_TONE: Record<Role, "red" | "blue" | "green"> = {
  admin: "red",
  writer: "blue",
  reader: "green",
};

function CreateUserDialog({ onCreated }: { onCreated: () => void }) {
  const { text } = usePreferences();
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
        {text("新建用户", "New user")}
      </Button>
      <Modal
        open={dialog.open}
        title={text("新建用户", "New user")}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={!name.trim() || password.length < 8}
            >
              {text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error ? <ErrorBanner error={error} /> : null}
          <Field label={text("用户名", "Username")}>
            <Input
              placeholder="alice"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field
            label={text("密码", "Password")}
            hint={text(
              "至少 8 个字符；创建后无法查看，请妥善保存。",
              "At least 8 characters. It cannot be viewed after creation.",
            )}
          >
            <Input.Password
              placeholder="••••••••"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Field
            label={text("角色", "Role")}
            hint={text(
              "reader 只读；writer 读写；admin 全部（含用户与密钥管理）。",
              "Reader is read-only, writer can read and write, and admin can also manage users and keys.",
            )}
          >
            <Select<Role>
              className="w-full"
              value={role}
              onChange={setRole}
              options={[
                {
                  value: "reader",
                  label: text("reader · 只读", "reader · read-only"),
                },
                {
                  value: "writer",
                  label: text("writer · 读写", "writer · read/write"),
                },
                {
                  value: "admin",
                  label: text("admin · 管理员", "admin · administrator"),
                },
              ]}
            />
          </Field>
        </div>
      </Modal>
    </>
  );
}

export function UsersPage() {
  const { locale, text } = usePreferences();
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
      title: text("用户名", "Username"),
      dataIndex: "name",
      key: "name",
      width: 190,
      render: (name: string) => (
        <span className="font-medium text-zinc-100">{name}</span>
      ),
    },
    {
      title: text("角色", "Role"),
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
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 210,
      render: (state: User["state"], user) => (
        <div className="flex items-center gap-2">
          <StateBadge state={state} />
          <Popconfirm
            title={
              state === "active"
                ? text("确认禁用该用户？", "Disable this user?")
                : text("确认启用该用户？", "Enable this user?")
            }
            description={
              state === "active"
                ? text(
                    "禁用后，该用户将无法登录或调用 API。",
                    "The user will no longer be able to sign in or call APIs.",
                  )
                : text(
                    "启用后，该用户可以按当前角色重新访问系统。",
                    "The user regains access with the current role.",
                  )
            }
            okText={
              state === "active"
                ? text("禁用", "Disable")
                : text("启用", "Enable")
            }
            cancelText={text("取消", "Cancel")}
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
                  ? text(`禁用用户 ${user.name}`, `Disable user ${user.name}`)
                  : text(`启用用户 ${user.name}`, `Enable user ${user.name}`)
              }
              onChange={() => undefined}
            />
          </Popconfirm>
        </div>
      ),
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (createdAt: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(createdAt, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      width: 82,
      align: "right",
      render: (_value, user) => (
        <Button
          type="text"
          size="small"
          danger
          icon={<DeleteOutlined />}
          aria-label={text(`删除用户 ${user.name}`, `Delete user ${user.name}`)}
          onClick={() => setToDelete(user)}
        />
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={text("用户", "Users")}
        description={text(
          "本地用户账户（用户名 + 密码 + 角色）。禁用或删除后立即失效。",
          "Local username, password, and role accounts. Disable or delete to revoke access immediately.",
        )}
        actions={<CreateUserDialog onCreated={load} />}
      />
      {error ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : !users ? (
        <Loading />
      ) : users.length === 0 ? (
        <Card>
          <EmptyState
            title={text("暂无用户", "No users")}
            hint={text(
              "创建用户后即可用用户名密码登录控制台",
              "Create a user to enable username and password sign-in",
            )}
          />
        </Card>
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: text("用户总数", "Users"),
                value: users.length,
                hint: text(
                  `${activeUsers} 个有效账户`,
                  `${activeUsers} active accounts`,
                ),
              },
              {
                label: text("管理员", "Administrators"),
                value: adminUsers,
                hint: adminUsers
                  ? text("具备全局治理权限", "Have global governance access")
                  : text("暂无管理员账户", "No administrator accounts"),
                tone: adminUsers ? "warning" : "default",
              },
              {
                label: text("已禁用", "Disabled"),
                value: users.length - activeUsers,
                hint: text("无法登录或调用 API", "Cannot sign in or call APIs"),
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
                    {text("清除筛选", "Clear filters")}
                  </Button>
                ) : undefined
              }
            >
              <FilterField
                label={text("搜索", "Search")}
                className="min-w-[260px]"
              >
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder={text("搜索用户名…", "Search usernames…")}
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                />
              </FilterField>
              <FilterField
                label={text("角色", "Role")}
                className="min-w-[150px]"
              >
                <Select<Role | "all">
                  className="w-full"
                  value={roleFilter}
                  onChange={setRoleFilter}
                  options={[
                    { value: "all", label: text("全部角色", "All roles") },
                    { value: "reader", label: "reader" },
                    { value: "writer", label: "writer" },
                    { value: "admin", label: "admin" },
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleUsers.length === 0 ? (
              <EmptyState
                title={text("没有匹配的用户", "No matching users")}
                hint={text(
                  "调整筛选条件后重试",
                  "Adjust the filters and try again",
                )}
              />
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
        title={text("删除用户", "Delete user")}
        message={
          <>
            {text("确定删除用户", "Delete user")}{" "}
            <span className="font-mono text-zinc-100">{toDelete?.name}</span>{" "}
            {text(
              "吗？该账户的会话将立即失效。",
              "? The account's sessions become invalid immediately.",
            )}
          </>
        }
        confirmLabel={text("删除", "Delete")}
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
