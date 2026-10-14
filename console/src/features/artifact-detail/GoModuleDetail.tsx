import { useEffect, useMemo, useState } from "react";
import { Alert, Button } from "antd";
import { CopyOutlined, DownloadOutlined } from "@ant-design/icons";
import { useAuth } from "../../lib/auth";
import { formatBytes, formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { goUsage } from "../../lib/usage";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";
import { useClipboardAction } from "../../components/ui/ConsolePrimitives";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";

interface GoModuleInfo {
  Version: string;
  Time: string;
}

function escapeGoValue(value: string): string {
  return value
    .split("")
    .map((character) =>
      character >= "A" && character <= "Z"
        ? `!${character.toLowerCase()}`
        : character,
    )
    .join("");
}

function goModuleBase(repoName: string, modulePath: string): string {
  const escaped = escapeGoValue(modulePath)
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");
  return `/go/${encodeURIComponent(repoName)}/${escaped}`;
}

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
  const { token } = useAuth();
  const [versions, setVersions] = useState<string[]>([]);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [info, setInfo] = useState<GoModuleInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [infoLoading, setInfoLoading] = useState(false);
  const [versionDigest, setVersionDigest] = useState<string | undefined>();
  const [error, setError] = useState("");
  const { copiedValue, copy } = useClipboardAction(1400);
  const base = useMemo(
    () => goModuleBase(repoName, modulePath),
    [modulePath, repoName],
  );
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void fetch(`${base}/@v/list`, {
      credentials: "include",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
      .then(async (response) => {
        if (!response.ok)
          throw new Error(
            t("artifactDetail.failedToLoadGoModuleVersions", {
              status: response.status,
            }),
          );
        return response.text();
      })
      .then((body) => {
        if (cancelled) return;
        const next = body
          .split(/\s+/)
          .map((version) => version.trim())
          .filter(Boolean)
          .reverse();
        setVersions(next);
        setSelectedVersion((current) =>
          initialVersion && next.includes(initialVersion)
            ? initialVersion
            : current && next.includes(current)
              ? current
              : (next[0] ?? ""),
        );
        setLoading(false);
      })
      .catch((requestError: unknown) => {
        if (cancelled) return;
        setError(
          requestError instanceof Error
            ? requestError.message
            : t("artifactDetail.failedToLoadGoVersions"),
        );
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [base, initialVersion, t, token]);

  useEffect(() => {
    if (!selectedVersion) return;
    let cancelled = false;
    setInfoLoading(true);
    const escapedVersion = encodeURIComponent(escapeGoValue(selectedVersion));
    void fetch(`${base}/@v/${escapedVersion}.info`, {
      credentials: "include",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
      .then(async (response) => {
        if (!response.ok)
          throw new Error(
            t("artifactDetail.failedToLoadVersionInfo", {
              status: response.status,
            }),
          );
        return response.json() as Promise<GoModuleInfo>;
      })
      .then((payload) => {
        if (!cancelled) setInfo(payload);
      })
      .catch((requestError: unknown) => {
        if (!cancelled)
          setError(
            requestError instanceof Error
              ? requestError.message
              : t("artifactDetail.failedToLoadVersionInfo2"),
          );
      })
      .finally(() => {
        if (!cancelled) setInfoLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [base, selectedVersion, t, token]);

  useEffect(() => {
    if (!selectedVersion) {
      setVersionDigest(undefined);
      return;
    }
    let cancelled = false;
    setVersionDigest(undefined);
    const escapedVersion = encodeURIComponent(escapeGoValue(selectedVersion));
    void fetch(`${base}/@v/${escapedVersion}.zip`, {
      method: "HEAD",
      credentials: "include",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
      .then((response) => {
        if (cancelled || !response.ok) return;
        const etag = response.headers.get("ETag")?.replaceAll('"', "") ?? "";
        if (/^[a-f0-9]{64}$/.test(etag)) setVersionDigest(`sha256:${etag}`);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [base, selectedVersion, token]);

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

  if (loading) return <Loading />;
  if (error && versions.length === 0) return <ErrorBanner error={error} />;

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
              setInfo(null);
              setError("");
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
