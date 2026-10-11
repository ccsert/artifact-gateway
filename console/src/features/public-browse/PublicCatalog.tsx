import {
  ArrowRightOutlined,
  DatabaseOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button } from "antd";
import { Link } from "react-router-dom";
import { Badge, FormatBadge } from "../../components/ui/Badge";
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
  const { t, text } = usePreferences();
  return repositories.length === 0 ? (
    <PublicBrowseStateSurface className="ag-public-catalog-empty-surface">
      <EmptyState
        className="ag-public-catalog-empty"
        icon={<DatabaseOutlined />}
        title={text("暂无公开仓库", "No public repositories")}
        hint={text(
          "管理员需先启用全局匿名访问，并在仓库上允许匿名读取。公开仓库出现后，这里会直接展示可匿名使用的来源与格式。",
          "An administrator must enable global anonymous access and allow reads on a repository. Public sources and formats will appear here as soon as they are available.",
        )}
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
            {text("选择制品来源", "Choose an artifact source")}
          </h2>
          <p className="mt-1 text-sm text-fg-tertiary">
            {text(
              `${repositories.length} 个公开来源 · ${publicFormats.length} 种制品格式`,
              `${repositories.length} public sources · ${publicFormats.length} artifact formats`,
            )}
          </p>
        </div>
        <div
          className="flex max-w-full gap-1 overflow-x-auto rounded-lg border border-line bg-surface-translucent p-1"
          aria-label={text("按制品格式筛选", "Filter by artifact format")}
        >
          <Button
            size="small"
            type={catalogFormat === "all" ? "primary" : "text"}
            aria-pressed={catalogFormat === "all"}
            onClick={() => onCatalogFormatChange("all")}
          >
            {text("全部", "All")}
          </Button>
          {publicFormats.map((format) => (
            <Button
              key={format}
              size="small"
              type={catalogFormat === format ? "primary" : "text"}
              aria-pressed={catalogFormat === format}
              onClick={() => onCatalogFormatChange(format)}
            >
              {format.toUpperCase()}
            </Button>
          ))}
        </div>
      </div>
      {visibleRepositories.length === 0 ? (
        <EmptyState
          compact
          icon={<SearchOutlined />}
          title={text("没有匹配的公开仓库", "No matching public repositories")}
          hint={text(
            "调整搜索词或格式筛选后重试。",
            "Adjust the search term or format filter and try again.",
          )}
          action={
            <Button onClick={onClearFilters}>
              {text("清除筛选", "Clear filters")}
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
                    {PUBLIC_FORMAT_STYLE[repository.format].icon}
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge
                      tone={
                        repository.type === "proxy"
                          ? "visualization-2"
                          : repository.type === "group"
                            ? "visualization-6"
                            : "visualization-1"
                      }
                    >
                      {(repository.type ?? "hosted").toUpperCase()}
                    </Badge>
                    <FormatBadge format={repository.format} />
                  </div>
                </div>
                <div className="mt-5 min-w-0 truncate text-base font-semibold text-fg-strong">
                  {repository.name}
                </div>
                <p className="mt-2 text-sm leading-5 text-fg-tertiary">
                  {repository.type === "proxy"
                    ? text(
                        "缓存并加速经过管理员信任的上游制品。",
                        "Caches and accelerates artifacts from an administrator-trusted upstream.",
                      )
                    : repository.type === "group"
                      ? text(
                          "通过一个稳定入口聚合多个公开仓库。",
                          "Aggregates multiple public repositories behind one stable endpoint.",
                        )
                      : text(
                          "由团队直接发布和维护的公开制品。",
                          "Public artifacts published and maintained directly by the team.",
                        )}
                </p>
                <div className="mt-auto flex items-center justify-between pt-5 text-xs">
                  <span className="text-fg-disabled">
                    {text("无需登录即可读取", "No sign-in to read")}
                  </span>
                  <span className="flex items-center gap-1 font-medium text-[var(--ag-link)] transition-colors group-hover:text-[var(--ag-link-hover)]">
                    {text("进入仓库", "Open repository")}
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
