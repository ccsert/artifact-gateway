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
      Component: (await import("../pages/Login")).LoginPage,
    }),
  },
  {
    path: "/browse",
    errorElement: <RouteErrorPage />,
    hydrateFallbackElement: <RouteFallback />,
    lazy: async () => ({
      Component: (await import("../pages/PublicBrowse")).PublicBrowsePage,
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
          Component: (await import("../pages/Dashboard")).DashboardPage,
        }),
      },
      {
        path: "/search",
        lazy: async () => ({
          Component: (await import("../pages/Search")).SearchPage,
        }),
      },
      {
        path: "/operations",
        lazy: async () => ({
          Component: (await import("../pages/Operations")).OperationsPage,
        }),
      },
      {
        path: "/repositories",
        lazy: async () => ({
          Component: (await import("../pages/Repositories")).RepositoriesPage,
        }),
      },
      {
        path: "/repositories/:repositoryId",
        lazy: async () => ({
          Component: (await import("../pages/RepositoryDetail"))
            .RepositoryDetailPage,
        }),
      },
      {
        path: "/groups",
        lazy: async () => ({
          Component: (await import("../pages/Groups")).GroupsPage,
        }),
      },
      { path: "/proxy", element: <Navigate to="/repositories" replace /> },
      {
        path: "/access",
        lazy: async () => ({
          Component: (await import("../pages/AccessControl")).AccessControlPage,
        }),
      },
      {
        path: "/audits",
        lazy: async () => ({
          Component: (await import("../pages/Audits")).AuditsPage,
        }),
      },
      {
        path: "/identity-providers",
        lazy: async () => ({
          Component: (await import("../pages/Authentication"))
            .AuthenticationPage,
        }),
      },
      {
        path: "/keys",
        lazy: async () => ({
          Component: (await import("../pages/ApiKeys")).ApiKeysPage,
        }),
      },
      {
        path: "/users",
        lazy: async () => ({
          Component: (await import("../pages/Users")).UsersPage,
        }),
      },
      {
        path: "/audit-retention",
        lazy: async () => ({
          Component: (await import("../pages/AuditRetention"))
            .AuditRetentionPage,
        }),
      },
    ],
  },
]);
