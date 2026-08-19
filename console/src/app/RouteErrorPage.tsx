import { isRouteErrorResponse, useRouteError } from "react-router-dom";
import { usePreferences } from "../lib/preferences";

function isDynamicModuleError(error: unknown): boolean {
  return (
    error instanceof Error &&
    /dynamically imported module|importing a module script failed|error loading dynamically imported module|outdated optimize dep/i.test(
      error.message,
    )
  );
}

function routeErrorDetail(error: unknown): string | undefined {
  if (isRouteErrorResponse(error)) {
    return [error.status, error.statusText].filter(Boolean).join(" ");
  }
  if (error instanceof Error && !isDynamicModuleError(error)) {
    return error.message;
  }
  return undefined;
}

export function RouteErrorPage() {
  const error = useRouteError();
  const { text } = usePreferences();
  const dynamicModuleError = isDynamicModuleError(error);
  const detail = routeErrorDetail(error);

  return (
    <main className="ag-route-error-page">
      <section
        aria-labelledby="route-error-title"
        className="ag-route-error-card"
        role="alert"
      >
        <div aria-hidden="true" className="ag-route-error-symbol">
          <svg viewBox="0 0 24 24" focusable="false">
            <path d="M12 7.25v5.5" />
            <circle cx="12" cy="16.5" r="1" fill="currentColor" stroke="none" />
          </svg>
        </div>
        <h1 id="route-error-title" className="ag-route-error-title">
          {dynamicModuleError
            ? text("页面资源加载失败", "Page resources failed to load")
            : text("页面加载失败", "Page failed to load")}
        </h1>
        <p className="ag-route-error-description">
          {dynamicModuleError
            ? text(
                "页面资源可能已更新，或开发服务正在重建依赖。请重新加载；若仍失败，请重启本地 Console。",
                "Page resources may have changed, or the development server may be rebuilding dependencies. Reload the page; if it still fails, restart the local Console.",
              )
            : text(
                "页面未能完成加载。请先重新加载；如果问题持续，可以前往公开制品浏览。",
                "The page could not finish loading. Reload it first; if the problem continues, open public artifact browsing.",
              )}
        </p>
        {detail && (
          <details className="ag-route-error-detail">
            <summary>{text("技术详情", "Technical details")}</summary>
            <code>{detail}</code>
          </details>
        )}
        <div className="ag-route-error-actions">
          <button
            type="button"
            className="ag-route-error-primary"
            onClick={() => window.location.reload()}
          >
            {text("重新加载", "Reload")}
          </button>
          <a href="/browse" className="ag-route-error-secondary">
            {text("浏览公开制品", "Browse public artifacts")}
          </a>
        </div>
      </section>
    </main>
  );
}
