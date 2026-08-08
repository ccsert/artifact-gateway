import { useEffect, useState } from "react";
import {
  ClockCircleOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  InboxOutlined,
  KeyOutlined,
  LoginOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SyncOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Button, Input, Menu, Space, Tooltip } from "antd";
import type { MenuProps } from "antd";
import {
  Link,
  Navigate,
  Outlet,
  useLocation,
  useNavigate,
  useSearchParams,
} from "react-router-dom";
import { useAuth } from "../lib/auth";
import { Modal, useDisclosure } from "../components/Modal";
import { Field } from "../components/Layout";

const navItems = [
  {
    to: "/",
    label: "总览",
    exact: true,
    icon: <DashboardOutlined />,
    group: "运行",
    admin: true,
  },
  {
    to: "/repositories",
    label: "仓库",
    icon: <InboxOutlined />,
    group: "运行",
    admin: true,
  },
  { to: "/search", label: "制品搜索", icon: <SearchOutlined />, group: "运行" },
  {
    to: "/operations",
    label: "任务中心",
    icon: <SyncOutlined />,
    group: "运行",
    admin: true,
  },
  {
    to: "/groups",
    label: "分组",
    icon: <TeamOutlined />,
    group: "治理",
    admin: true,
  },
  {
    to: "/access",
    label: "访问控制",
    icon: <SafetyCertificateOutlined />,
    group: "治理",
    admin: true,
  },
  {
    to: "/audits",
    label: "审计日志",
    icon: <FileSearchOutlined />,
    group: "治理",
    admin: true,
  },
  {
    to: "/audit-retention",
    label: "审计保留",
    icon: <ClockCircleOutlined />,
    group: "治理",
    admin: true,
  },
  {
    to: "/keys",
    label: "API 密钥",
    icon: <KeyOutlined />,
    group: "管理",
    admin: true,
  },
  {
    to: "/users",
    label: "用户",
    icon: <UserOutlined />,
    group: "管理",
    admin: true,
  },
] as const;

const SIDER_EXPANDED = 224;
const SIDER_COLLAPSED = 68;

function GlobalSearchBox() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [value, setValue] = useState(params.get("q") ?? "");

  useEffect(() => {
    setValue(params.get("q") ?? "");
  }, [params]);

  const search = (nextValue: string) => {
    const query = nextValue.trim();
    if (query) navigate(`/search?q=${encodeURIComponent(query)}`);
  };

  return (
    <Input.Search
      allowClear
      className="w-full max-w-md"
      placeholder="跨仓库搜索制品…"
      value={value}
      onChange={(event) => setValue(event.target.value)}
      onSearch={search}
    />
  );
}

function TokenDialog() {
  const { token, setToken, clearToken } = useAuth();
  const dialog = useDisclosure();
  const [draft, setDraft] = useState("");

  return (
    <>
      <Button
        color={token ? "green" : "orange"}
        variant="filled"
        icon={<KeyOutlined />}
        onClick={() => {
          setDraft(token);
          dialog.show();
        }}
      >
        {token ? "已配置 Token" : "设置 Token"}
      </Button>
      <Modal
        open={dialog.open}
        title="API 访问令牌"
        onClose={dialog.hide}
        footer={
          <div className="flex items-center justify-between gap-4">
            <div>
              {token && (
                <Button
                  danger
                  onClick={() => {
                    clearToken();
                    dialog.hide();
                  }}
                >
                  清除令牌
                </Button>
              )}
            </div>
            <Space>
              <Button onClick={dialog.hide}>取消</Button>
              <Button
                type="primary"
                disabled={!draft.trim()}
                onClick={() => {
                  setToken(draft);
                  window.location.reload();
                }}
              >
                保存
              </Button>
            </Space>
          </div>
        }
      >
        <Field
          label="Bearer Token"
          hint="管理 API 使用 Bearer 认证，令牌仅保存在浏览器 localStorage。"
        >
          <Input.TextArea
            className="font-mono text-xs"
            autoSize={{ minRows: 4, maxRows: 8 }}
            placeholder="粘贴 Token…"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </Field>
      </Modal>
    </>
  );
}

