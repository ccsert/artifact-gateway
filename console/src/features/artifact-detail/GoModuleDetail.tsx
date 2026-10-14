import { useEffect, useMemo, useState } from "react";
import { Alert, Button } from "antd";
import { CopyOutlined, DownloadOutlined } from "@ant-design/icons";
import { formatBytes, formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { goUsage } from "../../lib/usage";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { errorText } from "../../messages/translate";
import {
  escapeGoValue,
  goModuleBase,
  useGoModuleDigest,
  useGoModuleInfo,
  useGoModuleVersions,
} from "./protocolDetailQueries";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";

export function GoModuleDetail({
  repositoryId,
  repoName,
  modulePath,
  initialVersion,
  size,
  publisher,
  onVersionChange,
  canQuarantine = false,
}: {
  repositoryId?: string;
  repoName: string;
  modulePath: string;
  initialVersion?: string;
  size?: number;
  publisher?: string;
  onVersionChange?: (version: string) => void;
  canQuarantine?: boolean;
}) {
  const { locale, t } = usePreferences();
  const versionsQuery = useGoModuleVersions(repoName, modulePath);
  const versionList = versionsQuery.data;
  const versions = useMemo(() => versionList ?? [], [versionList]);
  const [selectedVersion, setSelectedVersion] = useState("");
  // A deep-linked version wins; otherwise keep the pick while it still exists.
  useEffect(() => {
    if (!versionList) return;
    setSelectedVersion((current) =>
      initialVersion && versionList.includes(initialVersion)
        ? initialVersion
        : current && versionList.includes(current)
          ? current
          : (versionList[0] ?? ""),
    );
  }, [initialVersion, versionList]);
  const infoQuery = useGoModuleInfo(repoName, modulePath, selectedVersion);
  const info = infoQuery.data ?? null;
  const infoLoading = infoQuery.isFetching;
  const versionDigest =
    useGoModuleDigest(repoName, modulePath, selectedVersion).data ?? undefined;
  const error = versionsQuery.isError
    ? errorText(versionsQuery.error, t, "artifactDetail.failedToLoadGoVersions")
    : infoQuery.isError
      ? errorText(infoQuery.error, t, "artifactDetail.failedToLoadVersionInfo2")
      : "";
  const { copiedValue, copy } = useClipboardAction(1400);
  const base = goModuleBase(repoName, modulePath);

  const snippets = useMemo(
    () =>
      selectedVersion ? goUsage(repoName, modulePath, selectedVersion) : [],
    [modulePath, repoName, selectedVersion],
  );
  const assets = selectedVersion
    ? (["info", "mod", "zip"] as const).map((kind) => ({
        kind,
        label: `${selectedVersion}.${kind}`,
        url: `${window.location.origin}${base}/@v/${encodeURIComponent(escapeGoValue(selectedVersion))}.${kind}`,
      }))
    : [];

  if (versionsQuery.isPending) return <Loading />;
  if (error && versions.length === 0)
    return (
      <ErrorBanner error={error} onRetry={() => void versionsQuery.refetch()} />
    );

  return (
    <div className="space-y-4 px-1 py-2">
      <ArtifactQuarantinePanel
        repositoryId={repositoryId}
        coordinate={`${modulePath}@${selectedVersion}`}
        digest={versionDigest}
        canManage={canQuarantine}
      />
      <ArtifactScanStatus
        repositoryId={repositoryId}
        format="go"
        coordinate={`${modulePath}@${selectedVersion}`}
        digest={versionDigest}
      />
      <ArtifactIntelligencePanel
        repositoryId={repositoryId}
        format="go"
        coordinate={`${modulePath}@${selectedVersion}`}
        digest={versionDigest}
      />
      {error && <Alert type="warning" showIcon title={error} />}
      <div className="grid gap-4 lg:grid-cols-[minmax(260px,360px)_minmax(0,1fr)]">
        <div>
          <div className="mb-1 text-xs font-medium text-fg-tertiary">
            {t("artifactDetail.selectModuleVersion")}
          </div>
          <SearchableVersionSelect
            value={selectedVersion}
            options={versions.map((version) => ({
              value: version,
              label: version,
            }))}
            loading={infoLoading}
            placeholder={t("artifactDetail.searchVersions")}
            notFoundContent={t("artifactDetail.noMatchingVersions")}
            onChange={(version) => {
              setSelectedVersion(version);
              onVersionChange?.(version);
            }}
          />
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <MetadataItem
            label={t("artifactDetail.module")}
            value={`${modulePath}@${selectedVersion}`}
            mono
          />
          <MetadataItem
            label={t("artifactDetail.published")}
            value={formatDate(info?.Time, locale)}
          />
          <MetadataItem
            label={t("artifactDetail.source2")}
            value={publisher ?? t("artifactDetail.upstreamProxy")}
            mono
          />
          <MetadataItem
            label={t("artifactDetail.knownSize")}
            value={formatBytes(size)}
          />
        </div>
      </div>

      <div>
        <div className="mb-2 text-xs font-medium text-fg-tertiary">
          {t("artifactDetail.protocolAssetsCachedOnFirstAccess")}
        </div>
        <div className="grid gap-2 lg:grid-cols-3">
          {assets.map((asset) => (
            <div
              key={asset.kind}
              className="flex min-w-0 items-center gap-2 rounded-lg border border-line bg-surface-translucent px-3 py-2"
            >
              <a
                className="min-w-0 flex-1 truncate font-mono text-xs text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
                href={asset.url}
              >
                {asset.label}
              </a>
              <Button
                type="text"
                size="small"
                aria-label={`${t("artifactDetail.copy")} ${asset.label}`}
                icon={<CopyOutlined />}
                onClick={() => void copy(asset.url)}
              />
              <Button
                type="text"
                size="small"
                aria-label={`${t("artifactDetail.download")} ${asset.label}`}
                icon={<DownloadOutlined />}
                href={asset.url}
              />
            </div>
          ))}
        </div>
      </div>

      <div className="grid gap-2 lg:grid-cols-3">
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
  );
}
