import { useEffect, useMemo, useState } from "react";
import { Tag } from "antd";
import type { TableProps } from "antd";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { usePreferences } from "../../lib/preferences";
import { ArtifactIntelligencePanel } from "./ArtifactIntelligencePanel";
import { ArtifactQuarantinePanel } from "./ArtifactQuarantinePanel";
import { ArtifactScanStatus } from "./ArtifactScanStatus";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { pypiUsage } from "../../lib/usage";
import {
  ConsoleTable,
  useClipboardAction,
} from "../../components/ui/ConsolePrimitives";
import { errorText } from "../../messages/translate";
import { usePyPIProject, type PyPIFile } from "./protocolDetailQueries";

function fileVersion(file: PyPIFile): string {
  return file["_artifact-gateway"]?.version ?? "";
}

function fileDigest(file: PyPIFile): string | undefined {
  const hash = file.hashes.sha256?.replace(/^sha256:/, "").toLowerCase();
  return hash && /^[a-f0-9]{64}$/.test(hash) ? `sha256:${hash}` : undefined;
}

export function PyPIProjectDetail({
  repositoryId,
  repoName,
  project,
  initialVersion,
  size,
  publisher,
  onVersionChange,
  canQuarantine = false,
}: {
  repositoryId?: string;
  repoName: string;
  project: string;
  initialVersion?: string;
  size?: number;
  publisher?: string;
  onVersionChange?: (version: string) => void;
  canQuarantine?: boolean;
}) {
  const { locale, t } = usePreferences();
  const projectQuery = usePyPIProject(repoName, project);
  const document = projectQuery.data ?? null;
  const [selectedVersion, setSelectedVersion] = useState("");
  const { copiedValue, copy } = useClipboardAction(1400);

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

  if (projectQuery.isPending) return <Loading />;
  if (projectQuery.isError)
    return (
      <ErrorBanner
        error={errorText(
          projectQuery.error,
          t,
          "artifactDetail.failedToLoadPyPIProject2",
        )}
        onRetry={() => void projectQuery.refetch()}
      />
    );
  if (!document || !selectedVersion) return null;

  const selectedFiles = document.files.filter(
    (file) => fileVersion(file) === selectedVersion,
  );
  const selectedDigest = selectedFiles.map(fileDigest).find(Boolean);
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
      title: t("artifactDetail.distributionFile"),
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
      title: t("artifactDetail.type"),
      key: "type",
      width: 130,
      render: (_, file) => (
        <Tag variant="filled">
          {file["_artifact-gateway"]?.["file-type"] || "sdist"}
        </Tag>
      ),
    },
    {
      title: t("artifactDetail.python"),
      key: "python",
      width: 130,
      render: (_, file) => (
        <span className="font-mono text-xs text-fg-tertiary">
          {file["requires-python"] ||
            file["_artifact-gateway"]?.["python-version"] ||
            "—"}
        </span>
      ),
    },
    {
      title: t("artifactDetail.size"),
      key: "size",
      width: 110,
      render: (_, file) => (
        <span className="text-xs text-fg-tertiary">
          {file["_artifact-gateway"]?.cached
            ? formatBytes(file["_artifact-gateway"]?.size)
            : t("artifactDetail.afterDownload")}
        </span>
      ),
    },
    {
      title: "SHA-256",
      key: "digest",
      width: 145,
      render: (_, file) => (
        <span className="font-mono text-xs text-fg-tertiary">
          {shortDigest(file.hashes.sha256)}
        </span>
      ),
    },
  ];

  return (
    <div className="grid gap-5 px-2 py-1 xl:grid-cols-[minmax(0,260px)_minmax(0,1fr)]">
      {selectedFiles.map((file) => {
        const digest = fileDigest(file);
        return digest ? (
          <ArtifactQuarantinePanel
            key={`quarantine:${file.filename}:${digest}`}
            repositoryId={repositoryId}
            coordinate={`${project}@${selectedVersion}`}
            digest={digest}
            canManage={canQuarantine}
            label={file.filename}
          />
        ) : null;
      })}
      <ArtifactScanStatus
        repositoryId={repositoryId}
        format="pypi"
        coordinate={`${project}@${selectedVersion}`}
        digest={selectedDigest}
      />
      <ArtifactIntelligencePanel
        repositoryId={repositoryId}
        format="pypi"
        coordinate={`${project}@${selectedVersion}`}
        digest={selectedDigest}
      />
      <div>
        <label className="mb-1.5 block text-xs font-medium text-fg-tertiary">
          {t("artifactDetail.selectVersion")} ({versions.length})
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
          placeholder={t("artifactDetail.searchPyPIVersions")}
        />
      </div>
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-xs text-fg-strong">
            {project}=={selectedVersion}
          </span>
          <Tag
            color={cachedCount === selectedFiles.length ? "success" : "default"}
            variant="filled"
          >
            {cachedCount === selectedFiles.length
              ? t("artifactDetail.cached")
              : t("artifactDetail.cached2", {
                  cachedCount,
                  length: selectedFiles.length,
                })}
          </Tag>
        </div>
        <div className="mt-3 grid grid-cols-4 gap-x-4 border-y border-line py-3 text-xs">
          <MetadataItem
            label={t("artifactDetail.published")}
            value={formatDate(latestMetadata?.["created-at"], locale)}
          />
          <MetadataItem
            label={t("artifactDetail.publisher")}
            value={
              latestMetadata?.publisher ??
              publisher ??
              t("artifactDetail.notRecorded")
            }
            mono
          />
          <MetadataItem
            label={t("artifactDetail.distributionFiles")}
            value={String(selectedFiles.length)}
          />
          <MetadataItem
            label={t("artifactDetail.totalSize")}
            value={totalSize > 0 ? formatBytes(totalSize) : formatBytes(size)}
          />
        </div>
        <ConsoleTable<PyPIFile>
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
              copied={copiedValue === snippet.code}
              onCopy={() => void copy(snippet.code)}
            />
          ))}
        </div>
      </div>
    </div>
  );
}
