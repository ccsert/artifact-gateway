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

interface CargoIndexEntry {
  name: string;
  vers: string;
  cksum: string;
  yanked: boolean;
}

function sparsePath(name: string): string {
  const normalized = name.toLowerCase();
  if (normalized.length === 1) return `1/${normalized}`;
  if (normalized.length === 2) return `2/${normalized}`;
  if (normalized.length === 3) return `3/${normalized[0]}/${normalized}`;
  return `${normalized.slice(0, 2)}/${normalized.slice(2, 4)}/${normalized}`;
}

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
  const { text } = usePreferences();
  const [versions, setVersions] = useState<CargoIndexEntry[]>([]);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [loading, setLoading] = useState(true);
  const [downloading, setDownloading] = useState(false);
  const [error, setError] = useState("");
  const { copiedValue, copy } = useClipboardAction(1400);
  const base = `/cargo/${encodeURIComponent(repoName)}`;

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void fetch(`${base}/${sparsePath(crateName)}`, {
      credentials: "include",
      headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    })
      .then(async (response) => {
        if (!response.ok)
          throw new Error(
            text(
              `读取 Cargo 版本失败 (${response.status})`,
              `Failed to load Cargo versions (${response.status})`,
            ),
          );
        return response.text();
      })
      .then((body) => {
        if (cancelled) return;
        const entries = body
          .split("\n")
          .filter(Boolean)
          .map((line) => JSON.parse(line) as CargoIndexEntry)
          .filter(
            (entry) => entry.name.toLowerCase() === crateName.toLowerCase(),
          )
          .sort((left, right) =>
            right.vers.localeCompare(left.vers, undefined, { numeric: true }),
          );
        setVersions(entries);
        setSelectedVersion((current) =>
          initialVersion &&
          entries.some((entry) => entry.vers === initialVersion)
            ? initialVersion
            : current && entries.some((entry) => entry.vers === current)
              ? current
              : (entries[0]?.vers ?? ""),
        );
      })
      .catch((requestError: unknown) => {
        if (!cancelled)
          setError(
            requestError instanceof Error
              ? requestError.message
              : text("读取 Cargo 版本失败", "Failed to load Cargo versions"),
          );
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [base, crateName, initialVersion, text, token]);

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
    setError("");
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
      setError(
        requestError instanceof Error
          ? requestError.message
          : text("下载失败", "Download failed"),
      );
    } finally {
      setDownloading(false);
    }
  };

  if (loading) return <Loading />;
  if (error && versions.length === 0) return <ErrorBanner error={error} />;

  return (
    <div className="space-y-4 px-1 py-2">
      {error && <ErrorBanner error={error} />}
      <div className="grid gap-4 lg:grid-cols-[minmax(260px,360px)_minmax(0,1fr)]">
        <div>
          <div className="mb-1 text-xs font-medium text-fg-tertiary">
            {text("选择 crate 版本", "Select crate version")}
          </div>
          <SearchableVersionSelect
            value={selectedVersion}
            options={versions.map((entry) => ({
              value: entry.vers,
              label: entry.yanked ? `${entry.vers} · yanked` : entry.vers,
            }))}
            placeholder={text("搜索版本", "Search versions")}
            onChange={(version) => {
              setSelectedVersion(version);
              onVersionChange?.(version);
            }}
          />
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
          <MetadataItem label="Crate" value={crateName} mono />
          <MetadataItem
            label={text("版本", "Version")}
            value={selectedVersion}
            mono
          />
          <MetadataItem
            label={text("索引状态", "Index status")}
            value={selected?.yanked ? "yanked" : text("可解析", "Available")}
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
                ? text("已复制链接", "Link copied")
                : text("复制下载链接", "Copy download link")}
            </Button>
            <Button
              size="small"
              icon={<DownloadOutlined />}
              loading={downloading}
              onClick={() => void download()}
            >
              {text("下载 .crate", "Download .crate")}
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
