import { ArtifactDetailView } from "./ArtifactDetail";
import type { ArtifactMeta } from "./ArtifactDetail";
import { usePreferences } from "../lib/preferences";
import { formatDate } from "../lib/format";

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
  const { locale, text } = usePreferences();
  const assetKind = meta.coordinate.startsWith("pool/")
    ? text("软件包对象", "Package object")
    : meta.coordinate.includes("/by-hash/")
      ? text("按摘要索引的仓库元数据", "By-hash repository metadata")
      : text("仓库元数据", "Repository metadata");

  return (
    <ArtifactDetailView
      format="apt"
      repositoryId={repositoryId}
      repoName={repoName}
      meta={meta}
      canQuarantine={canQuarantine}
      timestampLabel={
        published
          ? text("已发布", "Published")
          : text("首次缓存", "First cached")
      }
      extra={
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="rounded-lg border border-zinc-800 px-3 py-2">
            <div className="text-xs uppercase tracking-wider text-zinc-500">
              {text("APT 资产类型", "APT asset type")}
            </div>
            <div className="mt-0.5 text-xs font-semibold text-zinc-100">
              {assetKind}
            </div>
          </div>
          {meta.cachedAt && (
            <div className="rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-xs uppercase tracking-wider text-zinc-500">
                {text("最近缓存", "Last cached")}
              </div>
              <div className="mt-0.5 text-xs font-semibold text-zinc-100">
                {formatDate(meta.cachedAt, locale)}
              </div>
            </div>
          )}
          {meta.sourceUrl && (
            <div className="min-w-0 rounded-lg border border-zinc-800 px-3 py-2">
              <div className="text-xs uppercase tracking-wider text-zinc-500">
                {text("上游地址", "Upstream URL")}
              </div>
              <a
                className="mt-0.5 block truncate font-mono text-xs text-cyan-300 hover:text-cyan-200"
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
