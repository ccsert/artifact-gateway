import { useEffect, useMemo, useState } from "react";
import { Button } from "antd";
import { CopyOutlined, DownloadOutlined } from "@ant-design/icons";
import { useAuth } from "../../lib/auth";
import { usePreferences } from "../../lib/preferences";
import { shortDigest } from "../../lib/format";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { errorText } from "../../messages/translate";
import { useCargoIndex } from "./protocolDetailQueries";

export function CargoCrateDetail({
  repoName,
  crateName,
  initialVersion,
  onVersionChange,
}: {
  repoName: string;
  crateName: string;
  initialVersion?: string;
  onVersionChange?: (version: string) => void;
}) {
  const { token } = useAuth();
  const { t } = usePreferences();
  const indexQuery = useCargoIndex(repoName, crateName);
  const entries = indexQuery.data;
  const versions = useMemo(() => entries ?? [], [entries]);
  const [selectedVersion, setSelectedVersion] = useState("");
  // A deep-linked version wins; otherwise keep the pick while it still exists.
  useEffect(() => {
    if (!entries) return;
    setSelectedVersion((current) =>
      initialVersion && entries.some((entry) => entry.vers === initialVersion)
        ? initialVersion
        : current && entries.some((entry) => entry.vers === current)
          ? current
          : (entries[0]?.vers ?? ""),
    );
  }, [entries, initialVersion]);
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState("");
  const error = indexQuery.isError
    ? errorText(
        indexQuery.error,
        t,
        "artifactDetail.failedToLoadCargoVersions2",
      )
    : downloadError;
  const { copiedValue, copy } = useClipboardAction(1400);
  const base = `/cargo/${encodeURIComponent(repoName)}`;

  const selected = versions.find((entry) => entry.vers === selectedVersion);
  const downloadURL = selected
    ? `${window.location.origin}${base}/api/v1/crates/${encodeURIComponent(crateName)}/${encodeURIComponent(selected.vers)}/download`
    : "";
  const configSnippet = useMemo(
    () => ({
      label: ".cargo/config.toml",
      code: `[registries.gateway]\nindex = "sparse+${window.location.origin}${base}/"`,
    }),
    [base],
  );
  const manifestSnippet = useMemo(
    () => ({
      label: "Cargo.toml",
      code: `[dependencies]\n${crateName} = { version = "${selectedVersion}", registry = "gateway" }`,
    }),
    [crateName, selectedVersion],
  );

  const download = async () => {
    if (!selected) return;
    setDownloading(true);
    setDownloadError("");
    try {
      const response = await fetch(downloadURL, {
        credentials: "include",
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const blob = await response.blob();
      const temporaryURL = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = temporaryURL;
      link.download = `${crateName}-${selected.vers}.crate`;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(temporaryURL), 1000);
    } catch (requestError) {
      setDownloadError(
        requestError instanceof Error
          ? requestError.message
          : t("artifactDetail.downloadFailed"),
      );
    } finally {
      setDownloading(false);
    }
  };

  if (indexQuery.isPending) return <Loading />;
  if (error && versions.length === 0)
    return (
      <ErrorBanner error={error} onRetry={() => void indexQuery.refetch()} />
    );

  return (
    <div className="space-y-4 px-1 py-2">
      {error && <ErrorBanner error={error} />}
      <div className="grid gap-4 lg:grid-cols-[minmax(260px,360px)_minmax(0,1fr)]">
        <div>
          <div className="mb-1 text-xs font-medium text-fg-tertiary">
            {t("artifactDetail.selectCrateVersion")}
          </div>
          <SearchableVersionSelect
            value={selectedVersion}
            options={versions.map((entry) => ({
              value: entry.vers,
              label: entry.yanked ? `${entry.vers} · yanked` : entry.vers,
            }))}
            placeholder={t("artifactDetail.searchVersions")}
            onChange={(version) => {
              setSelectedVersion(version);
              onVersionChange?.(version);
            }}
          />
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <MetadataItem label="Crate" value={crateName} mono />
          <MetadataItem
            label={t("artifactDetail.version")}
            value={selectedVersion}
            mono
          />
          <MetadataItem
            label={t("artifactDetail.indexStatus")}
            value={selected?.yanked ? "yanked" : t("artifactDetail.available")}
          />
        </div>
      </div>
      {selected && (
        <>
          <div className="flex flex-wrap items-center gap-2 rounded-lg border border-line bg-surface-translucent px-3 py-2">
            <span
              className="font-mono text-xs text-fg-secondary"
              title={selected.cksum}
            >
              SHA-256 {shortDigest(`sha256:${selected.cksum}`)}
            </span>
            <Button
              size="small"
              icon={<CopyOutlined />}
              onClick={() => void copy(downloadURL)}
            >
              {copiedValue === downloadURL
                ? t("artifactDetail.linkCopied")
                : t("artifactDetail.copyDownloadLink")}
            </Button>
            <Button
              size="small"
              icon={<DownloadOutlined />}
              loading={downloading}
              onClick={() => void download()}
            >
              {t("artifactDetail.downloadCrate")}
            </Button>
          </div>
          <UsageSnippetBlock
            snippet={configSnippet}
            copied={copiedValue === configSnippet.code}
            onCopy={() => void copy(configSnippet.code)}
          />
          <UsageSnippetBlock
            snippet={manifestSnippet}
            copied={copiedValue === manifestSnippet.code}
            onCopy={() => void copy(manifestSnippet.code)}
          />
        </>
      )}
    </div>
  );
}
