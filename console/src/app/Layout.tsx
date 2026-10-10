import { useCallback, useEffect, useState } from "react";
import {
  ClockCircleOutlined,
  DashboardOutlined,
  DownOutlined,
  FileSearchOutlined,
  InboxOutlined,
  KeyOutlined,
  LoginOutlined,
  LogoutOutlined,
  MenuOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MonitorOutlined,
  ReloadOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SettingOutlined,
  SyncOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import { Button, Drawer, Dropdown, Input, Menu, Space, Tooltip } from "antd";
import type { MenuProps } from "antd";
import { Link, Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { platformCapabilities } from "../lib/authorization";
import { Modal, useDisclosure } from "../components/ui/Modal";
import { Field } from "../components/ui/Layout";
import { Loading } from "../components/ui/Feedback";
import { PreferenceControls } from "../components/ui/PreferenceControls";
import { usePreferences } from "../lib/preferences";
import { SiteBrandMark, SiteName } from "../components/ui/SiteBrand";
import { getDiagnostics, type CurrentIdentity } from "../client";
import { CommandPalette, useCommandPaletteShortcut } from "./CommandPalette";

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
    to: "/system",
    label: "nav.system",
    icon: <MonitorOutlined />,
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
    to: "/site-settings",
    label: "nav.siteSettings",
    icon: <SettingOutlined />,
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
      <SiteBrandMark className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-sm font-bold text-white" />
      <div className="ag-brand-copy min-w-0">
        <div className="truncate text-sm font-semibold text-zinc-100">
          <SiteName />
        </div>
        <div className="text-xs uppercase tracking-widest text-zinc-600">
          Console
        </div>
      </div>
    </div>
  );
}

function CommandTrigger({
  collapsed,
  onOpen,
}: {
  collapsed: boolean;
  onOpen: () => void;
}) {
  const { t } = usePreferences();
  const shortcut =
    typeof navigator !== "undefined" &&
    /Mac|iP(hone|ad)/.test(navigator.platform)
      ? "⌘K"
      : "Ctrl K";
  return (
    <Tooltip
      title={collapsed ? t("palette.trigger") : undefined}
      placement="right"
    >
      <button
        type="button"
        className="ag-command-trigger"
        data-collapsed={collapsed ? "true" : "false"}
        aria-label={t("palette.trigger")}
        aria-keyshortcuts="Meta+K Control+K"
        onClick={onOpen}
      >
        <SearchOutlined aria-hidden="true" />
        <span className="ag-command-trigger-label">{t("palette.trigger")}</span>
        <kbd className="ag-command-trigger-kbd">{shortcut}</kbd>
      </button>
    </Tooltip>
  );
}

function TokenDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { token, setToken, clearToken } = useAuth();
  const { t } = usePreferences();
  const [draft, setDraft] = useState("");

  useEffect(() => {
    if (open) setDraft(token);
  }, [open, token]);

  return (
    <Modal
      open={open}
      title={t("auth.tokenDialog")}
      onClose={onClose}
      footer={
        <div className="flex items-center justify-between gap-4">
          <div>
            {token && (
              <Button
                danger
                onClick={() => {
                  clearToken();
                  onClose();
                }}
              >
                {t("auth.clearToken")}
              </Button>
            )}
          </div>
          <Space>
            <Button onClick={onClose}>{t("common.cancel")}</Button>
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
  );
}

/**
 * Who is signed in, and how. Token management and sign-out live here instead
 * of the old top bar, so the button states the real authentication method.
 */
function AccountMenu({
  identity,
  collapsed = false,
  onSignOut,
}: {
  identity: CurrentIdentity | null;
  collapsed?: boolean;
  onSignOut: () => void;
}) {
  const { token } = useAuth();
  const { t } = usePreferences();
  const tokenDialog = useDisclosure();
  const actor = identity?.actor ?? "—";
  const initial =
    actor
      .replace(/^[a-z-]+:/, "")
      .charAt(0)
      .toUpperCase() || "?";
  const kind = identity ? t(`account.kind.${identity.kind}`) : "";
  const role =
    identity?.role && identity.role !== "none"
      ? t(`account.role.${identity.role}`)
      : "";
  const summary = [role, kind].filter(Boolean).join(" · ");

  return (
    <>
      <Dropdown
        trigger={["click"]}
        placement="topLeft"
        menu={{
          items: [
            {
              key: "token",
              icon: <KeyOutlined />,
              label: token ? t("auth.tokenConfigured") : t("auth.setToken"),
              onClick: tokenDialog.show,
            },
            { type: "divider" },
            {
              key: "sign-out",
              icon: <LogoutOutlined />,
              label: t("auth.logout"),
              onClick: onSignOut,
            },
          ],
        }}
      >
        <button
          type="button"
          className="ag-account-button"
          data-collapsed={collapsed ? "true" : "false"}
          aria-label={`${t("account.menu")}: ${actor}`}
        >
          <span className="ag-account-avatar" aria-hidden="true">
            {initial}
          </span>
          <span className="ag-account-copy">
            <span className="ag-account-name">{actor}</span>
            {summary && <span className="ag-account-meta">{summary}</span>}
          </span>
          <DownOutlined className="ag-account-caret" aria-hidden="true" />
        </button>
      </Dropdown>
      <TokenDialog open={tokenDialog.open} onClose={tokenDialog.hide} />
    </>
  );
}

