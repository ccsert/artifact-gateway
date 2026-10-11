import { useEffect, useState } from "react";
import {
  LockOutlined,
  MoreOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, Dropdown, Input, Select, Space, Tooltip } from "antd";
import type { ColumnsType, TablePaginationConfig } from "antd/es/table";
import type { User } from "../../client";
import { Badge, StateBadge } from "../../components/ui/Badge";
import { EmptyState, ErrorBanner } from "../../components/ui/Feedback";
import { Card, PageHeader } from "../../components/ui/Layout";
import {
  ConsoleTable,
  FilterBar,
  FilterField,
} from "../../components/ui/ConsolePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { UserCreateDialog } from "./users/UserCreateDialog";
import { UserDetailsDrawer } from "./users/UserDetailsDrawer";
import { isUserLocked, roleTone } from "./users/userPresentation";
import { CONSOLE_PAGE_SIZE } from "../../components/ui/useConsolePagination";
import { useUserChanged, useUserPage } from "./userQueries";

type Role = User["role"];
type UserState = User["state"];

export function UsersPage() {
  const { locale, t } = usePreferences();
  const [searchInput, setSearchInput] = useState("");
  const [search, setSearch] = useState("");
  const [role, setRole] = useState<Role | undefined>();
  const [state, setState] = useState<UserState | undefined>();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(CONSOLE_PAGE_SIZE);
  const [createOpen, setCreateOpen] = useState(false);
  const [selectedUser, setSelectedUser] = useState<User | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchInput.trim());
      setPage(1);
    }, 300);
    return () => window.clearTimeout(timer);
  }, [searchInput]);

  const usersQuery = useUserPage({
    search: search || undefined,
    role,
    state,
    limit: pageSize,
    offset: (page - 1) * pageSize,
  });
  const users = usersQuery.data?.items ?? [];
  const total = usersQuery.data?.total ?? 0;
  const loading = usersQuery.isFetching;
  const error = usersQuery.error;
  const userChanged = useUserChanged();
  const load = () => void usersQuery.refetch();

  const updateVisibleUser = (updated: User) => {
    setSelectedUser(updated);
    void userChanged(updated);
  };

  const handleDeleted = () => {
    setSelectedUser(null);
    if (users.length === 1 && page > 1) {
      setPage((current) => current - 1);
    }
    void userChanged();
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
      title: t("identity.user"),
      key: "identity",
      width: 270,
      render: (_value, user) => (
        <div className="min-w-0">
          <div className="truncate font-medium text-fg-strong">
            {user.displayName || user.name}
          </div>
          <div className="mt-0.5 truncate text-xs text-fg-tertiary">
            <span className="font-mono">{user.name}</span>
            {user.email ? ` · ${user.email}` : ""}
          </div>
        </div>
      ),
    },
    {
      title: t("identity.role"),
      dataIndex: "role",
      key: "role",
      width: 110,
      render: (value: Role) => (
        <Badge tone={roleTone(value)}>
          {value === "none" ? t("identity.pending") : value}
        </Badge>
      ),
    },
    {
      title: t("identity.access"),
      key: "access",
      width: 150,
      render: (_value, user) =>
        isUserLocked(user) ? (
          <Space size={6}>
            <Badge tone="warning">
              <LockOutlined /> {t("identity.locked")}
            </Badge>
          </Space>
        ) : (
          <StateBadge state={user.state} />
        ),
    },
    {
      title: t("identity.lastSignIn"),
      dataIndex: "lastLoginAt",
      key: "lastLoginAt",
      width: 190,
      render: (value?: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {value ? formatDate(value, locale) : t("identity.neverSignedIn")}
        </span>
      ),
    },
    {
      title: t("identity.password"),
      key: "password",
      width: 180,
      render: (_value, user) => (
        <div className="text-xs">
          <div className="text-fg-secondary">
            {!user.localPasswordEnabled
              ? t("identity.ssoOnly")
              : user.mustChangePassword
                ? t("identity.changeRequired")
                : t("identity.ready")}
          </div>
          {user.localPasswordEnabled && user.passwordChangedAt ? (
            <div className="mt-0.5 text-fg-disabled">
              {t("identity.changed")}{" "}
              {formatDate(user.passwordChangedAt, locale)}
            </div>
          ) : null}
        </div>
      ),
    },
    {
      title: t("identity.created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 190,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: t("identity.actions"),
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
                label: t("identity.viewAndManage"),
              },
            ],
            onClick: () => setSelectedUser(user),
          }}
        >
          <Tooltip title={t("identity.userActions")}>
            <Button
              type="text"
              size="small"
              icon={<MoreOutlined />}
              aria-label={t("identity.manageUser", { user: String(user.name) })}
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
    onChange: (nextPage, nextPageSize) => {
      setPageSize(nextPageSize);
      setPage(nextPageSize === pageSize ? nextPage : 1);
    },
  };

  const filtersActive = Boolean(searchInput || role || state);

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("identity.users")}
        description={t("identity.manageLocalAndSsoAccountsSignInStatusRoles")}
        actions={
          <Button
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => setCreateOpen(true)}
          >
            {t("identity.newUser")}
          </Button>
        }
      />

      {error ? <ErrorBanner error={error} onRetry={load} /> : null}

      <Card>
        <FilterBar
          className="rounded-none border-x-0 border-t-0"
          actions={
            filtersActive ? (
              <Button type="text" onClick={clearFilters}>
                {t("identity.clearFilters")}
              </Button>
            ) : undefined
          }
        >
          <FilterField label={t("identity.search")} className="w-[320px]">
            <Input
              allowClear
              prefix={<SearchOutlined />}
              placeholder={t("identity.searchUsernameDisplayNameOrEmail")}
              value={searchInput}
              onChange={(event) => setSearchInput(event.target.value)}
            />
          </FilterField>
          <FilterField label={t("identity.role")} className="w-[160px]">
            <Select<Role>
              allowClear
              className="w-full"
              placeholder={t("identity.allRoles")}
              value={role}
              onChange={(value) => {
                setRole(value);
                setPage(1);
              }}
              options={[
                { value: "none", label: t("identity.awaitingApproval") },
                { value: "member", label: "member" },
                { value: "admin", label: "admin" },
              ]}
            />
          </FilterField>
          <FilterField label={t("identity.accountState")} className="w-[170px]">
            <Select<UserState>
              allowClear
              className="w-full"
              placeholder={t("identity.allStates")}
              value={state}
              onChange={(value) => {
                setState(value);
                setPage(1);
              }}
              options={[
                { value: "active", label: t("identity.active") },
                { value: "disabled", label: t("identity.disabled") },
              ]}
            />
          </FilterField>
        </FilterBar>

        <ConsoleTable<User>
          rowKey="id"
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
                    ? t("identity.noMatchingUsers")
                    : t("identity.noUsers")
                }
                hint={
                  filtersActive
                    ? t("identity.adjustTheFiltersAndTryAgain")
                    : t("identity.createTheFirstLocalAccount")
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
          void userChanged();
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
