import {
  ArrowRightOutlined,
  DatabaseOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button } from "antd";
import { Link } from "react-router-dom";
import { FormatBadge, RepositoryTypeBadge } from "../../components/ui/Badge";
import { EmptyState } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { artifactFormatVisualizationClass } from "../../lib/artifactFormatVisuals";
import { usePreferences } from "../../lib/preferences";
import type { PublicRepositoryFormat } from "../../lib/publicRepositoryUsage";
import { PublicBrowseStateSurface } from "./PublicBrowseChrome";
import { PUBLIC_FORMAT_STYLE } from "./publicFormats";
import type { PublicRepository } from "./types";

/** The public sources, filterable by format, or why there are none. */
export function PublicCatalog({
  authenticated,
  repositories,
  publicFormats,
  visibleRepositories,
  catalogFormat,
  onCatalogFormatChange,
  onClearFilters,
}: {
  authenticated: boolean;
  repositories: PublicRepository[];
  publicFormats: PublicRepositoryFormat[];
  visibleRepositories: PublicRepository[];
  catalogFormat: PublicRepositoryFormat | "all";
  onCatalogFormatChange: (format: PublicRepositoryFormat | "all") => void;
  onClearFilters: () => void;
}) {
  const { t } = usePreferences();
  return repositories.length === 0 ? (
    <PublicBrowseStateSurface className="ag-public-catalog-empty-surface">
      <EmptyState
        className="ag-public-catalog-empty"
        icon={<DatabaseOutlined />}
        title={t("publicBrowse.noPublicRepositories")}
        hint={t("publicBrowse.emptyCatalogHint")}
        action={
          <Button type="primary" href={authenticated ? "/" : "/login"}>
            {t(
              authenticated
                ? "public.managementConsole"
                : "public.managementLogin",
            )}
          </Button>
        }
      />
    </PublicBrowseStateSurface>
  ) : (
    <div>
      <div className="mb-4 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h2 className="text-lg font-semibold text-fg-strong">
            {t("publicBrowse.chooseAnArtifactSource")}
          </h2>
          <p className="mt-1 text-sm text-fg-tertiary">
            {t("publicBrowse.publicSourcesArtifactFormats", {
              sourceCount: repositories.length,
              formatCount: publicFormats.length,
            })}
          </p>
        </div>
        <div
          className="flex max-w-full gap-1 overflow-x-auto rounded-lg border border-line bg-surface-translucent p-1"
          aria-label={t("publicBrowse.filterByArtifactFormat")}
        >
          <Button
            size="small"
            type={catalogFormat === "all" ? "primary" : "text"}
            aria-pressed={catalogFormat === "all"}
            onClick={() => onCatalogFormatChange("all")}
          >
            {t("publicBrowse.all")}
          </Button>
          {publicFormats.map((format) => (
            <Button
              key={format}
              size="small"
              type={catalogFormat === format ? "primary" : "text"}
              aria-pressed={catalogFormat === format}
              onClick={() => onCatalogFormatChange(format)}
            >
              <span className="font-mono">{format}</span>
            </Button>
          ))}
        </div>
      </div>
      {visibleRepositories.length === 0 ? (
        <EmptyState
          compact
          icon={<SearchOutlined />}
          title={t("publicBrowse.noMatchingPublicRepositories")}
          hint={t("publicBrowse.catalogFilterHint")}
          action={
            <Button onClick={onClearFilters}>
              {t("publicBrowse.clearFilters")}
            </Button>
          }
        />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {visibleRepositories.map((repository) => (
            <Link
              key={repository.id}
              to={`/browse?repository=${encodeURIComponent(repository.id)}`}
              className="group block text-left"
            >
              <Card
                className="ag-public-repository-card h-full border-[var(--ag-border-subtle)] bg-[var(--ag-surface-container-translucent)] group-hover:border-[var(--ag-border-default)] group-hover:bg-[var(--ag-surface-hover)]"
                bodyClassName="flex h-full min-h-52 flex-col p-5"
              >
                <div className="flex items-start justify-between gap-3">
                  <div
                    className={`flex h-11 min-w-11 items-center justify-center rounded-xl border px-2 text-xs font-semibold tracking-wide ${artifactFormatVisualizationClass(repository.format)}`}
                  >
                    {PUBLIC_FORMAT_STYLE[repository.format]?.icon ?? (
                      <DatabaseOutlined />
                    )}
                  </div>
                  <div className="flex items-center gap-2">
                    <RepositoryTypeBadge type={repository.type} />
                    <FormatBadge format={repository.format} />
                  </div>
                </div>
                <div className="mt-5 min-w-0 truncate text-base font-semibold text-fg-strong">
                  {repository.name}
                </div>
                <p className="mt-2 text-sm leading-5 text-fg-tertiary">
                  {repository.type === "proxy"
                    ? t("publicBrowse.proxyDescription")
                    : repository.type === "group"
                      ? t("publicBrowse.groupDescription")
                      : t("publicBrowse.hostedDescription")}
                </p>
                <div className="mt-auto flex items-center justify-between pt-5 text-xs">
                  <span className="text-fg-disabled">
                    {t("publicBrowse.noSignInToRead")}
                  </span>
                  <span className="flex items-center gap-1 font-medium text-[var(--ag-link)] transition-colors group-hover:text-[var(--ag-link-hover)]">
                    {t("publicBrowse.openRepository")}
                    <ArrowRightOutlined />
                  </span>
                </div>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
