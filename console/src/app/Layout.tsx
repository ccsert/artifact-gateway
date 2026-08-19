import { useEffect, useState } from "react";
import {
  ClockCircleOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  InboxOutlined,
  KeyOutlined,
  LoginOutlined,
  MenuOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SyncOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Button, Drawer, Input, Menu, Space, Tooltip } from "antd";
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
import { Loading } from "../components/Feedback";
import { PreferenceControls } from "../components/PreferenceControls";
import { usePreferences } from "../lib/preferences";

const navItems = [
  {
    to: "/",
    label: "nav.dashboard",
    exact: true,
    icon: <DashboardOutlined />,
    group: "runtime",
    admin: true,
  },
  {
    to: "/repositories",
    label: "nav.repositories",
    icon: <InboxOutlined />,
    group: "runtime",
    admin: true,
  },
  {
    to: "/search",
    label: "nav.search",
    icon: <SearchOutlined />,
    group: "runtime",
  },
  {
    to: "/operations",
    label: "nav.operations",
    icon: <SyncOutlined />,
    group: "runtime",
    admin: true,
  },
  {
    to: "/groups",
    label: "nav.groups",
    icon: <TeamOutlined />,
    group: "governance",
    admin: true,
  },
  {
    to: "/access",
    label: "nav.access",
    icon: <SafetyCertificateOutlined />,
    group: "governance",
    admin: true,
  },
  {
    to: "/audits",
    label: "nav.audits",
    icon: <FileSearchOutlined />,
    group: "governance",
    admin: true,
  },
  {
    to: "/audit-retention",
    label: "nav.auditRetention",
    icon: <ClockCircleOutlined />,
    group: "governance",
    admin: true,
  },
  {
    to: "/identity-providers",
    label: "nav.authentication",
    icon: <LoginOutlined />,
    group: "management",
    admin: true,
  },
  {
    to: "/keys",
    label: "nav.apiKeys",
    icon: <KeyOutlined />,
    group: "management",
    admin: true,
  },
  {
    to: "/service-accounts",
    label: "nav.serviceAccounts",
    icon: <RobotOutlined />,
    group: "management",
    admin: true,
  },
  {
    to: "/users",
    label: "nav.users",
    icon: <UserOutlined />,
    group: "management",
    admin: true,
  },
] as const;

function BrandLockup({ collapsed = false }: { collapsed?: boolean }) {
  return (
    <div
      className="ag-brand-lockup flex items-center"
      data-collapsed={collapsed ? "true" : "false"}
    >
      <div className="ag-brand-mark flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-sm font-bold text-white">
        AG
      </div>
      <div className="ag-brand-copy min-w-0">
        <div className="truncate text-sm font-semibold text-zinc-100">
          Artifact Gateway
        </div>
        <div className="text-xs uppercase tracking-widest text-zinc-600">
          Console
        </div>
      </div>
    </div>
  );
}

function GlobalSearchBox() {
  const { t } = usePreferences();
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
      className="ag-global-search w-full max-w-md"
      placeholder={t("header.search")}
      value={value}
      onChange={(event) => setValue(event.target.value)}
      onSearch={search}
    />
  );
}

function TokenDialog() {
  const { token, setToken, clearToken } = useAuth();
  const { t } = usePreferences();
  const dialog = useDisclosure();
  const [draft, setDraft] = useState("");

  return (
    <>
      <Button
        color={token ? "green" : "orange"}
        variant="filled"
        icon={<KeyOutlined />}
        aria-label={token ? t("auth.tokenConfigured") : t("auth.setToken")}
        onClick={() => {
          setDraft(token);
          dialog.show();
        }}
      >
        <span className="ag-token-label">
          {token ? t("auth.tokenConfigured") : t("auth.setToken")}
        </span>
      </Button>
      <Modal
        open={dialog.open}
        title={t("auth.tokenDialog")}
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
                  {t("auth.clearToken")}
                </Button>
              )}
            </div>
            <Space>
              <Button onClick={dialog.hide}>{t("common.cancel")}</Button>
              <Button
                type="primary"
                disabled={!draft.trim()}
                onClick={() => {
                  setToken(draft);
                  window.location.reload();
                }}
              >
                {t("auth.saveToken")}
              </Button>
            </Space>
          </div>
        }
      >
        <Field label="Bearer Token" hint={t("auth.tokenDialogHint")}>
          <Input.TextArea
            className="font-mono text-xs"
            autoSize={{ minRows: 4, maxRows: 8 }}
            placeholder={t("auth.tokenPlaceholder")}
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
          />
        </Field>
      </Modal>
    </>
  );
}

