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
  const { t } = usePreferences();
  return (
    <Card
      className="ag-page-primary overflow-hidden border-line bg-surface-translucent"
      bodyClassName="p-6 sm:p-8 lg:p-10"
    >
      <div className="grid gap-7 lg:grid-cols-[minmax(0,1fr)_340px] lg:items-end">
        <div className="max-w-3xl">
          <div className="inline-flex items-center gap-2 rounded-full border border-[var(--ag-status-success-border)] bg-[var(--ag-status-success-soft)] px-3 py-1 text-xs font-medium text-[var(--ag-status-success)]">
            <SafetyCertificateOutlined />
            {t("publicBrowse.publicReadOnly")}
          </div>
          <h1 className="mt-4 text-2xl font-semibold tracking-tight text-fg-strong">
            {t("publicBrowse.catalogHeading")}
          </h1>
          <p className="mt-3 max-w-2xl text-sm leading-6 text-fg-secondary">
            {t("publicBrowse.catalogDescription")}
          </p>
          {(!repositories || repositories.length > 0) && (
            <Input
              allowClear
              size="large"
              prefix={<SearchOutlined className="text-fg-tertiary!" />}
              className="mt-6 max-w-2xl"
              placeholder={t("publicBrowse.searchRepositoryNameOrFormat")}
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
              {t("publicBrowse.publicSources")}
            </div>
          </div>
          <div className="bg-surface-translucent p-4">
            <div className="text-2xl font-semibold text-fg-strong">
              {repositories ? publicFormats.length : "—"}
            </div>
            <div className="mt-1 text-xs text-fg-tertiary">
              {t("publicBrowse.artifactFormats")}
            </div>
          </div>
          <div className="col-span-2 flex items-center gap-3 bg-surface-translucent p-4">
            <div className="flex size-9 items-center justify-center rounded-lg bg-[var(--ag-surface-hover)] text-[var(--ag-content-secondary)]">
              <SafetyCertificateOutlined />
            </div>
            <div>
              <div className="text-sm font-medium text-fg">
                {t("publicBrowse.managementActionsRequireSignIn")}
              </div>
              <div className="mt-0.5 text-xs text-fg-tertiary">
                {t("publicBrowse.catalogAccessHint")}
              </div>
            </div>
          </div>
        </div>
      </div>
    </Card>
  );
}