export function AppLayout() {
  const { token, identity, clearToken } = useAuth();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return window.localStorage.getItem("ag:sider-collapsed") === "1";
    } catch {
      return false;
    }
  });

  const toggleCollapsed = () => {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        window.localStorage.setItem("ag:sider-collapsed", next ? "1" : "0");
      } catch {
        // localStorage may be unavailable in restricted contexts.
      }
      return next;
    });
  };

  if (!token) {
    if (location.pathname === "/" || location.pathname === "/search") {
      return <Navigate to={`/browse${location.search}`} replace />;
    }
    const target = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?redirect=${target}`} replace />;
  }

  const adminOnlyPath = [
    "/",
    "/repositories",
    "/operations",
    "/groups",
    "/access",
    "/audits",
    "/audit-retention",
    "/keys",
    "/users",
  ].includes(location.pathname);
  if (identity && !identity.administrator && adminOnlyPath) {
    return <Navigate to="/search" replace />;
  }

  const visibleNavItems = navItems.filter(
    (item) => !("admin" in item) || identity?.administrator,
  );
  const selectedItem = visibleNavItems.find((item) =>
    "exact" in item
      ? location.pathname === item.to
      : location.pathname.startsWith(item.to),
  );
  const menuItems: MenuProps["items"] = (
    ["运行", "治理", "管理"] as const
  ).flatMap((group) => {
    const children = visibleNavItems
      .filter((item) => item.group === group)
      .map((item) => ({
        key: item.to,
        icon: item.icon,
        label: <Link to={item.to}>{item.label}</Link>,
      }));
    return children.length > 0
      ? [
          {
            key: `group-${group}`,
            type: "group" as const,
            label: collapsed ? "" : group,
            children,
          },
        ]
      : [];
  });

  const siderWidth = collapsed ? SIDER_COLLAPSED : SIDER_EXPANDED;

  return (
    <div className="flex min-h-screen">
      <aside
        className="ag-sider fixed inset-y-0 left-0 z-30 flex flex-col"
        style={{
          width: siderWidth,
          transition: "width 220ms cubic-bezier(0.16, 1, 0.3, 1)",
        }}
      >
        <div
          className={`flex items-center py-5 ${collapsed ? "justify-center px-0" : "gap-2.5 px-5"}`}
        >
          <div className="ag-brand-mark flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-sm font-bold text-white">
            AG
          </div>
          {!collapsed && (
            <div className="min-w-0">
              <div className="truncate text-sm font-semibold text-zinc-100">
                Artifact Gateway
              </div>
              <div className="text-[10px] uppercase tracking-widest text-zinc-600">
                Console
              </div>
            </div>
          )}
        </div>
        <Menu
          className={`ag-nav flex-1 border-0 bg-transparent ${collapsed ? "px-2" : "px-3"}`}
          mode="inline"
          theme="dark"
          inlineCollapsed={collapsed}
          selectedKeys={selectedItem ? [selectedItem.to] : []}
          items={menuItems}
        />
        <div
          className={`flex border-t border-zinc-800/60 py-2 ${
            collapsed
              ? "justify-center px-0"
              : "items-center justify-between px-4"
          }`}
        >
          {!collapsed && (
            <span className="text-[10px] leading-4 text-zinc-600">
              Native Hosted API v2
            </span>
          )}
          <Tooltip
            title={collapsed ? "展开导航" : "收起导航"}
            placement="right"
          >
            <Button
              type="text"
              size="small"
              aria-label={collapsed ? "展开导航" : "收起导航"}
              icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
              onClick={toggleCollapsed}
            />
          </Tooltip>
        </div>
      </aside>
      <div
        className="flex min-h-screen min-w-0 flex-1 flex-col"
        style={{
          marginLeft: siderWidth,
          transition: "margin-left 220ms cubic-bezier(0.16, 1, 0.3, 1)",
        }}
      >
        <header className="ag-topbar sticky top-0 z-20 flex h-14 items-center gap-3 px-6">
          <GlobalSearchBox />
          <Space className="ml-auto">
            <TokenDialog />
            <Button icon={<LoginOutlined />} onClick={clearToken}>
              退出
            </Button>
          </Space>
        </header>
        <main className="mx-auto w-full max-w-[1440px] flex-1 px-6 py-6">
          <div key={location.pathname} className="ag-page-enter">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
