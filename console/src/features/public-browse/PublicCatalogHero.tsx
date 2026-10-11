import { SafetyCertificateOutlined, SearchOutlined } from "@ant-design/icons";
import { Input } from "antd";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import type { PublicRepositoryFormat } from "../../lib/publicRepositoryUsage";
import type { PublicRepository } from "./types";

/** The catalog's introduction: what public means, and the catalog search. */
export function PublicCatalogHero({
  repositories,
  publicFormats,
  catalogQuery,
  onCatalogQueryChange,
}: {
  repositories: PublicRepository[] | null;
  publicFormats: PublicRepositoryFormat[];
  catalogQuery: string;
  onCatalogQueryChange: (value: string) => void;
}) {
  const { text } = usePreferences();
  return (
    <Card
      className="ag-page-primary overflow-hidden border-line bg-surface-translucent"
      bodyClassName="p-6 sm:p-8 lg:p-10"
    >
      <div className="grid gap-7 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-end">
        <div className="max-w-3xl">
          <div className="inline-flex items-center gap-2 rounded-full border border-[var(--ag-status-success-border)] bg-[var(--ag-status-success-soft)] px-3 py-1 text-xs font-medium text-[var(--ag-status-success)]">
            <SafetyCertificateOutlined />
            {text("公开只读", "Public read-only")}
          </div>
          <h1 className="mt-4 text-2xl font-semibold tracking-tight text-fg-strong">
            {text(
              "查找并使用可信的公开制品",
              "Discover and consume trusted public artifacts",
            )}
          </h1>
          <p className="mt-3 max-w-2xl text-sm leading-6 text-fg-secondary">
            {text(
              "从团队明确公开的仓库中检索镜像、依赖和软件包。读取无需登录，发布、授权和仓库管理始终需要身份认证。",
              "Search images, dependencies, and packages from repositories explicitly published by your team. Reading needs no sign-in; publishing, grants, and repository administration always require authentication.",
            )}
          </p>
          {(!repositories || repositories.length > 0) && (
            <Input
              allowClear
              size="large"
              prefix={<SearchOutlined className="text-fg-tertiary!" />}
              className="mt-6 max-w-2xl"
              placeholder={text(
                "搜索仓库名称或格式",
                "Search repository name or format",
              )}
              value={catalogQuery}
              onChange={(event) => onCatalogQueryChange(event.target.value)}
            />
          )}
        </div>
        <div className="grid grid-cols-2 gap-px overflow-hidden rounded-xl border border-line bg-line">
          <div className="bg-surface-translucent p-4">
            <div className="text-2xl font-semibold text-fg-strong">
              {repositories?.length ?? "—"}
            </div>
            <div className="mt-1 text-xs text-fg-tertiary">
              {text("个公开来源", "public sources")}
            </div>
          </div>
          <div className="bg-surface-translucent p-4">
            <div className="text-2xl font-semibold text-fg-strong">
              {repositories ? publicFormats.length : "—"}
            </div>
            <div className="mt-1 text-xs text-fg-tertiary">
              {text("种制品格式", "artifact formats")}
            </div>
          </div>
          <div className="col-span-2 flex items-center gap-3 bg-surface-translucent p-4">
            <div className="flex size-9 items-center justify-center rounded-lg bg-[var(--ag-surface-hover)] text-[var(--ag-content-secondary)]">
              <SafetyCertificateOutlined />
            </div>
            <div>
              <div className="text-sm font-medium text-fg">
                {text("管理操作需要登录", "Management actions require sign-in")}
              </div>
              <div className="mt-0.5 text-xs text-fg-tertiary">
                {text(
                  "公开目录不会授予写入或管理权限",
                  "The public catalog never grants write or admin access",
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </Card>
  );
}
