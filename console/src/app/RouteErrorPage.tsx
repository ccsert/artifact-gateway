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
  const { t } = usePreferences();
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
            ? t("shell.pageResourcesFailedToLoad")
            : t("shell.pageFailedToLoad")}
        </h1>
        <p className="ag-route-error-description">
          {dynamicModuleError
            ? t("shell.pageResourcesMayHaveChangedOrTheDevelopment")
            : t("shell.thePageCouldNotFinishLoadingReloadIt")}
        </p>
        {detail && (
          <details className="ag-route-error-detail">
            <summary>{t("shell.technicalDetails")}</summary>
            <code>{detail}</code>
          </details>
        )}
        <div className="ag-route-error-actions">
          <button
            type="button"
            className="ag-route-error-primary"
            onClick={() => window.location.reload()}
          >
            {t("shell.reload")}
          </button>
          <a href="/browse" className="ag-route-error-secondary">
            {t("shell.browsePublicArtifacts")}
          </a>
        </div>
      </section>
    </main>
  );
}
