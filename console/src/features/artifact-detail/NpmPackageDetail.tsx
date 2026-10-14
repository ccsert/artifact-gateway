import { useEffect, useMemo, useState } from "react";
import { Tag } from "antd";
import { Badge } from "../../components/ui/Badge";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { usePreferences } from "../../lib/preferences";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { npmUsage } from "../../lib/usage";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { errorText } from "../../messages/translate";
import { useNpmPackument } from "./protocolDetailQueries";

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
  const { locale, t } = usePreferences();
  const packumentQuery = useNpmPackument(repoName, packageName);
  const packument = packumentQuery.data ?? null;
  const [selectedVersion, setSelectedVersion] = useState("");
  const { copiedValue, copy } = useClipboardAction(1400);

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

  if (packumentQuery.isPending) return <Loading />;
  if (packumentQuery.isError)
    return (
      <ErrorBanner
        error={errorText(
          packumentQuery.error,
          t,
          "artifactDetail.failedToLoadNpmPackage2",
        )}
        onRetry={() => void packumentQuery.refetch()}
      />
    );
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
        <label className="mb-1.5 block text-xs font-medium text-fg-tertiary">
          {t("artifactDetail.selectVersion")} ({versions.length})
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
          placeholder={t("artifactDetail.searchNpmVersions")}
        />
        {manifest?.description ? (
          <p className="mt-3 text-xs leading-5 text-fg-tertiary">
            {manifest.description}
          </p>
        ) : null}
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-fg-strong">
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
                ? t("artifactDetail.metadataOnly")
                : t("artifactDetail.cached")}
            </Tag>
          ) : null}
        </div>
        <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-3 border-y border-line py-3 text-xs sm:grid-cols-4">
          <MetadataItem
            label={t("artifactDetail.published")}
            value={formatDate(publishedAt, locale)}
          />
          <MetadataItem
            label={t("artifactDetail.publisher")}
            value={
              artifactMetadata?.publisher ??
              publisher ??
              t("artifactDetail.notRecorded")
            }
            mono
          />
          <MetadataItem
            label={t("artifactDetail.packageSize")}
            value={
              metadataOnly
                ? t("artifactDetail.availableAfterDownload")
                : formatBytes(artifactMetadata?.size ?? size)
            }
          />
          <MetadataItem
            label={t("artifactDetail.dependenciesLicense")}
            value={`${dependencyCount} / ${license ?? "—"}`}
          />
        </div>
        <div className="mt-3 grid grid-cols-3 gap-x-4 gap-y-3 text-xs">
          <MetadataItem
            label="SHA-256 digest"
            value={
              metadataOnly
                ? t("artifactDetail.computedAfterDownload")
                : (artifactMetadata?.digest ?? t("artifactDetail.notRecorded"))
            }
            mono
          />
          <MetadataItem
            label="SHA-512 integrity"
            value={manifest?.dist?.integrity ?? t("artifactDetail.notRecorded")}
            mono
          />
          <MetadataItem
            label="SHA-1 shasum"
            value={shortDigest(manifest?.dist?.shasum)}
            mono
          />
        </div>
        {artifactMetadata?.source === "proxy" ? (
          <p className="mt-3 text-xs text-fg-tertiary">
            {metadataOnly
              ? t(
                  "artifactDetail.thisVersionWasDiscoveredUpstreamTheFirstInstall",
                )
              : t("artifactDetail.artifactCached", {
                  cachedAt: artifactMetadata.cachedAt
                    ? ` · ${formatDate(artifactMetadata.cachedAt, locale)}`
                    : "",
                })}
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
