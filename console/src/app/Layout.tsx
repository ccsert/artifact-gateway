import { useCallback, useState } from "react";
import {
  MenuOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  ReloadOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, Drawer, Menu, Space, Tooltip } from "antd";
import type { MenuProps } from "antd";
import { Link, Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "../lib/auth";
import { platformCapabilities } from "../lib/authorization";
import { Loading } from "../components/ui/Feedback";
import { PreferenceControls } from "../components/ui/PreferenceControls";
import { usePreferences } from "../lib/preferences";
import { SiteBrandMark } from "../components/ui/SiteBrand";
import { CommandPalette, useCommandPaletteShortcut } from "./CommandPalette";

import { AccountMenu } from "./AccountMenu";
import { BrandLockup, CommandTrigger, ConnectedVersion } from "./ShellChrome";
import { navItems } from "./shellNavigation";

export function AppLayout() {
  const { authenticated, identity, identityLoading, clearToken } = useAuth();
  const { colorMode, t } = usePreferences();
  const location = useLocation();
  // Mobile navigation stays open only on the page it was opened from.
  const [mobileNavPath, setMobileNavPath] = useState<string | null>(null);
  const mobileNavOpen = mobileNavPath === location.pathname;
  const setMobileNavOpen = (open: boolean) =>
    setMobileNavPath(open ? location.pathname : null);
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
        <div className="w-full max-w-lg rounded-2xl border border-line bg-surface p-8 text-center">
          <SiteBrandMark className="mx-auto flex h-12 w-12 items-center justify-center rounded-xl text-lg font-bold text-on-identity" />
          <h1 className="mt-5 text-xl font-semibold text-fg-strong">
            {t("auth.awaitingAuthorization")}
          </h1>
          <p className="mt-3 text-sm leading-6 text-fg-secondary">
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
          className="ag-sider-footer border-t border-line py-2"
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
        <div className="ag-mobile-nav-footer text-xs text-fg-disabled">
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
