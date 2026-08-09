import { useEffect, useMemo, useState } from "react";
import { Alert } from "antd";
import { Loading } from "./Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "./PublicBrowsePrimitives";
import { usePreferences } from "../lib/preferences";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { npmUsage } from "../lib/usage";

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
  };
}

interface NpmPackument {
  name: string;
  "dist-tags": Record<string, string>;
  versions: Record<string, NpmVersionManifest>;
  time?: Record<string, string>;
}

export function NpmPackageDetail({
  repoName,
  packageName,
  initialVersion,
  size,
  publisher,
  onVersionChange,
}: {
  repoName: string;
  packageName: string;
  initialVersion?: string;
  size?: number;
  publisher?: string;
  onVersionChange?: (version: string) => void;
}) {
  const { locale, text } = usePreferences();
  const [packument, setPackument] = useState<NpmPackument | null>(null);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void fetch(
      `/npm/${encodeURIComponent(repoName)}/${encodeURIComponent(packageName)}`,
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
  }, [packageName, repoName, text]);

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

  return (
    <div className="grid gap-5 px-2 py-1 xl:grid-cols-[minmax(0,260px)_minmax(0,1fr)]">
      <div>
        <label className="mb-1.5 block text-[11px] font-medium text-zinc-500">
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
            <span className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-300">
              latest
            </span>
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
            value={formatBytes(artifactMetadata?.size ?? size)}
          />
          <MetadataItem
            label={text("依赖 / 许可证", "Dependencies / license")}
            value={`${dependencyCount} / ${license ?? "—"}`}
          />
        </div>
        <div className="mt-3 grid grid-cols-3 gap-x-4 gap-y-3 text-xs">
          <MetadataItem
            label="SHA-256 digest"
            value={artifactMetadata?.digest ?? text("未记录", "Not recorded")}
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
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          {snippets.map((snippet) => (
            <UsageSnippetBlock
              key={snippet.label}
              snippet={snippet}
              copied={copied === snippet.code}
              onCopy={() => {
                void navigator.clipboard.writeText(snippet.code).then(() => {
                  setCopied(snippet.code);
                  window.setTimeout(
                    () =>
                      setCopied((current) =>
                        current === snippet.code ? "" : current,
                      ),
                    1400,
                  );
                });
              }}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
