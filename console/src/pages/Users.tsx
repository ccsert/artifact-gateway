import { useCallback, useEffect, useRef, useState } from "react";
import {
  LockOutlined,
  MoreOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, Dropdown, Input, Select, Space, Table, Tooltip } from "antd";
import type { ColumnsType, TablePaginationConfig } from "antd/es/table";
import { listUsers } from "../client";
import type { User } from "../client";
import { Badge, StateBadge } from "../components/Badge";
import { EmptyState, ErrorBanner } from "../components/Feedback";
import { Card, PageHeader } from "../components/Layout";
import { FilterBar, FilterField } from "../components/ConsolePrimitives";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { UserCreateDialog } from "./users/UserCreateDialog";
import { UserDetailsDrawer } from "./users/UserDetailsDrawer";
import { isUserLocked, roleTone } from "./users/userPresentation";

type Role = User["role"];
type UserState = User["state"];

const DEFAULT_PAGE_SIZE = 20;

export function UsersPage() {
  const { locale, text } = usePreferences();
  const requestSequence = useRef(0);
  const [users, setUsers] = useState<User[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [role, setRole] = useState<Role | undefined>();
  const [state, setState] = useState<UserState | undefined>();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(DEFAULT_PAGE_SIZE);
  const [createOpen, setCreateOpen] = useState(false);
  const [selectedUser, setSelectedUser] = useState<User | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim());
      setPage(1);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const load = useCallback(async () => {
    const sequence = ++requestSequence.current;
    setLoading(true);
    setError(null);
    const { data, error: requestError } = await listUsers({
      query: {
        search: search || undefined,
        role,
        state,
        limit: pageSize,
        offset: (page - 1) * pageSize,
      },
    });
    if (sequence !== requestSequence.current) return;
    setLoading(false);
    if (requestError) {
      setError(requestError);
      return;
    }
    setUsers(data?.items ?? []);
    setTotal(data?.total ?? 0);
  }, [page, pageSize, role, search, state]);

  useEffect(() => {
    void load();
  }, [load]);

  const updateVisibleUser = (updated: User) => {
    setUsers((current) =>
      current.map((user) => (user.id === updated.id ? updated : user)),
    );
    setSelectedUser(updated);
    void load();
  };

  const handleDeleted = () => {
    setSelectedUser(null);
    if (users.length === 1 && page > 1) {
      setPage((current) => current - 1);
      return;
    }
    void load();
  };

  const clearFilters = () => {
    setSearchInput("");
    setSearch("");
    setRole(undefined);
    setState(undefined);
    setPage(1);
  };

  const columns: ColumnsType<User> = [
    {
      title: text("用户", "User"),
      key: "identity",
      width: 270,
      render: (_value, user) => (
        <div className="min-w-0">
          <div className="truncate font-medium text-zinc-100">
            {user.displayName || user.name}
          </div>
          <div className="mt-0.5 truncate text-xs text-zinc-500">
            <span className="font-mono">{user.name}</span>
            {user.email ? ` · ${user.email}` : ""}
          </div>
        </div>
      ),
    },
    {
      title: text("角色", "Role"),
      dataIndex: "role",
      key: "role",
      width: 110,
      render: (value: Role) => <Badge tone={roleTone(value)}>{value}</Badge>,
    },
    {
      title: text("访问状态", "Access"),
      key: "access",
      width: 150,
      render: (_value, user) =>
        isUserLocked(user) ? (
          <Space size={6}>
            <Badge tone="warning">
              <LockOutlined /> {text("已锁定", "Locked")}
            </Badge>
          </Space>
        ) : (
          <StateBadge state={user.state} />
        ),
    },
    {
      title: text("最后登录", "Last sign-in"),
      dataIndex: "lastLoginAt",
      key: "lastLoginAt",
      width: 190,
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {value
            ? formatDate(value, locale)
            : text("从未登录", "Never signed in")}
        </span>
      ),
    },
    {
      title: text("密码状态", "Password"),
      key: "password",
      width: 180,
      render: (_value, user) => (
        <div className="text-xs">
          <div className="text-zinc-400">
            {user.mustChangePassword
              ? text("等待用户修改", "Change required")
              : text("可正常使用", "Ready")}
          </div>
          <div className="mt-0.5 text-zinc-600">
            {text("更新于", "Changed")}{" "}
            {formatDate(user.passwordChangedAt, locale)}
          </div>
        </div>
      ),
    },
    {
      title: text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      width: 76,
      align: "right",
      fixed: "right",
      render: (_value, user) => (
        <Dropdown
          trigger={["click"]}
          menu={{
            items: [
              {
                key: "manage",
                label: text("查看与管理", "View and manage"),
              },
            ],
            onClick: () => setSelectedUser(user),
          }}
        >
          <Tooltip title={text("用户操作", "User actions")}>
            <Button
              type="text"
              size="small"
              icon={<MoreOutlined />}
              aria-label={text(
                `管理用户 ${user.name}`,
                `Manage user ${user.name}`,
              )}
              onClick={(event) => event.stopPropagation()}
            />
          </Tooltip>
        </Dropdown>
      ),
    },
  ];

  const pagination: TablePaginationConfig = {
    current: page,
    pageSize,
    total,
    showSizeChanger: true,
    pageSizeOptions: [20, 50, 100],
    showTotal: (count, range) =>
      text(
        `第 ${range[0]}-${range[1]} 项，共 ${count} 个用户`,
        `${range[0]}-${range[1]} of ${count} users`,
      ),
    onChange: (nextPage, nextPageSize) => {
      setPageSize(nextPageSize);
      setPage(nextPageSize === pageSize ? nextPage : 1);
    },
  };

  const filtersActive = Boolean(searchInput || role || state);

  return (
    <div>
      <PageHeader
        title={text("用户", "Users")}
        description={text(
          "管理本地账户、登录状态、密码生命周期和有效会话。",
          "Manage local accounts, sign-in status, password lifecycle, and active sessions.",
        )}
        actions={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setCreateOpen(true)}
          >
            {text("新建用户", "New user")}
          </Button>
        }
      />

      {error ? <ErrorBanner error={error} onRetry={load} /> : null}

      <Card className={error ? "mt-4" : ""}>
        <FilterBar
          className="rounded-none border-x-0 border-t-0"
          actions={
            filtersActive ? (
              <Button type="text" onClick={clearFilters}>
                {text("清除筛选", "Clear filters")}
              </Button>
            ) : undefined
          }
        >
          <FilterField label={text("搜索", "Search")} className="w-[320px]">
            <Input
              allowClear
              prefix={<SearchOutlined />}
              placeholder={text(
                "搜索用户名、显示名或邮箱…",
                "Search username, display name, or email…",
              )}
              value={searchInput}
              onChange={(event) => setSearchInput(event.target.value)}
            />
          </FilterField>
          <FilterField label={text("角色", "Role")} className="w-[160px]">
            <Select<Role>
              allowClear
              className="w-full"
              placeholder={text("全部角色", "All roles")}
              value={role}
              onChange={(value) => {
                setRole(value);
                setPage(1);
              }}
              options={[
                { value: "reader", label: "reader" },
                { value: "writer", label: "writer" },
                { value: "admin", label: "admin" },
              ]}
            />
          </FilterField>
          <FilterField
            label={text("账户状态", "Account state")}
            className="w-[170px]"
          >
            <Select<UserState>
              allowClear
              className="w-full"
              placeholder={text("全部状态", "All states")}
              value={state}
              onChange={(value) => {
                setState(value);
                setPage(1);
              }}
              options={[
                { value: "active", label: text("有效", "Active") },
                { value: "disabled", label: text("已停用", "Disabled") },
              ]}
            />
          </FilterField>
        </FilterBar>

        <Table<User>
          className="ag-console-table"
          rowKey="id"
          size="middle"
          dataSource={users}
          columns={columns}
          loading={loading}
          pagination={pagination}
          scroll={{ x: 1160, y: "calc(100vh - 350px)" }}
          locale={{
            emptyText: (
              <EmptyState
                title={
                  filtersActive
                    ? text("没有匹配的用户", "No matching users")
                    : text("暂无用户", "No users")
                }
                hint={
                  filtersActive
                    ? text(
                        "调整筛选条件后重试",
                        "Adjust the filters and try again",
                      )
                    : text(
                        "创建第一个本地账户",
                        "Create the first local account",
                      )
                }
              />
            ),
          }}
          onRow={(user) => ({
            className: "cursor-pointer",
            onClick: () => setSelectedUser(user),
          })}
        />
      </Card>

      <UserCreateDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={() => {
          setCreateOpen(false);
          setPage(1);
          void load();
        }}
      />
      <UserDetailsDrawer
        user={selectedUser}
        onClose={() => setSelectedUser(null)}
        onChanged={updateVisibleUser}
        onDeleted={handleDeleted}
      />
    </div>
  );
}
