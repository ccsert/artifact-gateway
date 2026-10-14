import {
  ClockCircleOutlined,
  DashboardOutlined,
  FileSearchOutlined,
  InboxOutlined,
  KeyOutlined,
  LoginOutlined,
  MonitorOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  SearchOutlined,
  SettingOutlined,
  SyncOutlined,
  TeamOutlined,
  UserOutlined,
} from "@ant-design/icons";
import type { ComponentType, ReactNode } from "react";
import type { MessageKey } from "../messages";

/**
 * Who may open a page: any signed-in identity, an identity that can read
 * repositories, or a platform administrator. The shell redirects everyone
 * else, and hides the page from the navigation.
 */
export type RouteAccess = "signedIn" | "repositories" | "admin";

export type NavGroup = "runtime" | "governance" | "management";

export interface ConsoleRoute {
  path: string;
  access: RouteAccess;
  /** Loads the page on first visit, so each page stays its own chunk. */
  lazy: () => Promise<{ Component: ComponentType }>;
  /** Pages reachable from the sidebar and the command palette. */
  nav?: {
    label: MessageKey;
    icon: ReactNode;
    group: NavGroup;
    /** Selected only on its own path, not on paths below it. */
    exact?: true;
  };
}

/**
 * Every page inside the signed-in shell. The router, the sidebar, the command
 * palette and the access redirects all read this table; nothing else lists
 * paths. Order is the sidebar order within each group.
 */
export const consoleRoutes: readonly ConsoleRoute[] = [
  {
    path: "/",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/dashboard/Dashboard"))
        .DashboardPage,
    }),
    nav: {
      label: "nav.dashboard",
      icon: <DashboardOutlined />,
      group: "runtime",
      exact: true,
    },
  },
  {
    path: "/repositories",
    access: "repositories",
    lazy: async () => ({
      Component: (await import("../features/repository/Repositories"))
        .RepositoriesPage,
    }),
    nav: {
      label: "nav.repositories",
      icon: <InboxOutlined />,
      group: "runtime",
    },
  },
  {
    path: "/repositories/:repositoryId",
    access: "repositories",
    lazy: async () => ({
      Component: (await import("../features/repository/RepositoryDetail"))
        .RepositoryDetailPage,
    }),
  },
  {
    path: "/search",
    access: "signedIn",
    lazy: async () => ({
      Component: (await import("../features/search/Search")).SearchPage,
    }),
    nav: { label: "nav.search", icon: <SearchOutlined />, group: "runtime" },
  },
  {
    path: "/operations",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/operations/Operations"))
        .OperationsPage,
    }),
    nav: { label: "nav.operations", icon: <SyncOutlined />, group: "runtime" },
  },
  {
    path: "/system",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/system/System")).SystemPage,
    }),
    nav: { label: "nav.system", icon: <MonitorOutlined />, group: "runtime" },
  },
  {
    path: "/groups",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/groups/Groups")).GroupsPage,
    }),
    nav: { label: "nav.groups", icon: <TeamOutlined />, group: "governance" },
  },
  {
    path: "/groups/:groupId/browse",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/groups/pages/GroupBrowsePage"))
        .GroupBrowsePage,
    }),
  },
  {
    path: "/access",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/access-control/AccessControl"))
        .AccessControlPage,
    }),
    nav: {
      label: "nav.access",
      icon: <SafetyCertificateOutlined />,
      group: "governance",
    },
  },
  {
    path: "/audits",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/audit/Audits")).AuditsPage,
    }),
    nav: {
      label: "nav.audits",
      icon: <FileSearchOutlined />,
      group: "governance",
    },
  },
  {
    path: "/audit-retention",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/audit/AuditRetention"))
        .AuditRetentionPage,
    }),
    nav: {
      label: "nav.auditRetention",
      icon: <ClockCircleOutlined />,
      group: "governance",
    },
  },
  {
    path: "/identity-providers",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/settings/Authentication"))
        .AuthenticationPage,
    }),
    nav: {
      label: "nav.authentication",
      icon: <LoginOutlined />,
      group: "management",
    },
  },
  {
    path: "/site-settings",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/settings/SiteSettings"))
        .SiteSettingsPage,
    }),
    nav: {
      label: "nav.siteSettings",
      icon: <SettingOutlined />,
      group: "management",
    },
  },
  {
    path: "/keys",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/identity/ApiKeys")).ApiKeysPage,
    }),
    nav: { label: "nav.apiKeys", icon: <KeyOutlined />, group: "management" },
  },
  {
    path: "/service-accounts",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/identity/ServiceAccounts"))
        .ServiceAccountsPage,
    }),
    nav: {
      label: "nav.serviceAccounts",
      icon: <RobotOutlined />,
      group: "management",
    },
  },
  {
    path: "/users",
    access: "admin",
    lazy: async () => ({
      Component: (await import("../features/identity/Users")).UsersPage,
    }),
    nav: { label: "nav.users", icon: <UserOutlined />, group: "management" },
  },
];

/** Paths kept for old links, each pointing at where the page lives now. */
export const consoleRedirects: readonly { from: string; to: string }[] = [
  { from: "/proxy", to: "/repositories" },
];

export const navGroups: readonly { key: NavGroup; label: MessageKey }[] = [
  { key: "runtime", label: "nav.runtime" },
  { key: "governance", label: "nav.governance" },
  { key: "management", label: "nav.management" },
];

export interface NavItem {
  to: string;
  label: MessageKey;
  icon: ReactNode;
  group: NavGroup;
  exact?: true;
}

export interface RouteCapabilities {
  platformAdmin: boolean;
  browseRepositories: boolean;
}

export function canOpen(
  access: RouteAccess,
  capabilities: RouteCapabilities,
): boolean {
  if (access === "admin") return capabilities.platformAdmin;
  if (access === "repositories") return capabilities.browseRepositories;
  return true;
}

/** The sidebar entries this identity can open, in table order. */
export function navItemsFor(capabilities: RouteCapabilities): NavItem[] {
  return consoleRoutes.flatMap((route) =>
    route.nav && canOpen(route.access, capabilities)
      ? [{ to: route.path, ...route.nav }]
      : [],
  );
}

/**
 * The access a path needs, decided by its section (its first segment), so a
 * path below a page, even one no route matches, is guarded like the page.
 */
export function routeAccess(pathname: string): RouteAccess | undefined {
  const section = pathname.split("/")[1] ?? "";
  return consoleRoutes.find((route) => route.path.split("/")[1] === section)
    ?.access;
}