export function AppLayout() {
  const { authenticated, identity, identityLoading, clearToken } = useAuth();
  const { colorMode, t } = usePreferences();
  const location = useLocation();
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
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

  useEffect(() => {
    setMobileNavOpen(false);
  }, [location.pathname]);

  if (identityLoading) {
    return (
      <div className="ag-app-fallback flex min-h-screen items-center justify-center">
        <Loading label={t("common.loading")} />
      </div>
    );
  }

  if (!authenticated) {
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
    "/identity-providers",
    "/keys",
    "/service-accounts",
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
  const createMenuItems = (): MenuProps["items"] =>
    (
      [
        { key: "runtime", label: "nav.runtime" },
        { key: "governance", label: "nav.governance" },
        { key: "management", label: "nav.management" },
      ] as const
    ).flatMap((group) => {
      const children = visibleNavItems
        .filter((item) => item.group === group.key)
        .map((item) => ({
          key: item.to,
          icon: item.icon,
          label: <Link to={item.to}>{t(item.label)}</Link>,
        }));
      return children.length > 0
        ? [
            {
              key: `group-${group.key}`,
              type: "group" as const,
              label: t(group.label),
              children,
            },
          ]
        : [];
    });

  const menuItems = createMenuItems();

  return (
    <div
      className="ag-shell flex min-h-screen"
      data-sider-collapsed={collapsed ? "true" : "false"}
    >
      <aside
        className="ag-sider ag-sider-desktop fixed inset-y-0 left-0 z-30 flex flex-col"
        data-collapsed={collapsed ? "true" : "false"}
      >
        <BrandLockup collapsed={collapsed} />
        <Menu
          className="ag-nav ag-desktop-nav flex-1 border-0 bg-transparent"
          mode="inline"
          theme={colorMode}
          inlineCollapsed={collapsed}
          selectedKeys={selectedItem ? [selectedItem.to] : []}
          items={menuItems}
        />
        <div
          className="ag-sider-footer border-t border-zinc-800/60 py-2"
          data-collapsed={collapsed ? "true" : "false"}
        >
          <span className="ag-sider-meta text-xs leading-4 text-zinc-600">
            Native Hosted API v2
          </span>
          <Tooltip
            title={collapsed ? t("nav.expand") : t("nav.collapse")}
            placement="right"
          >
            <Button
              className="ag-sider-toggle"
              type="text"
              size="small"
              shape="circle"
              aria-label={collapsed ? t("nav.expand") : t("nav.collapse")}
              icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
              onClick={toggleCollapsed}
            />
          </Tooltip>
        </div>
      </aside>
      <Drawer
        rootClassName="ag-mobile-nav-drawer"
        classNames={{
          body: "ag-mobile-nav-body",
          header: "ag-mobile-nav-header",
        }}
        title={<BrandLockup />}
        placement="left"
        size="min(88vw, 320px)"
        closable={{ "aria-label": t("nav.close") }}
        open={mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
      >
        <Menu
          className="ag-nav ag-mobile-nav-menu flex-1 border-0 bg-transparent px-3"
          mode="inline"
          theme={colorMode}
          selectedKeys={selectedItem ? [selectedItem.to] : []}
          items={menuItems}
          onClick={() => setMobileNavOpen(false)}
        />
        <div className="ag-mobile-nav-footer text-xs text-zinc-600">
          Native Hosted API v2
        </div>
      </Drawer>
      <div className="ag-shell-main flex min-h-screen min-w-0 flex-1 flex-col">
        <header className="ag-topbar sticky top-0 z-20 flex min-h-14 items-center gap-3 px-6">
          <Button
            className="ag-mobile-nav-trigger"
            type="text"
            shape="circle"
            aria-label={t("nav.open")}
            icon={<MenuOutlined />}
            onClick={() => setMobileNavOpen(true)}
          />
          <GlobalSearchBox />
          <Space className="ag-topbar-actions ml-auto" size={4}>
            <PreferenceControls compact />
            <TokenDialog />
            <Button
              icon={<LoginOutlined />}
              aria-label={t("auth.logout")}
              onClick={clearToken}
            >
              <span className="ag-topbar-action-label">{t("auth.logout")}</span>
            </Button>
          </Space>
        </header>
        <main className="ag-main mx-auto w-full max-w-[1440px] flex-1 px-6 py-6">
          <div>
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
