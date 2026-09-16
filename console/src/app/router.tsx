import { Navigate, createBrowserRouter } from "react-router-dom";
import { usePreferences } from "../lib/preferences";
import { RouteErrorPage } from "./RouteErrorPage";

function RouteFallback() {
  const { t } = usePreferences();
  return (
    <div className="ag-app-fallback flex min-h-screen items-center justify-center text-sm text-zinc-500">
      {t("common.loading")}
    </div>
  );
}

export const router = createBrowserRouter([
  {
    path: "/login",
    errorElement: <RouteErrorPage />,
    hydrateFallbackElement: <RouteFallback />,
    lazy: async () => ({
      Component: (await import("../features/auth/Login")).LoginPage,
    }),
  },
  {
    path: "/browse",
    errorElement: <RouteErrorPage />,
    hydrateFallbackElement: <RouteFallback />,
    lazy: async () => ({
      Component: (await import("../features/public-browse/PublicBrowsePage"))
        .PublicBrowsePage,
    }),
  },
  {
    errorElement: <RouteErrorPage />,
    hydrateFallbackElement: <RouteFallback />,
    lazy: async () => ({ Component: (await import("./Layout")).AppLayout }),
    children: [
      {
        path: "/",
        lazy: async () => ({
          Component: (await import("../features/dashboard/Dashboard"))
            .DashboardPage,
        }),
      },
      {
        path: "/search",
        lazy: async () => ({
          Component: (await import("../features/search/Search")).SearchPage,
        }),
      },
      {
        path: "/operations",
        lazy: async () => ({
          Component: (await import("../features/operations/Operations"))
            .OperationsPage,
        }),
      },
      {
        path: "/repositories",
        lazy: async () => ({
          Component: (await import("../features/repository/Repositories"))
            .RepositoriesPage,
        }),
      },
      {
        path: "/repositories/:repositoryId",
        lazy: async () => ({
          Component: (await import("../features/repository/RepositoryDetail"))
            .RepositoryDetailPage,
        }),
      },
      {
        path: "/groups",
        lazy: async () => ({
          Component: (await import("../features/groups/Groups")).GroupsPage,
        }),
      },
      {
        path: "/groups/:groupId/browse",
        lazy: async () => ({
          Component: (await import("../features/groups/pages/GroupBrowsePage"))
            .GroupBrowsePage,
        }),
      },
      { path: "/proxy", element: <Navigate to="/repositories" replace /> },
      {
        path: "/access",
        lazy: async () => ({
          Component: (await import("../features/access-control/AccessControl"))
            .AccessControlPage,
        }),
      },
      {
        path: "/audits",
        lazy: async () => ({
          Component: (await import("../features/audit/Audits")).AuditsPage,
        }),
      },
      {
        path: "/identity-providers",
        lazy: async () => ({
          Component: (await import("../features/settings/Authentication"))
            .AuthenticationPage,
        }),
      },
      {
        path: "/site-settings",
        lazy: async () => ({
          Component: (await import("../features/settings/SiteSettings"))
            .SiteSettingsPage,
        }),
      },
      {
        path: "/keys",
        lazy: async () => ({
          Component: (await import("../features/identity/ApiKeys")).ApiKeysPage,
        }),
      },
      {
        path: "/service-accounts",
        lazy: async () => ({
          Component: (await import("../features/identity/ServiceAccounts"))
            .ServiceAccountsPage,
        }),
      },
      {
        path: "/users",
        lazy: async () => ({
          Component: (await import("../features/identity/Users")).UsersPage,
        }),
      },
      {
        path: "/audit-retention",
        lazy: async () => ({
          Component: (await import("../features/audit/AuditRetention"))
            .AuditRetentionPage,
        }),
      },
    ],
  },
]);