function ConnectedVersion() {
  const { text } = usePreferences();
  const [build, setBuild] = useState<{
    version: string;
    revision: string;
  } | null>(null);

  useEffect(() => {
    let mounted = true;
    const load = async () => {
      try {
        const result = await getDiagnostics();
        if (mounted)
          setBuild(result.error ? null : (result.data?.build ?? null));
      } catch {
        if (mounted) setBuild(null);
      }
    };
    void load();
    const timer = window.setInterval(() => void load(), 60_000);
    return () => {
      mounted = false;
      window.clearInterval(timer);
    };
  }, []);

  const version =
    build?.version && build.version !== "unknown"
      ? build.version === "dev"
        ? text("开发构建", "Development build")
        : build.version
      : text("版本未知", "Version unknown");
  const revision =
    build?.revision && build.revision !== "unknown"
      ? ` · ${build.revision.slice(0, 12)}`
      : "";

  return (
    <Link
      to="/system?tab=diagnostics"
      className="ag-sider-meta text-xs leading-4 text-zinc-500 hover:text-zinc-200"
      title={text(
        "查看当前连接节点的系统诊断",
        "View diagnostics for the connected node",
      )}
    >
      {text("当前节点", "Connected node")} · {version}
      {revision}
    </Link>
  );
}

export function AppLayout() {
  const { authenticated, identity, identityLoading, clearToken } = useAuth();
  const { colorMode, t } = usePreferences();
  const location = useLocation();
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const openPalette = useCallback(() => setPaletteOpen(true), []);
  useCommandPaletteShortcut(openPalette);
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

  const capabilities = platformCapabilities(identity);

  if (capabilities.pending) {
    return (
      <div className="ag-app-fallback flex min-h-screen items-center justify-center px-6">
        <div className="w-full max-w-lg rounded-2xl border border-zinc-800 bg-zinc-900 p-8 text-center">
          <SiteBrandMark className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl text-lg font-bold text-white" />
          <h1 className="mt-5 text-xl font-semibold text-zinc-100">
            {t("auth.awaitingAuthorization")}
          </h1>
          <p className="mt-3 text-sm leading-6 text-zinc-400">
            {t("auth.awaitingAuthorizationDescription")}
          </p>
          <Space className="mt-6">
            <Button
              type="primary"
              icon={<ReloadOutlined />}
              aria-label={t("auth.checkAuthorization")}
              onClick={() => window.location.reload()}
            >
              {t("auth.checkAuthorization")}
            </Button>
            <Button onClick={clearToken}>{t("auth.logout")}</Button>
          </Space>
        </div>
      </div>
    );
  }

  const adminOnlyPath = [
    "/",
    "/operations",
    "/system",
    "/groups",
    "/access",
    "/audits",
    "/audit-retention",
    "/identity-providers",
    "/site-settings",
    "/keys",
    "/service-accounts",
    "/users",
  ].includes(location.pathname);
  const adminOnlySection = [
    "/operations",
    "/system",
    "/groups",
    "/access",
    "/audits",
    "/audit-retention",
    "/identity-providers",
    "/site-settings",
    "/keys",
    "/service-accounts",
    "/users",
  ].some((prefix) => location.pathname.startsWith(prefix + "/"));
  if (
    identity &&
    !capabilities.platformAdmin &&
    (adminOnlyPath || adminOnlySection)
  ) {
    return (
      <Navigate
        to={capabilities.browseRepositories ? "/repositories" : "/search"}
        replace
      />
    );
  }

  // The catalog is no longer administrator-only, so it needs its own gate: an
  // identity that cannot read any repository must not reach it by URL, and the
  // trailing-slash form must not slip past an exact-path comparison.
  const repositoryCatalogPath =
    location.pathname === "/repositories" ||
    location.pathname.startsWith("/repositories/");
  if (identity && !capabilities.browseRepositories && repositoryCatalogPath) {
    return <Navigate to="/search" replace />;
  }

  const visibleNavItems = navItems.filter(
    (item) =>
      (!("admin" in item) || capabilities.platformAdmin) &&
      (item.to !== "/repositories" || capabilities.browseRepositories),
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
        <CommandTrigger collapsed={collapsed} onOpen={openPalette} />
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
          {capabilities.platformAdmin && <ConnectedVersion />}
          {!collapsed && <PreferenceControls compact />}
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
        <div
          className="ag-sider-account"
          data-collapsed={collapsed ? "true" : "false"}
        >
          {collapsed && <PreferenceControls compact />}
          <AccountMenu
            identity={identity}
            collapsed={collapsed}
            onSignOut={clearToken}
          />
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
          {capabilities.platformAdmin && <ConnectedVersion />}
        </div>
      </Drawer>
      <div className="ag-shell-main flex min-h-screen min-w-0 flex-1 flex-col">
        <header className="ag-mobile-bar sticky top-0 z-20 flex min-h-14 items-center gap-2">
          <Button
            className="ag-mobile-nav-trigger"
            type="text"
            shape="circle"
            aria-label={t("nav.open")}
            icon={<MenuOutlined />}
            onClick={() => setMobileNavOpen(true)}
          />
          <BrandLockup />
          <Space className="ml-auto" size={4}>
            <Button
              type="text"
              shape="circle"
              className="ag-mobile-bar-action"
              aria-label={t("palette.trigger")}
              icon={<SearchOutlined />}
              onClick={openPalette}
            />
            <PreferenceControls compact />
            <AccountMenu identity={identity} collapsed onSignOut={clearToken} />
          </Space>
        </header>
        <main className="ag-main mx-auto w-full max-w-[1440px] flex-1 px-6 py-6 lg:px-10 lg:py-8">
          <div>
            <Outlet />
          </div>
        </main>
      </div>
      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        pages={visibleNavItems.map((item) => ({
          to: item.to,
          label: t(item.label),
          icon: item.icon,
        }))}
        canBrowseRepositories={capabilities.browseRepositories}
        onSignOut={clearToken}
      />
    </div>
  );
}
