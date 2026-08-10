import { useEffect, useMemo, useState } from "react";
import { Alert, Button } from "antd";
import { CopyOutlined, DownloadOutlined } from "@ant-design/icons";
import { useAuth } from "../lib/auth";
import { formatBytes, formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { goUsage } from "../lib/usage";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { Loading } from "./Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "./PublicBrowsePrimitives";

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
}: {
  repositoryId?: string;
  repoName: string;
  modulePath: string;
  initialVersion?: string;
  size?: number;
  publisher?: string;
  onVersionChange?: (version: string) => void;
}) {
  const { locale, text } = usePreferences();
  const { token } = useAuth();
  const [versions, setVersions] = useState<string[]>([]);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [info, setInfo] = useState<GoModuleInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [infoLoading, setInfoLoading] = useState(false);
  const [versionDigest, setVersionDigest] = useState<string | undefined>();
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");
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
            text(
              `读取 Go 模块版本失败 (${response.status})`,
              `Failed to load Go module versions (${response.status})`,
            ),
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
            : text("读取 Go 模块版本失败", "Failed to load Go versions"),
        );
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [base, initialVersion, text, token]);

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
            text(
              `读取版本信息失败 (${response.status})`,
              `Failed to load version info (${response.status})`,
            ),
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
              : text("读取版本信息失败", "Failed to load version info"),
          );
      })
      .finally(() => {
        if (!cancelled) setInfoLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [base, selectedVersion, text, token]);

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

  const copy = async (value: string) => {
    await navigator.clipboard.writeText(value);
    setCopied(value);
    window.setTimeout(
      () => setCopied((current) => (current === value ? "" : current)),
      1400,
    );
  };

  if (loading) return <Loading />;
  if (error && versions.length === 0)
    return <Alert type="error" showIcon title={error} />;

  return (
    <div className="space-y-4 px-1 py-2">
      <ArtifactIntelligencePanel
        repositoryId={repositoryId}
        format="go"
        coordinate={`${modulePath}@${selectedVersion}`}
        digest={versionDigest}
      />
      {error && <Alert type="warning" showIcon title={error} />}
      <div className="grid gap-4 lg:grid-cols-[minmax(260px,360px)_minmax(0,1fr)]">
        <div>
          <div className="mb-1 text-[11px] font-medium text-zinc-500">
            {text("选择模块版本", "Select module version")}
          </div>
          <SearchableVersionSelect
            value={selectedVersion}
            options={versions.map((version) => ({
              value: version,
              label: version,
            }))}
            loading={infoLoading}
            placeholder={text("搜索版本", "Search versions")}
            notFoundContent={text("没有匹配版本", "No matching versions")}
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
            label={text("模块", "Module")}
            value={`${modulePath}@${selectedVersion}`}
            mono
          />
          <MetadataItem
            label={text("发布时间", "Published")}
            value={formatDate(info?.Time, locale)}
          />
          <MetadataItem
            label={text("来源", "Source")}
            value={publisher ?? text("上游代理", "Upstream proxy")}
            mono
          />
          <MetadataItem
            label={text("已知大小", "Known size")}
            value={formatBytes(size)}
          />
        </div>
      </div>

      <div>
        <div className="mb-2 text-[11px] font-medium text-zinc-500">
          {text(
            "协议资产（首次访问时缓存）",
            "Protocol assets (cached on first access)",
          )}
        </div>
        <div className="grid gap-2 lg:grid-cols-3">
          {assets.map((asset) => (
            <div
              key={asset.kind}
              className="flex min-w-0 items-center gap-2 rounded-lg border border-zinc-800 bg-zinc-950/50 px-3 py-2"
            >
              <a
                className="min-w-0 flex-1 truncate font-mono text-xs text-cyan-300 hover:text-cyan-200"
                href={asset.url}
              >
                {asset.label}
              </a>
              <Button
                type="text"
                size="small"
                aria-label={`${text("复制", "Copy")} ${asset.label}`}
                icon={<CopyOutlined />}
                onClick={() => void copy(asset.url)}
              />
              <Button
                type="text"
                size="small"
                aria-label={`${text("下载", "Download")} ${asset.label}`}
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
            copied={copied === snippet.code}
            onCopy={() => void copy(snippet.code)}
          />
        ))}
      </div>
    </div>
  );
}
