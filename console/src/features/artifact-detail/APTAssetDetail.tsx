import { ArtifactDetailView } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { usePreferences } from "../../lib/preferences";
import { formatDate } from "../../lib/format";

export function APTAssetDetail({
  repositoryId,
  repoName,
  meta,
  canQuarantine = false,
  published = false,
}: {
  repositoryId?: string;
  repoName: string;
  meta: ArtifactMeta;
  canQuarantine?: boolean;
  published?: boolean;
}) {
  const { locale, t } = usePreferences();
  const assetKind = meta.coordinate.startsWith("pool/")
    ? t("artifactDetail.packageObject")
    : meta.coordinate.includes("/by-hash/")
      ? t("artifactDetail.byHashRepositoryMetadata")
      : t("artifactDetail.repositoryMetadata");

  return (
    <ArtifactDetailView
      format="apt"
      repositoryId={repositoryId}
      repoName={repoName}
      meta={meta}
      canQuarantine={canQuarantine}
      timestampLabel={
        published
          ? t("artifactDetail.published2")
          : t("artifactDetail.firstCached")
      }
      extra={
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="rounded-lg border border-line px-3 py-2">
            <div className="text-xs uppercase tracking-wider text-fg-tertiary">
              {t("artifactDetail.aPTAssetType")}
            </div>
            <div className="mt-0.5 text-xs font-semibold text-fg-strong">
              {assetKind}
            </div>
          </div>
          {meta.cachedAt && (
            <div className="rounded-lg border border-line px-3 py-2">
              <div className="text-xs uppercase tracking-wider text-fg-tertiary">
                {t("artifactDetail.lastCached")}
              </div>
              <div className="mt-0.5 text-xs font-semibold text-fg-strong">
                {formatDate(meta.cachedAt, locale)}
              </div>
            </div>
          )}
          {meta.sourceUrl && (
            <div className="min-w-0 rounded-lg border border-line px-3 py-2">
              <div className="text-xs uppercase tracking-wider text-fg-tertiary">
                {t("artifactDetail.upstreamURL")}
              </div>
              <a
                className="mt-0.5 block truncate font-mono text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
                href={meta.sourceUrl}
                target="_blank"
                rel="noreferrer"
                title={meta.sourceUrl}
              >
                {meta.sourceUrl}
              </a>
            </div>
          )}
        </div>
      }
    />
  );
}
