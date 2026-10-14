import { Navigate, createBrowserRouter } from "react-router-dom";
import { usePreferences } from "../lib/preferences";
import { RouteErrorPage } from "./RouteErrorPage";
import { consoleRedirects, consoleRoutes } from "./routes";

function RouteFallback() {
  const { t } = usePreferences();
  return (
    <div className="ag-app-fallback flex min-h-screen items-center justify-center text-sm text-fg-tertiary">
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
      ...consoleRoutes.map(({ path, lazy }) => ({ path, lazy })),
      ...consoleRedirects.map(({ from, to }) => ({
        path: from,
        element: <Navigate to={to} replace />,
      })),
    ],
  },
]);
