import { useEffect, useMemo, useState } from "react";
import { Alert, Tag } from "antd";
import { Badge } from "./Badge";
import { Loading } from "./Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "./PublicBrowsePrimitives";
import { usePreferences } from "../lib/preferences";
import { useAuth } from "../lib/auth";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { npmUsage } from "../lib/usage";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";
import { useClipboardAction } from "./ConsolePrimitives";

interface NpmVersionManifest {
  name: string;
  version: string;
  description?: string;
  license?: string | { type?: string };
  dependencies?: Record<string, string>;
  dist?: {
    tarball?: string;
    integrity?: string;
    shasum?: string;
  };
  _artifactGateway?: {
    digest?: string;
    publisher?: string;
    size?: number;
    source?: "hosted" | "proxy";
    cacheStatus?: "metadata" | "cached";
    cachedAt?: string;
  };
}

interface NpmPackument {
  name: string;
  "dist-tags": Record<string, string>;
  versions: Record<string, NpmVersionManifest>;
  time?: Record<string, string>;
}

export function NpmPackageDetail({
  repositoryId,
  repoName,
  packageName,
  initialVersion,
  size,
  publisher,
  onVersionChange,
  canQuarantine = false,
}: {
  repositoryId?: string;
  repoName: string;
  packageName: string;
  initialVersion?: string;
  size?: number;
  publisher?: string;
  onVersionChange?: (version: string) => void;
  canQuarantine?: boolean;
}) {
  const { locale, text } = usePreferences();
  const { token } = useAuth();
  const [packument, setPackument] = useState<NpmPackument | null>(null);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const { copiedValue, copy } = useClipboardAction(1400);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void fetch(
      `/npm/${encodeURIComponent(repoName)}/${encodeURIComponent(packageName)}`,
      {
        credentials: "include",
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      },
    )
      .then(async (response) => {
        if (!response.ok)
          throw new Error(
            text(
              `读取 npm 包失败 (${response.status})`,
              `Failed to load npm package (${response.status})`,
            ),
          );
        return response.json() as Promise<NpmPackument>;
      })
      .then((document) => {
        if (cancelled) return;
        setPackument(document);
        setLoading(false);
      })
      .catch((requestError: unknown) => {
        if (cancelled) return;
        setError(
          requestError instanceof Error
            ? requestError.message
            : text("读取 npm 包失败", "Failed to load npm package"),
        );
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [packageName, repoName, text, token]);

  useEffect(() => {
    if (!packument) return;
    const fallback =
      packument["dist-tags"]?.latest ??
      Object.keys(packument.versions)[0] ??
      "";
    setSelectedVersion((current) => {
      if (initialVersion && packument.versions[initialVersion]) {
        return initialVersion;
      }
      return current && packument.versions[current] ? current : fallback;
    });
  }, [initialVersion, packument]);

  const versions = useMemo(() => {
    if (!packument) return [];
    return Object.keys(packument.versions).sort((left, right) => {
      const leftTime = packument.time?.[left] ?? "";
      const rightTime = packument.time?.[right] ?? "";
      return (
        rightTime.localeCompare(leftTime) ||
        right.localeCompare(left, undefined, { numeric: true })
      );
    });
  }, [packument]);

  if (loading) return <Loading />;
  if (error)
    return <Alert showIcon type="error" title={error} className="my-2" />;
  if (!packument || !selectedVersion) return null;

  const manifest = packument.versions[selectedVersion];
  const publishedAt = packument.time?.[selectedVersion];
  const license =
    typeof manifest?.license === "string"
      ? manifest.license
      : manifest?.license?.type;
  const dependencyCount = Object.keys(manifest?.dependencies ?? {}).length;
  const snippets = npmUsage(repoName, packageName, selectedVersion);
  const artifactMetadata = manifest?._artifactGateway;
  const metadataOnly = artifactMetadata?.cacheStatus === "metadata";

  return (
    <div className="grid gap-5 px-2 py-1 xl:grid-cols-[minmax(0,260px)_minmax(0,1fr)]">
      <ArtifactQuarantinePanel
        repositoryId={repositoryId}
        coordinate={`${packageName}@${selectedVersion}`}
        digest={artifactMetadata?.digest}
        canManage={canQuarantine}
      />
      <ArtifactScanStatus
        repositoryId={repositoryId}
        format="npm"
        coordinate={`${packageName}@${selectedVersion}`}
        digest={artifactMetadata?.digest}
      />
      <ArtifactIntelligencePanel
        repositoryId={repositoryId}
        format="npm"
        coordinate={`${packageName}@${selectedVersion}`}
        digest={artifactMetadata?.digest}
      />
      <div>
        <label className="mb-1.5 block text-xs font-medium text-zinc-500">
          {text("选择版本", "Select version")} ({versions.length})
        </label>
        <SearchableVersionSelect
          value={selectedVersion}
          options={versions.map((version) => ({
            value: version,
            label:
              packument["dist-tags"].latest === version
                ? `${version} · latest`
                : version,
          }))}
          onChange={(version) => {
            setSelectedVersion(version);
            onVersionChange?.(version);
          }}
          placeholder={text("搜索并选择 npm 版本", "Search npm versions")}
        />
        {manifest?.description ? (
          <p className="mt-3 text-xs leading-5 text-zinc-500">
            {manifest.description}
          </p>
        ) : null}
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-zinc-100">
            {packageName}@{selectedVersion}
          </span>
          {packument["dist-tags"].latest === selectedVersion ? (
            <span className="rounded bg-[var(--ag-status-success-soft)] px-1.5 py-0.5 text-xs text-[var(--ag-status-success)]">
              latest
            </span>
          ) : null}
          {artifactMetadata?.source === "proxy" ? (
            <Badge tone="visualization-5">Proxy</Badge>
          ) : null}
          {artifactMetadata?.cacheStatus ? (
            <Tag color={metadataOnly ? "default" : "success"} variant="filled">
              {metadataOnly
                ? text("仅元数据", "Metadata only")
                : text("已缓存", "Cached")}
            </Tag>
          ) : null}
        </div>
        <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-zinc-800/80 py-3 text-xs sm:grid-cols-4">
          <MetadataItem
            label={text("发布时间", "Published")}
            value={formatDate(publishedAt, locale)}
          />
          <MetadataItem
            label={text("发布者", "Publisher")}
            value={
              artifactMetadata?.publisher ??
              publisher ??
              text("未记录", "Not recorded")
            }
            mono
          />
          <MetadataItem
            label={text("包大小", "Package size")}
            value={
              metadataOnly
                ? text("下载后统计", "Available after download")
                : formatBytes(artifactMetadata?.size ?? size)
            }
          />
          <MetadataItem
            label={text("依赖 / 许可证", "Dependencies / license")}
            value={`${dependencyCount} / ${license ?? "—"}`}
          />
        </div>
        <div className="mt-3 grid grid-cols-3 gap-x-4 gap-y-3 text-xs">
          <MetadataItem
            label="SHA-256 digest"
            value={
              metadataOnly
                ? text("下载后计算", "Computed after download")
                : (artifactMetadata?.digest ?? text("未记录", "Not recorded"))
            }
            mono
          />
          <MetadataItem
            label="SHA-512 integrity"
            value={manifest?.dist?.integrity ?? text("未记录", "Not recorded")}
            mono
          />
          <MetadataItem
            label="SHA-1 shasum"
            value={shortDigest(manifest?.dist?.shasum)}
            mono
          />
        </div>
        {artifactMetadata?.source === "proxy" ? (
          <p className="mt-3 text-xs text-zinc-500">
            {metadataOnly
              ? text(
                  "该版本已从上游发现；首次安装会校验完整性并缓存 tarball。",
                  "This version was discovered upstream. The first install verifies and caches its tarball.",
                )
              : text(
                  `制品已缓存${artifactMetadata.cachedAt ? ` · ${formatDate(artifactMetadata.cachedAt, locale)}` : ""}`,
                  `Artifact cached${artifactMetadata.cachedAt ? ` · ${formatDate(artifactMetadata.cachedAt, locale)}` : ""}`,
                )}
          </p>
        ) : null}
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          {snippets.map((snippet) => (
            <UsageSnippetBlock
              key={snippet.label}
              snippet={snippet}
              copied={copiedValue === snippet.code}
              onCopy={() => void copy(snippet.code)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
