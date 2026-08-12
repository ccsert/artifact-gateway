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
    <main className="flex min-h-screen items-center justify-center px-6">
      <section
        aria-labelledby="route-error-title"
        className="w-full max-w-xl rounded-2xl border border-rose-500/20 bg-zinc-950/80 p-8 text-center shadow-2xl shadow-black/20"
      >
        <div
          aria-hidden="true"
          className="mx-auto mb-5 flex size-12 items-center justify-center rounded-full bg-rose-500/10 text-2xl text-rose-400"
        >
          !
        </div>
        <h1
          id="route-error-title"
          className="text-xl font-semibold text-zinc-100"
        >
          {dynamicModuleError
            ? text("页面资源加载失败", "Page resources failed to load")
            : text("页面加载失败", "Page failed to load")}
        </h1>
        <p className="mt-3 text-sm leading-6 text-zinc-400">
          {dynamicModuleError
            ? text(
                "页面资源可能已更新，或开发服务正在重建依赖。请重新加载；若仍失败，请重启本地 Console。",
                "Page resources may have changed, or the development server may be rebuilding dependencies. Reload the page; if it still fails, restart the local Console.",
              )
            : text(
                "当前页面未能完成加载。请重新加载，或返回公开制品浏览。",
                "The page could not finish loading. Reload it or return to public artifact browsing.",
              )}
        </p>
        {detail && (
          <p className="mt-3 font-mono text-xs text-zinc-500">{detail}</p>
        )}
        <div className="mt-7 flex flex-wrap justify-center gap-3">
          <button
            type="button"
            className="rounded-lg bg-sky-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-sky-400 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-sky-400"
            onClick={() => window.location.reload()}
          >
            {text("重新加载", "Reload")}
          </button>
          <a
            href="/browse"
            className="rounded-lg border border-zinc-700 px-4 py-2 text-sm font-medium text-zinc-200 transition hover:border-zinc-500 hover:bg-zinc-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-zinc-400"
          >
            {text("浏览公开制品", "Browse public artifacts")}
          </a>
        </div>
      </section>
    </main>
  );
}
