import { useEffect, useMemo, useState } from "react";
import { Alert, Table, Tag } from "antd";
import type { TableProps } from "antd";
import { Loading } from "./Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "./PublicBrowsePrimitives";
import { usePreferences } from "../lib/preferences";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { useAuth } from "../lib/auth";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { pypiUsage } from "../lib/usage";

interface PyPIFile {
  filename: string;
  url: string;
  hashes: { sha256?: string };
  "requires-python"?: string;
  "_artifact-gateway"?: {
    version?: string;
    size?: number;
    publisher?: string;
    "created-at"?: string;
    "cached-at"?: string;
    cached?: boolean;
    "file-type"?: string;
    "python-version"?: string;
  };
}

interface PyPISimpleProject {
  name: string;
  files: PyPIFile[];
}

function fileVersion(file: PyPIFile): string {
  return file["_artifact-gateway"]?.version ?? "";
}

export function PyPIProjectDetail({
  repositoryId,
  repoName,
  project,
  initialVersion,
  size,
  publisher,
  onVersionChange,
}: {
  repositoryId?: string;
  repoName: string;
  project: string;
  initialVersion?: string;
  size?: number;
  publisher?: string;
  onVersionChange?: (version: string) => void;
}) {
  const { locale, text } = usePreferences();
  const { token } = useAuth();
  const [document, setDocument] = useState<PyPISimpleProject | null>(null);
  const [selectedVersion, setSelectedVersion] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    void fetch(
      `/pypi/${encodeURIComponent(repoName)}/simple/${encodeURIComponent(project)}/`,
      {
        credentials: "include",
        headers: {
          Accept: "application/vnd.pypi.simple.v1+json",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
      },
    )
      .then(async (response) => {
        if (!response.ok)
          throw new Error(
            text(
              `读取 PyPI 项目失败 (${response.status})`,
              `Failed to load PyPI project (${response.status})`,
            ),
          );
        return response.json() as Promise<PyPISimpleProject>;
      })
      .then((payload) => {
        if (cancelled) return;
        setDocument(payload);
        setLoading(false);
      })
      .catch((requestError: unknown) => {
        if (cancelled) return;
        setError(
          requestError instanceof Error
            ? requestError.message
            : text("读取 PyPI 项目失败", "Failed to load PyPI project"),
        );
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [project, repoName, text, token]);

  const versions = useMemo(() => {
    const byVersion = new Map<string, string>();
    for (const file of document?.files ?? []) {
      const version = fileVersion(file);
      if (!version) continue;
      const createdAt = file["_artifact-gateway"]?.["created-at"] ?? "";
      if (createdAt > (byVersion.get(version) ?? ""))
        byVersion.set(version, createdAt);
    }
    return [...byVersion.entries()]
      .sort(
        ([leftVersion, leftTime], [rightVersion, rightTime]) =>
          rightTime.localeCompare(leftTime) ||
          rightVersion.localeCompare(leftVersion, undefined, { numeric: true }),
      )
      .map(([version]) => version);
  }, [document]);

  useEffect(() => {
    if (versions.length === 0) return;
    setSelectedVersion((current) =>
      initialVersion && versions.includes(initialVersion)
        ? initialVersion
        : current && versions.includes(current)
          ? current
          : versions[0],
    );
  }, [initialVersion, versions]);

  if (loading) return <Loading />;
  if (error)
    return <Alert showIcon type="error" title={error} className="my-2" />;
  if (!document || !selectedVersion) return null;

  const selectedFiles = document.files.filter(
    (file) => fileVersion(file) === selectedVersion,
  );
  const selectedDigest = selectedFiles.find((file) => file.hashes.sha256)
    ?.hashes.sha256;
  const latestMetadata = selectedFiles[0]?.["_artifact-gateway"];
  const totalSize = selectedFiles.reduce(
    (total, file) => total + (file["_artifact-gateway"]?.size ?? 0),
    0,
  );
  const cachedCount = selectedFiles.filter(
    (file) => file["_artifact-gateway"]?.cached,
  ).length;
  const snippets = pypiUsage(repoName, project, selectedVersion);
  const columns: TableProps<PyPIFile>["columns"] = [
    {
      title: text("分发文件", "Distribution file"),
      dataIndex: "filename",
      key: "filename",
      ellipsis: true,
      render: (value: string) => (
        <span className="font-mono text-xs" title={value}>
          {value}
        </span>
      ),
    },
    {
      title: text("类型", "Type"),
      key: "type",
      width: 130,
      render: (_, file) => (
        <Tag variant="filled">
          {file["_artifact-gateway"]?.["file-type"] || "sdist"}
        </Tag>
      ),
    },
    {
      title: text("Python", "Python"),
      key: "python",
      width: 130,
      render: (_, file) => (
        <span className="font-mono text-xs text-zinc-500">
          {file["requires-python"] ||
            file["_artifact-gateway"]?.["python-version"] ||
            "—"}
        </span>
      ),
    },
    {
      title: text("大小", "Size"),
      key: "size",
      width: 110,
      render: (_, file) => (
        <span className="text-xs text-zinc-500">
          {file["_artifact-gateway"]?.cached
            ? formatBytes(file["_artifact-gateway"]?.size)
            : text("首次下载后", "After download")}
        </span>
      ),
    },
    {
      title: "SHA-256",
      key: "digest",
      width: 145,
      render: (_, file) => (
        <span className="font-mono text-xs text-zinc-500">
          {shortDigest(file.hashes.sha256)}
        </span>
      ),
    },
  ];

  return (
    <div className="grid gap-5 px-2 py-1 xl:grid-cols-[minmax(0,260px)_minmax(0,1fr)]">
      <ArtifactIntelligencePanel
        repositoryId={repositoryId}
        format="pypi"
        coordinate={project}
        digest={selectedDigest ? `sha256:${selectedDigest}` : undefined}
      />
      <div>
        <label className="mb-1.5 block text-[11px] font-medium text-zinc-500">
          {text("选择版本", "Select version")} ({versions.length})
        </label>
        <SearchableVersionSelect
          value={selectedVersion}
          options={versions.map((version) => ({
            value: version,
            label: version,
          }))}
          onChange={(version) => {
            setSelectedVersion(version);
            onVersionChange?.(version);
          }}
          placeholder={text("搜索并选择 PyPI 版本", "Search PyPI versions")}
        />
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-zinc-100">
            {project}=={selectedVersion}
          </span>
          <Tag
            color={cachedCount === selectedFiles.length ? "green" : "default"}
            variant="filled"
          >
            {cachedCount === selectedFiles.length
              ? text("已缓存", "Cached")
              : text(
                  `${cachedCount}/${selectedFiles.length} 已缓存`,
                  `${cachedCount}/${selectedFiles.length} cached`,
                )}
          </Tag>
        </div>
        <div className="mt-3 grid grid-cols-4 gap-x-4 border-y border-zinc-800/80 py-3 text-xs">
          <MetadataItem
            label={text("发布时间", "Published")}
            value={formatDate(latestMetadata?.["created-at"], locale)}
          />
          <MetadataItem
            label={text("发布者", "Publisher")}
            value={
              latestMetadata?.publisher ??
              publisher ??
              text("未记录", "Not recorded")
            }
            mono
          />
          <MetadataItem
            label={text("分发文件", "Distribution files")}
            value={String(selectedFiles.length)}
          />
          <MetadataItem
            label={text("总大小", "Total size")}
            value={totalSize > 0 ? formatBytes(totalSize) : formatBytes(size)}
          />
        </div>
        <Table<PyPIFile>
          className="ag-console-table mt-3"
          rowKey="filename"
          size="small"
          tableLayout="fixed"
          columns={columns}
          dataSource={selectedFiles}
          pagination={false}
          scroll={{ x: 760, y: 220 }}
        />
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
